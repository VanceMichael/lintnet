package testcmd_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	gojsonnet "github.com/google/go-jsonnet"
	"github.com/lintnet/lintnet/pkg/controller/testcmd"
	"github.com/lintnet/lintnet/pkg/testutil"
)

func runTest(t *testing.T, files map[string]string, param *testcmd.ParamTest) (string, error) {
	t.Helper()
	fs, err := testutil.NewFs(files)
	if err != nil {
		t.Fatal(err)
	}
	stdout := &bytes.Buffer{}
	ctrl := testcmd.NewController(&testcmd.ParamController{
		Version: "v1.0.0",
	}, fs, stdout, &gojsonnet.MemoryImporter{})
	err = ctrl.Test(t.Context(), slog.New(slog.DiscardHandler), param)
	return stdout.String(), err
}

// data_file: an oversized case fails but subsequent cases still run.
func TestController_Test_MaxDataBytes_DataFile(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"hello.jsonnet": `function(param)
  [
    {
      name: 'hello',
      message: param.data.value.name,
    },
  ]
`,
		"hello_test.jsonnet": `[
  {
    name: 'oversize',
    data_file: 'big.txt',
    result: [],
  },
  {
    name: 'after',
    data_file: 'small.json',
    result: [],
  },
]
`,
		"big.txt":    strings.Repeat("x", 300),
		"small.json": `{"name":"small"}
`,
	}
	param := &testcmd.ParamTest{
		FilePaths:    []string{"hello_test.jsonnet"},
		MaxDataBytes: "100",
	}
	out, err := runTest(t, files, param)
	if err == nil {
		t.Fatal("the command must fail")
	}
	if !strings.Contains(out, "Test Name: oversize") {
		t.Fatalf("the oversize case must be reported: %s", out)
	}
	if oversizeBlock := strings.SplitN(strings.SplitN(out, "Test Name: oversize", 2)[1], "==========", 2)[0]; !strings.Contains(oversizeBlock, "max_data_bytes") {
		t.Fatalf("the error must mention max_data_bytes: %s", oversizeBlock)
	}
	if !strings.Contains(out, "Test Name: after") {
		t.Fatalf("subsequent cases must not be skipped: %s", out)
	}
	if afterBlock := strings.SplitN(strings.SplitN(out, "Test Name: after", 2)[1], "==========", 2)[0]; !strings.Contains(afterBlock, "Diff") {
		t.Fatalf("the later case must be executed and report its diff: %s", afterBlock)
	}
}

// data_files: the cumulative size across files follows the same budget.
func TestController_Test_MaxDataBytes_DataFiles(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"hello.jsonnet": `function(param)
  [
    {
      name: 'hello',
      message: param.data.text,
    },
  ]
`,
		"hello_test.jsonnet": `[
  {
    name: 'oversize',
    data_files: ['a.txt', 'b.txt'],
    result: [],
  },
  {
    name: 'after',
    data_file: 'small.json',
    result: [],
  },
]
`,
		"a.txt": "a",
		"b.txt": strings.Repeat("x", 300),
		"small.json": `{"name":"small"}
`,
	}
	param := &testcmd.ParamTest{
		FilePaths:    []string{"hello_test.jsonnet"},
		MaxDataBytes: "100",
	}
	out, err := runTest(t, files, param)
	if err == nil {
		t.Fatal("the command must fail")
	}
	if !strings.Contains(out, "Test Name: oversize") {
		t.Fatalf("the oversize case must be reported: %s", out)
	}
	oversizeBlock := strings.SplitN(strings.SplitN(out, "Test Name: oversize", 2)[1], "==========", 2)[0]
	if !strings.Contains(oversizeBlock, "a.txt") || !strings.Contains(oversizeBlock, "b.txt") {
		t.Fatalf("participating files must be reported: %s", oversizeBlock)
	}
	if !strings.Contains(out, "Test Name: after") {
		t.Fatalf("subsequent cases must not be skipped: %s", out)
	}
}

// Without a limit the unlimited behavior is preserved.
func TestController_Test_MaxDataBytes_Unlimited(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"hello.jsonnet": `function(param)
  [
    {
      name: 'hello',
      message: param.data.value.name,
    },
  ]
`,
		"hello_test.jsonnet": `[
  {
    name: 'normal',
    data_file: 'small.json',
    result: [
      {
        name: 'hello',
        message: 'small',
      },
    ],
  },
]
`,
		"small.json": `{"name":"small"}
`,
	}
	param := &testcmd.ParamTest{
		FilePaths: []string{"hello_test.jsonnet"},
	}
	out, err := runTest(t, files, param)
	if err != nil {
		t.Fatalf("the command must succeed: %v\n%s", err, out)
	}
}

// An invalid flag value fails before test cases are executed.
func TestController_Test_MaxDataBytes_InvalidFlag(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"hello.jsonnet": `function(param) []
`,
		"hello_test.jsonnet": `[
  {
    name: 'normal',
    data_file: 'small.txt',
    result: [],
  },
]
`,
		"small.txt": "x",
	}
	for _, v := range []string{"0", "-1", "abc", "1.5"} {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			param := &testcmd.ParamTest{
				FilePaths:    []string{"hello_test.jsonnet"},
				MaxDataBytes: v,
			}
			out, err := runTest(t, files, param)
			if err == nil {
				t.Fatal("an error must be returned")
			}
			if out != "" {
				t.Fatalf("test cases must not be executed: %s", out)
			}
		})
	}
}
