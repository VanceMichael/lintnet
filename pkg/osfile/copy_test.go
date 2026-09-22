package osfile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/afero"
)

func TestCopyContext_canceled(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, dirPermission); err != nil {
		t.Fatal(err)
	}
	// A FIFO without a writer makes the ongoing file copy block on read,
	// so cancellation has to interrupt a copy that is in progress.
	fifo := filepath.Join(src, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("Mkfifo isn't available: %v", err)
	}
	writerOpened := make(chan struct{})
	writerDone := make(chan struct{})
	t.Cleanup(func() {
		close(writerDone)
	})
	go func() {
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		defer w.Close()
		close(writerOpened)
		// Keep the FIFO open without writing until the test ends.
		<-writerDone
	}()

	dst := filepath.Join(root, "dst")
	if err := os.MkdirAll(dst, dirPermission); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- CopyContext(ctx, afero.NewOsFs(), src, dst)
	}()
	select {
	case <-writerOpened:
	case <-time.After(5 * time.Second):
		t.Fatal("the copy didn't start reading the FIFO")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CopyContext() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CopyContext didn't stop after cancellation")
	}

	// The partially copied destination is removed by the caller, and a rerun
	// after cleanup succeeds.
	if err := os.RemoveAll(dst); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(fifo); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "regular.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CopyContext(context.Background(), afero.NewOsFs(), src, dst); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dst, "regular.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("copied content = %q, want hello", string(b))
	}
}
