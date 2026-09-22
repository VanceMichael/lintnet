package testcmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/google/go-jsonnet"
	"github.com/lintnet/lintnet/pkg/controller/testcmd"
	"github.com/lintnet/lintnet/pkg/testutil"
)

func TestController_Test(t *testing.T) { //nolint:funlen
	t.Parallel()
	data := []struct {
		name     string
		paramC   *testcmd.ParamController
		param    *testcmd.ParamTest
		files    map[string]string
		contents map[string]string
		dirs     []string
		isErr    bool
	}{
		{
			name: "normal",
			paramC: &testcmd.ParamController{
				Version: "0.3.0",
			},
			param: &testcmd.ParamTest{
				RootDir:        "/home/foo/.local/share/lintnet",
				ConfigFilePath: "",
				PWD:            "/home/foo/workspace",
			},
			files: map[string]string{
				"lintnet.jsonnet":                        "testdata/lintnet.jsonnet",
				"hello.jsonnet":                          "testdata/hello.jsonnet",
				"hello_test.jsonnet":                     "testdata/hello_test.jsonnet",
				"testdata/pass.json":                     "testdata/pass.json",
				"testdata/fail.json":                     "testdata/fail.json",
				"/home/foo/workspace/hello.jsonnet":      "testdata/hello.jsonnet",
				"/home/foo/workspace/hello_test.jsonnet": "testdata/hello_test.jsonnet",
				"/home/foo/workspace/testdata/pass.json": "testdata/pass.json",
				"/home/foo/workspace/testdata/fail.json": "testdata/fail.json",
			},
			contents: map[string]string{},
		},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			fs, err := testutil.ReadFs(d.files, d.dirs...)
			if err != nil {
				t.Fatal(err)
			}
			stdout := &bytes.Buffer{}
			data := make(map[string]jsonnet.Contents, len(d.contents))
			for k, v := range d.contents {
				data[k] = jsonnet.MakeContents(v)
			}
			importer := &jsonnet.MemoryImporter{
				Data: data,
			}
			ctrl := testcmd.NewController(d.paramC, fs, stdout, importer)
			logger := slog.New(slog.DiscardHandler)
			if err := ctrl.Test(t.Context(), logger, d.param); err != nil {
				if d.isErr {
					return
				}
				t.Fatal(err)
			}
			if d.isErr {
				t.Fatal("error must be returned")
			}
		})
	}
}

const (
	testLintRule = `function(param) [{name: 'rule1'}]`
	// One pass case, one assertion failure and one fixture read error.
	testLintTestMixed = `function(param) [
  {name: 'passing', param: {}, result: [{name: 'rule1'}]},
  {name: 'failing', param: {}, result: []},
  {name: 'errored', data_file: 'testdata/missing.json', result: []},
]`
	testLintEmpty      = `function(param) []`
	testLintTestPass   = `function(param) [{name: 'passing', param: {}, result: []}]`
	testLintTestBroken = `function(param) [`
)

func newTestController(t *testing.T, version string, files map[string]string, stdout io.Writer) *testcmd.Controller {
	t.Helper()
	fs, err := testutil.NewFs(files)
	if err != nil {
		t.Fatal(err)
	}
	return testcmd.NewController(&testcmd.ParamController{
		Version: version,
	}, fs, stdout, &jsonnet.MemoryImporter{
		Data: map[string]jsonnet.Contents{},
	})
}

