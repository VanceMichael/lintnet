package osfile

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/otiai10/copy"
	"github.com/spf13/afero"
)

func Copy(fs afero.Fs, src, dst string) error {
	if _, ok := fs.(*afero.OsFs); ok {
		return copy.Copy(src, dst) //nolint:wrapcheck
	}
	return cpDir(fs, src, dst)
}

// CopyContext copies src to dst like Copy, but checks ctx between entries
// and aborts ongoing file copies as soon as ctx is canceled.
// The caller is responsible for removing a partially copied dst on cancellation.
func CopyContext(ctx context.Context, fs afero.Fs, src, dst string) error {
	return cpDirContext(ctx, fs, src, dst)
}

func cpDir(fs afero.Fs, src, dst string) error {
	fileInfos, err := afero.ReadDir(fs, src)
	if err != nil {
		return fmt.Errorf("read a directory: %w", err)
	}

	for _, fileInfo := range fileInfos {
		srcPath := filepath.Join(src, fileInfo.Name())
		dstPath := filepath.Join(dst, fileInfo.Name())

		if fileInfo.IsDir() {
			if err := MkdirAll(fs, dstPath); err != nil {
				return fmt.Errorf("create a directory: %w", err)
			}
			if err := cpDir(fs, srcPath, dstPath); err != nil {
				return fmt.Errorf("copy a directory: %w", err)
			}
		} else {
			if err := cpFile(fs, srcPath, dstPath); err != nil {
				return fmt.Errorf("copy a file: %w", err)
			}
		}
	}

	return nil
}

func cpFile(fs afero.Fs, src, dst string) error {
	srcFile, err := fs.Open(src)
	if err != nil {
		return fmt.Errorf("open a file: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := fs.Create(dst)
	if err != nil {
		return fmt.Errorf("create a file: %w", err)
	}
	defer dstFile.Close()

	if _, err = io.Copy(dstFile, srcFile); err != nil {
		return fmt.Errorf("copy a file: %w", err)
	}

	return nil
}

func cpDirContext(ctx context.Context, fs afero.Fs, src, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := fs.Stat(src)
	if err != nil {
		return fmt.Errorf("stat a directory: %w", err)
	}
	if err := fs.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return fmt.Errorf("create a directory: %w", err)
	}
	fileInfos, err := afero.ReadDir(fs, src)
	if err != nil {
		return fmt.Errorf("read a directory: %w", err)
	}

	for _, fileInfo := range fileInfos {
		if err := ctx.Err(); err != nil {
			return err
		}
		srcPath := filepath.Join(src, fileInfo.Name())
		dstPath := filepath.Join(dst, fileInfo.Name())

		if fileInfo.IsDir() {
			if err := fs.MkdirAll(dstPath, fileInfo.Mode().Perm()); err != nil {
				return fmt.Errorf("create a directory: %w", err)
			}
			if err := cpDirContext(ctx, fs, srcPath, dstPath); err != nil {
				return fmt.Errorf("copy a directory: %w", err)
			}
		} else {
			if err := cpFileContext(ctx, fs, srcPath, dstPath, fileInfo.Mode()); err != nil {
				return fmt.Errorf("copy a file: %w", err)
			}
		}
	}

	return nil
}

// cancelableReader returns ctx.Err() from Read as soon as ctx is canceled,
// even while an underlying read is in progress. The underlying read runs in
// its own goroutine and releases when the I/O completes or is closed.
type cancelableReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *cancelableReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	type readResult struct {
		n   int
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		n, err := c.r.Read(p)
		ch <- readResult{n: n, err: err}
	}()
	select {
	case <-c.ctx.Done():
		return 0, c.ctx.Err()
	case res := <-ch:
		return res.n, res.err
	}
}

func cpFileContext(ctx context.Context, fs afero.Fs, src, dst string, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	srcFile, err := fs.Open(src)
	if err != nil {
		return fmt.Errorf("open a file: %w", err)
	}
	defer srcFile.Close()

	dstFile, err := fs.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return fmt.Errorf("create a file: %w", err)
	}

	copyErr := make(chan error, 1)
	go func() {
		_, e := io.Copy(dstFile, &cancelableReader{ctx: ctx, r: srcFile})
		// The channel is buffered, so the goroutine doesn't leak even if the
		// copy was abandoned after cancellation and the underlying read
		// finishes a little later.
		copyErr <- e
	}()
	select {
	case <-ctx.Done():
		// Close the destination file exactly once; the abandoned copy writes
		// to the closed file and finishes by itself.
		_ = dstFile.Close()
		return ctx.Err()
	case err := <-copyErr:
		if closeErr := dstFile.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if err != nil {
			return fmt.Errorf("copy a file: %w", err)
		}
	}
	return nil
}
