package module

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-github/v92/github"
	"github.com/lintnet/lintnet/pkg/config"
	"github.com/spf13/afero"
)

type mockGitHub struct {
	u     *url.URL
	calls int
}

func (m *mockGitHub) GetArchiveLink(_ context.Context, _, _ string, _ github.ArchiveFormat, _ *github.RepositoryContentGetOptions, _ int) (*url.URL, *github.Response, error) {
	m.calls++
	return m.u, nil, nil
}

func makeTarGz(t *testing.T, rootDir string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{
		Name:     rootDir + "/",
		Typeflag: tar.TypeDir,
		Mode:     0o755,
	}); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     rootDir + "/" + name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstaller_Install_canceledDuringDownload(t *testing.T) {
	tmpRoot := t.TempDir()
	t.Setenv("TMPDIR", tmpRoot)

	mod := &config.ModuleArchive{
		Type:      "github_archive",
		Host:      "github.com",
		RepoOwner: "suzuki-shunsuke",
		RepoName:  "foo",
		Ref:       "1111111111111111111111111111111111111111",
	}
	baseDir := filepath.Join(t.TempDir(), "modules")
	dest := filepath.Join(baseDir, filepath.FromSlash(mod.FilePath()))

	// The server sends the response header and then blocks until the request
	// context is canceled, simulating a module download in progress.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	gh := &mockGitHub{u: u}
	installer := NewInstaller(afero.NewOsFs(), gh, srv.Client())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	err = installer.Install(ctx, slog.New(slog.DiscardHandler), &ParamInstall{BaseDir: baseDir}, mod)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Install() error = %v, want context.Canceled", err)
	}

	// The destination directory must not exist, otherwise DirExists would
	// mistake the canceled installation for an installed module.
	if f, err := afero.DirExists(afero.NewOsFs(), dest); err != nil {
		t.Fatal(err)
	} else if f {
		t.Fatalf("the destination directory %s must not exist after cancellation", dest)
	}

	// No staging directory must be left next to the destination.
	entries, err := os.ReadDir(filepath.Dir(dest))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if bytes.HasPrefix([]byte(entry.Name()), []byte(".")) {
			t.Fatalf("a staging directory is left after cancellation: %s", entry.Name())
		}
	}

	// No temporal directory must be left.
	if entries, err := os.ReadDir(tmpRoot); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("temporal resources are left after cancellation: %v", entries)
	}

	// A subsequent normal run installs the module from scratch and succeeds.
	archive := makeTarGz(t, "foo-1111111", map[string]string{
		"hello.jsonnet": "{}",
	})
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		if _, err := w.Write(archive); err != nil {
			t.Error(err)
		}
	}))
	defer srv2.Close()
	u2, err := url.Parse(srv2.URL)
	if err != nil {
		t.Fatal(err)
	}
	gh.u = u2
	if err := installer.Install(context.Background(), slog.New(slog.DiscardHandler), &ParamInstall{BaseDir: baseDir}, mod); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dest, "hello.jsonnet"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "{}" {
		t.Fatalf("installed file content = %q, want {}", string(b))
	}

	// A further run reuses the cached module and doesn't call the GitHub API.
	gh.calls = 0
	if err := installer.Install(context.Background(), slog.New(slog.DiscardHandler), &ParamInstall{BaseDir: baseDir}, mod); err != nil {
		t.Fatal(err)
	}
	if gh.calls != 0 {
		t.Fatalf("GetArchiveLink must not be called for a cached module, got %d calls", gh.calls)
	}
}

func TestInstaller_Install_alreadyCanceled(t *testing.T) {
	t.Parallel()
	baseDir := filepath.Join(t.TempDir(), "modules")
	mod := &config.ModuleArchive{
		Type:      "github_archive",
		Host:      "github.com",
		RepoOwner: "suzuki-shunsuke",
		RepoName:  "bar",
		Ref:       "2222222222222222222222222222222222222222",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("the HTTP server must not be called after cancellation")
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	installer := NewInstaller(afero.NewOsFs(), &mockGitHub{u: u}, srv.Client())
	err = installer.Install(ctx, slog.New(slog.DiscardHandler), &ParamInstall{BaseDir: baseDir}, mod)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Install() error = %v, want context.Canceled", err)
	}
	dest := filepath.Join(baseDir, filepath.FromSlash(mod.FilePath()))
	if f, _ := afero.DirExists(afero.NewOsFs(), dest); f {
		t.Fatal("the destination directory must not be created")
	}
}

func TestInstaller_Installs_stopsAtCanceledModule(t *testing.T) {
	t.Parallel()
	baseDir := filepath.Join(t.TempDir(), "modules")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("the HTTP server must not be called after cancellation")
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	installer := NewInstaller(afero.NewOsFs(), &mockGitHub{u: u}, srv.Client())
	modules := map[string]*config.ModuleArchive{
		"a": {
			Type:      "github_archive",
			Host:      "github.com",
			RepoOwner: "suzuki-shunsuke",
			RepoName:  "baz",
			Ref:       "3333333333333333333333333333333333333333",
		},
	}
	err = installer.Installs(ctx, slog.New(slog.DiscardHandler), &ParamInstall{BaseDir: baseDir}, modules)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Installs() error = %v, want context.Canceled", err)
	}
}

func TestInstaller_Install_httpError(t *testing.T) {
	t.Parallel()
	baseDir := filepath.Join(t.TempDir(), "modules")
	mod := &config.ModuleArchive{
		Type:      "github_archive",
		Host:      "github.com",
		RepoOwner: "suzuki-shunsuke",
		RepoName:  "qux",
		Ref:       "4444444444444444444444444444444444444444",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "internal server error")
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	installer := NewInstaller(afero.NewOsFs(), &mockGitHub{u: u}, srv.Client())
	err = installer.Install(context.Background(), slog.New(slog.DiscardHandler), &ParamInstall{BaseDir: baseDir}, mod)
	if err == nil || err.Error() != "HTTP status code >= 300" {
		t.Fatalf("Install() error = %v, want HTTP status code >= 300", err)
	}
}