func TestController_Test_JSON_passFailError(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	ctrl := newTestController(t, "0.3.0", map[string]string{
		"/workspace/rule.jsonnet":      testLintRule,
		"/workspace/rule_test.jsonnet": testLintTestMixed,
	}, stdout)

	err := ctrl.Test(t.Context(), slog.New(slog.DiscardHandler), &testcmd.ParamTest{
		FilePaths: []string{"/workspace/rule_test.jsonnet"},
		JSON:      true,
	})
	if err == nil || err.Error() != "test failed" {
		t.Fatalf("test failed must be returned, got %v", err)
	}

	report := decodeTestReport(t, stdout)
	if report.SchemaVersion != "1" {
		t.Fatalf("schema_version must be 1, got %s", report.SchemaVersion)
	}
	if report.LintnetVersion != "0.3.0" {
		t.Fatalf("lintnet_version must be 0.3.0, got %s", report.LintnetVersion)
	}
	if report.Summary.Total != 3 || report.Summary.Passed != 1 || report.Summary.Failed != 1 || report.Summary.Errors != 1 {
		t.Fatalf("bad summary: %+v", report.Summary)
	}
	// The summary must be consistent with records.
	assertSummaryConsistency(t, report)

	if len(report.Tests) != 3 {
		t.Fatalf("3 test cases must be reported, got %d", len(report.Tests))
	}

	pass := report.Tests[0]
	if pass.ID != "/workspace/rule_test.jsonnet#0" || pass.Name != "passing" ||
		pass.LintFilePath != "/workspace/rule.jsonnet" || pass.TestFilePath != "/workspace/rule_test.jsonnet" {
		t.Fatalf("bad pass record identity: %+v", pass)
	}
	if pass.Status != "pass" || pass.Diff != "" || pass.Error != "" {
		t.Fatalf("bad pass record: %+v", pass)
	}

	fail := report.Tests[1]
	if fail.ID != "/workspace/rule_test.jsonnet#1" || fail.Name != "failing" || fail.Status != "fail" {
		t.Fatalf("bad fail record: %+v", fail)
	}
	if fail.Diff == "" || !strings.Contains(fail.Diff, "rule1") {
		t.Fatalf("diff must be included, got %q", fail.Diff)
	}
	if fail.Error != "" {
		t.Fatalf("fail record must not have an error, got %q", fail.Error)
	}
	actual, ok := fail.Actual.([]any)
	if !ok || len(actual) != 1 {
		t.Fatalf("actual must be the lint result, got %#v", fail.Actual)
	}

	errored := report.Tests[2]
	if errored.ID != "/workspace/rule_test.jsonnet#2" || errored.Name != "errored" || errored.Status != "error" {
		t.Fatalf("bad error record: %+v", errored)
	}
	if !strings.Contains(errored.Error, "read a data file") {
		t.Fatalf("error message must be retained, got %q", errored.Error)
	}
	if errored.Diff != "" {
		t.Fatalf("error record must not have a diff, got %q", errored.Diff)
	}
}

func TestController_Test_JSON_allPass(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	ctrl := newTestController(t, "0.3.0", map[string]string{
		"/workspace/rule.jsonnet":      testLintEmpty,
		"/workspace/rule_test.jsonnet": testLintTestPass,
	}, stdout)

	// JSON is always output even if all tests pass.
	if err := ctrl.Test(t.Context(), slog.New(slog.DiscardHandler), &testcmd.ParamTest{
		FilePaths: []string{"/workspace/rule_test.jsonnet"},
		JSON:      true,
	}); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() == 0 {
		t.Fatal("JSON report must be output even if all tests pass")
	}
	report := decodeTestReport(t, stdout)
	if report.Summary.Total != 1 || report.Summary.Passed != 1 || report.Summary.Failed != 0 || report.Summary.Errors != 0 {
		t.Fatalf("bad summary: %+v", report.Summary)
	}
	assertSummaryConsistency(t, report)
	if report.Tests[0].Status != "pass" {
		t.Fatalf("status must be pass, got %s", report.Tests[0].Status)
	}
}

func TestController_Test_JSON_fileErrorDoesNotTruncateRun(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	ctrl := newTestController(t, "0.3.0", map[string]string{
		"/workspace/bad.jsonnet":       testLintEmpty,
		"/workspace/bad_test.jsonnet":  testLintTestBroken,
		"/workspace/good.jsonnet":      testLintEmpty,
		"/workspace/good_test.jsonnet": testLintTestPass,
	}, stdout)

	err := ctrl.Test(t.Context(), slog.New(slog.DiscardHandler), &testcmd.ParamTest{
		FilePaths: []string{"/workspace/bad_test.jsonnet", "/workspace/good_test.jsonnet"},
		JSON:      true,
	})
	if err == nil || err.Error() != "test failed" {
		t.Fatalf("test failed must be returned, got %v", err)
	}

	report := decodeTestReport(t, stdout)
	if report.Summary.Total != 2 || report.Summary.Errors != 1 || report.Summary.Passed != 1 || report.Summary.Failed != 0 {
		t.Fatalf("bad summary: %+v", report.Summary)
	}
	assertSummaryConsistency(t, report)
	if len(report.Tests) != 2 {
		t.Fatalf("the file-level error and the other pair's result must be both reported, got %d", len(report.Tests))
	}

	fileErr := report.Tests[0]
	if fileErr.ID != "/workspace/bad_test.jsonnet#file" || fileErr.Status != "error" || fileErr.Name != "" {
		t.Fatalf("bad file-level error record: %+v", fileErr)
	}
	if fileErr.LintFilePath != "/workspace/bad.jsonnet" || fileErr.TestFilePath != "/workspace/bad_test.jsonnet" {
		t.Fatalf("file-level error must retain file paths: %+v", fileErr)
	}
	if !strings.Contains(fileErr.Error, "read a test file") {
		t.Fatalf("file-level error message must be retained, got %q", fileErr.Error)
	}

	if report.Tests[1].ID != "/workspace/good_test.jsonnet#0" || report.Tests[1].Status != "pass" {
		t.Fatalf("the other test pair must still be executed: %+v", report.Tests[1])
	}
}

