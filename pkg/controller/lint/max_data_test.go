package lint_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"

	gojsonnet "github.com/google/go-jsonnet"
	"github.com/lintnet/lintnet/pkg/controller/lint"
	"github.com/lintnet/lintnet/pkg/testutil"
)

const (
	pwd         = "/workspace"
	dataRootDir = "/workspace"
	rootDir     = "/root"
)

func readOutput(t *testing.T, stdout *bytes.Buffer) []map[string]any {
	t.Helper()
	out := map[string]any{}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, stdout.String())
	}
	arr, ok := out["errors"].([]any)
	if !ok {
		t.Fatalf("errors must be an array: %v", out)
	}
	errors := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		m, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("each error must be an object: %v", e)
		}
		errors = append(errors, m)
	}
	return errors
}

func findError(t *testing.T, errors []map[string]any, dataFile string) map[string]any {
	t.Helper()
	for _, e := range errors {
		if e["data_file"] == dataFile {
			return e
		}
	}
	t.Fatalf("an error for %q isn't found: %v", dataFile, errors)
	return nil
}

func runLint(t *testing.T, files map[string]string, param *lint.ParamLint) ([]map[string]any, error) {
	t.Helper()
	fs, err := testutil.NewFs(files)
	if err != nil {
		t.Fatal(err)
	}
	stdout := &bytes.Buffer{}
	ctrl := lint.NewController(&lint.ParamController{
		Version: "v1.0.0",
		Env:     "darwin/arm64",
	}, fs, stdout, &lint.MockModuleInstaller{}, &gojsonnet.MemoryImporter{})
	err = ctrl.Lint(t.Context(), slog.New(slog.DiscardHandler), param)
	if stdout.Len() == 0 {
		return nil, err
	}
	return readOutput(t, stdout), err
}

// A normal rule reports an oversized file with target and data path,
// and other files are still processed.
func TestController_Lint_MaxDataBytes_NormalRule(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"lintnet.jsonnet": `function(param) {
  targets: [
    {
      id: 't1',
      data_files: ['*.json', '*.txt'],
      lint_files: ['hello.jsonnet'],
    },
  ],
}
`,
		"/workspace/hello.jsonnet": `function(param)
  [
    {
      name: 'hello',
      message: param.data.value.name,
    },
  ]
`,
		"/workspace/small.json": `{"name":"small"}
`,
		"/workspace/big.txt": strings.Repeat("x", 300),
	}
	param := &lint.ParamLint{
		RootDir:      rootDir,
		DataRootDir:  dataRootDir,
		PWD:          pwd,
		MaxDataBytes: "100",
	}
	errors, err := runLint(t, files, param)
	if err == nil {
		t.Fatal("the command must fail")
	}
	if len(errors) != 2 {
		t.Fatalf("2 errors must be output, but %d errors were output: %v", len(errors), errors)
	}
	big := findError(t, errors, "big.txt")
	if big["target_id"] != "t1" {
		t.Fatalf("target_id must be t1: %v", big)
	}
	msg, _ := big["message"].(string)
	if !strings.Contains(msg, "max_data_bytes") {
		t.Fatalf("message must mention max_data_bytes: %v", big)
	}
	if _, ok := big["name"]; ok {
		t.Fatalf("a read error must not have a rule name: %v", big)
	}
	small := findError(t, errors, "small.json")
	if small["name"] != "hello" || small["message"] != "small" {
		t.Fatalf("the small file must be linted normally: %v", small)
	}
}

