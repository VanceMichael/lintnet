package lint_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/go-jsonnet"
	"github.com/lintnet/lintnet/pkg/controller/lint"
	"github.com/spf13/afero"
)

func TestController_Lint_canceled(t *testing.T) {
	t.Parallel()
	stdout := &bytes.Buffer{}
	importer := &jsonnet.MemoryImporter{}
	ctrl := lint.NewController(&lint.ParamController{
		Version: "v0.3.0",
		Env:     "darwin/arm64",
	}, afero.NewMemMapFs(), stdout, &lint.MockModuleInstaller{}, importer)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := ctrl.Lint(ctx, slog.New(slog.DiscardHandler), &lint.ParamLint{
		RootDir: "/home/foo/.local/share/lintnet",
		PWD:     "/home/foo/workspace",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Lint() error = %v, want context.Canceled", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("a canceled run must not write output, got %q", stdout.String())
	}
}