func TestController_Test_JSON_deterministicOrder(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"/workspace/sub/alpha.jsonnet":      testLintEmpty,
		"/workspace/sub/alpha_test.jsonnet": testLintTestPass,
		"/workspace/sub/zeta.jsonnet":       testLintEmpty,
		"/workspace/sub/zeta_test.jsonnet":  testLintTestPass,
	}
	for range 20 {
		stdout := &bytes.Buffer{}
		ctrl := newTestController(t, "0.3.0", files, stdout)
		if err := ctrl.Test(t.Context(), slog.New(slog.DiscardHandler), &testcmd.ParamTest{
			FilePaths: []string{"/workspace/sub"},
			JSON:      true,
		}); err != nil {
			t.Fatal(err)
		}
		report := decodeTestReport(t, stdout)
		if len(report.Tests) != 2 {
			t.Fatalf("2 test cases must be reported, got %d", len(report.Tests))
		}
		if report.Tests[0].TestFilePath != "/workspace/sub/alpha_test.jsonnet" ||
			report.Tests[1].TestFilePath != "/workspace/sub/zeta_test.jsonnet" {
			t.Fatalf("test pairs must be sorted deterministically: %+v", report.Tests)
		}
	}
}

type failWriter struct {
	written bytes.Buffer
}

func (w *failWriter) Write(p []byte) (int, error) {
	_, _ = w.written.Write(p)
	return 0, errors.New("write failure")
}

func TestController_Test_JSON_writerError(t *testing.T) {
	t.Parallel()
	w := &failWriter{}
	ctrl := newTestController(t, "0.3.0", map[string]string{
		"/workspace/rule.jsonnet":      testLintEmpty,
		"/workspace/rule_test.jsonnet": testLintTestPass,
	}, w)

	err := ctrl.Test(t.Context(), slog.New(slog.DiscardHandler), &testcmd.ParamTest{
		FilePaths: []string{"/workspace/rule_test.jsonnet"},
		JSON:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "encode the test result as JSON") {
		t.Fatalf("JSON output failure must be returned, got %v", err)
	}
	// The human-readable template must not be appended after an output failure.
	if strings.Contains(w.written.String(), "Lint file:") {
		t.Fatal("human-readable template must not be rendered in JSON mode")
	}
}

func TestController_Test_humanOutput(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	ctrl := newTestController(t, "0.3.0", map[string]string{
		"/workspace/rule.jsonnet":      testLintRule,
		"/workspace/rule_test.jsonnet": testLintTestMixed,
	}, stdout)

	err := ctrl.Test(t.Context(), slog.New(slog.DiscardHandler), &testcmd.ParamTest{
		FilePaths: []string{"/workspace/rule_test.jsonnet"},
	})
	if err == nil || err.Error() != "test failed" {
		t.Fatalf("test failed must be returned, got %v", err)
	}
	out := stdout.String()
	for _, want := range []string{
		"Test Name: failing",
		"Lint file: /workspace/rule.jsonnet",
		"Test file: /workspace/rule_test.jsonnet",
		"Error: read a data file",
		"Diff (- Expected + Actual)",
		"rule1",
		"==========",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output must contain %q:\n%s", want, out)
		}
	}
}

func TestController_Test_humanSuccessSilent(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	ctrl := newTestController(t, "0.3.0", map[string]string{
		"/workspace/rule.jsonnet":      testLintEmpty,
		"/workspace/rule_test.jsonnet": testLintTestPass,
	}, stdout)

	if err := ctrl.Test(t.Context(), slog.New(slog.DiscardHandler), &testcmd.ParamTest{
		FilePaths: []string{"/workspace/rule_test.jsonnet"},
	}); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("human output must be silent on success, got %q", stdout.String())
	}
}

func decodeTestReport(t *testing.T, r io.Reader) *testcmd.TestReport {
	t.Helper()
	report := &testcmd.TestReport{}
	if err := json.NewDecoder(r).Decode(report); err != nil {
		t.Fatalf("decode the JSON report: %v", err)
	}
	return report
}

func assertSummaryConsistency(t *testing.T, report *testcmd.TestReport) {
	t.Helper()
	var passed, failed, errored int
	for _, test := range report.Tests {
		switch test.Status {
		case "pass":
			passed++
		case "fail":
			failed++
		case "error":
			errored++
		default:
			t.Fatalf("status must be pass, fail or error, got %q", test.Status)
		}
	}
	if report.Summary.Total != len(report.Tests) ||
		report.Summary.Passed != passed ||
		report.Summary.Failed != failed ||
		report.Summary.Errors != errored {
		t.Fatalf("summary %+v is inconsistent with %d records (pass=%d fail=%d error=%d)",
			report.Summary, len(report.Tests), passed, failed, errored)
	}
}