// A combined rule isn't executed when the cumulative size exceeds the limit.
// It reports participating files, while non-combine results are kept.
func TestController_Lint_MaxDataBytes_CombinedRule(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"lintnet.jsonnet": `function(param) {
  targets: [
    {
      id: 't2',
      data_files: ['a.json', 'b.json'],
      lint_files: ['hello.jsonnet', 'hello_combine.jsonnet'],
    },
  ],
}
`,
		"/workspace/hello.jsonnet": `function(param)
  [
    {
      name: 'hello',
      message: param.data.value.name,
    },
  ]
`,
		"/workspace/hello_combine.jsonnet": `function(param)
  [
    {
      name: 'combined',
      message: '%d' % std.length(param.combined_data),
    },
  ]
`,
		"/workspace/a.json": `{"name":"a"}
`,
		"/workspace/b.json": strings.Repeat("x", 300),
	}
	param := &lint.ParamLint{
		RootDir:      rootDir,
		DataRootDir:  dataRootDir,
		PWD:          pwd,
		MaxDataBytes: "100",
	}
	errors, err := runLint(t, files, param)
	if err == nil {
		t.Fatal("the command must fail")
	}
	if len(errors) != 3 {
		t.Fatalf("3 errors must be output, but %d errors were output: %v", len(errors), errors)
	}
	a := findError(t, errors, "a.json")
	if a["name"] != "hello" {
		t.Fatalf("the non-combine result of a.json must be kept: %v", a)
	}
	b := findError(t, errors, "b.json")
	if !strings.Contains(b["message"].(string), "max_data_bytes") {
		t.Fatalf("b.json must be reported as oversized: %v", b)
	}
	// The combined diagnostic reports participating files.
	combined := slices.DeleteFunc(slices.Clone(errors), func(e map[string]any) bool {
		_, ok := e["data_file"]
		return ok
	})
	if len(combined) != 1 {
		t.Fatalf("one combined diagnostic must be output: %v", errors)
	}
	msg := combined[0]["message"].(string)
	if !strings.Contains(msg, "a.json") || !strings.Contains(msg, "b.json") {
		t.Fatalf("the combined diagnostic must report participating files: %s", msg)
	}
	for _, e := range errors {
		if e["name"] == "combined" {
			t.Fatalf("the combined rule must not be executed: %v", e)
		}
	}
}

// Config value and --max-data-bytes override precedence.
func TestController_Lint_MaxDataBytes_Override(t *testing.T) { //nolint:funlen
	t.Parallel()
	rule := `function(param) []
`
	data := []struct {
		name     string
		cfgLimit string
		flag     string
		fileSize int
		isErr    bool
	}{
		{
			name:     "config value is honored",
			cfgLimit: "100",
			fileSize: 150,
			isErr:    true,
		},
		{
			name:     "flag raises the config limit",
			cfgLimit: "100",
			flag:     "200",
			fileSize: 150,
			isErr:    false,
		},
		{
			name:     "flag lowers the config limit",
			cfgLimit: "1000",
			flag:     "50",
			fileSize: 150,
			isErr:    true,
		},
		{
			name:     "no limit keeps unlimited behavior",
			fileSize: 150,
			isErr:    false,
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			cfg := `function(param) {
  targets: [
    {
      data_files: ['big.txt'],
      lint_files: ['rule.jsonnet'],
    },
  ],
`
			if d.cfgLimit != "" {
				cfg += "  max_data_bytes: " + d.cfgLimit + ",\n"
			}
			cfg += "}\n"
			files := map[string]string{
				"lintnet.jsonnet":         cfg,
				"/workspace/rule.jsonnet": rule,
				"/workspace/big.txt":      strings.Repeat("x", d.fileSize),
			}
			param := &lint.ParamLint{
				RootDir:      rootDir,
				DataRootDir:  dataRootDir,
				PWD:          pwd,
				MaxDataBytes: d.flag,
			}
			_, err := runLint(t, files, param)
			if d.isErr != (err != nil) {
				t.Fatalf("got error %v, wanted isErr %v", err, d.isErr)
			}
		})
	}
}

// An invalid flag value fails before rules are executed.
func TestController_Lint_MaxDataBytes_InvalidFlag(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"lintnet.jsonnet": `function(param) {
  targets: [
    {
      data_files: ['small.txt'],
      lint_files: ['rule.jsonnet'],
    },
  ],
}
`,
		"/workspace/rule.jsonnet": `function(param) []
`,
		"/workspace/small.txt": "x",
	}
	for _, v := range []string{"0", "-1", "abc", "1.5"} {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			param := &lint.ParamLint{
				RootDir:      rootDir,
				DataRootDir:  dataRootDir,
				PWD:          pwd,
				MaxDataBytes: v,
			}
			stdout := &bytes.Buffer{}
			fs, err := testutil.NewFs(files)
			if err != nil {
				t.Fatal(err)
			}
			ctrl := lint.NewController(&lint.ParamController{}, fs, stdout, &lint.MockModuleInstaller{}, &gojsonnet.MemoryImporter{})
			err = ctrl.Lint(t.Context(), slog.New(slog.DiscardHandler), param)
			if err == nil {
				t.Fatal("an error must be returned")
			}
			if stdout.Len() != 0 {
				t.Fatalf("rules must not be executed: %s", stdout.String())
			}
		})
	}
}
