package testcmd_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/go-jsonnet"
	"github.com/lintnet/lintnet/pkg/controller/testcmd"
	"github.com/lintnet/lintnet/pkg/testutil"
)

func TestController_Test_canceled(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"lintnet.jsonnet":                    "testdata/lintnet.jsonnet",
		"hello.jsonnet":                     "testdata/hello.jsonnet",
		"hello_test.jsonnet":                "testdata/hello_test.jsonnet",
		"testdata/pass.json":                "testdata/pass.json",
		"testdata/fail.json":                "testdata/fail.json",
	}
	fs, err := testutil.ReadFs(files)
	if err != nil {
		t.Fatal(err)
	}
	stdout := &bytes.Buffer{}
	importer := &jsonnet.MemoryImporter{}
	ctrl := testcmd.NewController(&testcmd.ParamController{
		Version: "0.3.0",
	}, fs, stdout, importer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = ctrl.Test(ctx, slog.New(slog.DiscardHandler), &testcmd.ParamTest{
		RootDir: "/home/foo/.local/share/lintnet",
		PWD:     "/home/foo/workspace",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Test() error = %v, want context.Canceled", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("a canceled run must not write a test report, got %q", stdout.String())
	}
}
