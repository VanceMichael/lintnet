package filefind_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/lintnet/lintnet/pkg/config"
	"github.com/lintnet/lintnet/pkg/filefind"
	"github.com/lintnet/lintnet/pkg/testutil"
)

func TestFinder_Find_canceled(t *testing.T) {
	t.Parallel()
	fs, err := testutil.NewFs(map[string]string{
		"foo.json":      `{}`,
		"hello.jsonnet": `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Targets: []*config.Target{
			{
				LintFiles: []*config.LintGlob{{Glob: "*.jsonnet"}},
				DataFiles: []*config.DataFile{{Path: "*.json"}},
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finder := filefind.NewFileFinder(fs)
	_, err = finder.Find(ctx, slog.New(slog.DiscardHandler), cfg, "/home/foo/.local/share/lintnet", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Find() error = %v, want context.Canceled", err)
	}
}

func TestFinder_FindLintFiles_canceled(t *testing.T) {
	t.Parallel()
	fs, err := testutil.NewFs(map[string]string{
		"hello.jsonnet": `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Targets: []*config.Target{
			{
				LintFiles: []*config.LintGlob{{Glob: "*.jsonnet"}},
			},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finder := filefind.NewFileFinder(fs)
	_, err = finder.FindLintFiles(ctx, slog.New(slog.DiscardHandler), cfg, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FindLintFiles() error = %v, want context.Canceled", err)
	}
}
