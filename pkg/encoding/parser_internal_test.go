package encoding

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/lintnet/lintnet/pkg/domain"
	"github.com/spf13/afero"
)

func TestReadFile(t *testing.T) {
	t.Parallel()
	const content = "0123456789" // 10 bytes
	data := []struct {
		name         string
		maxDataBytes int64
		isErr        bool
	}{
		{name: "unlimited", maxDataBytes: 0},
		{name: "limit larger than file", maxDataBytes: 11},
		{name: "limit equal to file", maxDataBytes: 10},
		{name: "limit smaller than file", maxDataBytes: 9, isErr: true},
		{name: "limit one", maxDataBytes: 1, isErr: true},
	}
	for _, d := range data {
		t.Run(d.name, func(t *testing.T) {
			t.Parallel()
			mem := afero.NewMemMapFs()
			if err := afero.WriteFile(mem, "foo.txt", []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			b, err := readFile(mem, "foo.txt", d.maxDataBytes)
			if err != nil {
				if !d.isErr {
					t.Fatal(err)
				}
				var e *DataTooLargeError
				if !errors.As(err, &e) {
					t.Fatalf("error must be DataTooLargeError: %v", err)
				}
				if e.MaxDataBytes != d.maxDataBytes {
					t.Fatalf("got %d, wanted %d", e.MaxDataBytes, d.maxDataBytes)
				}
				return
			}
			if d.isErr {
				t.Fatal("error must be returned")
			}
			if string(b) != content {
				t.Fatalf("got %q, wanted %q", string(b), content)
			}
		})
	}
}

// countingFile counts the number of bytes actually read from the file.
type countingFile struct {
	afero.File
	read *int64
}

func (f *countingFile) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	*f.read += int64(n)
	return n, err
}

type countingFs struct {
	afero.Fs
	read int64
}

func (f *countingFs) Open(name string) (afero.File, error) {
	file, err := f.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &countingFile{
		File: file,
		read: &f.read,
	}, nil
}

// TestReadFile_DoesNotReadExcessBytes ensures an oversized file is not read into memory.
func TestReadFile_DoesNotReadExcessBytes(t *testing.T) {
	t.Parallel()
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "big.txt", []byte(strings.Repeat("x", 10000)), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := &countingFs{Fs: mem}
	_, err := readFile(fs, "big.txt", 100)
	if err == nil {
		t.Fatal("error must be returned")
	}
	if fs.read > 101 {
		t.Fatalf("at most 101 bytes must be read, but %d bytes were read", fs.read)
	}
}

type fakeStat struct {
	fs.FileInfo
	size int64
}

func (f *fakeStat) Size() int64 {
	return f.size
}

type statSpoofingFs struct {
	afero.Fs
}

func (f *statSpoofingFs) Stat(name string) (fs.FileInfo, error) {
	fi, err := f.Fs.Stat(name)
	if err != nil {
		return nil, err
	}
	return &fakeStat{
		FileInfo: fi,
		size:     1, // Pretend the file is small even if it grew after being discovered.
	}, nil
}

// TestReadFile_FileGrowingAfterDiscovered ensures the limit is enforced on actual reads,
// not on file size metadata, so a file growing after it was found can't cross the limit.
func TestReadFile_FileGrowingAfterDiscovered(t *testing.T) {
	t.Parallel()
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "big.txt", []byte(strings.Repeat("x", 10000)), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := &statSpoofingFs{Fs: mem}
	_, err := readFile(fs, "big.txt", 100)
	var e *DataTooLargeError
	if !errors.As(err, &e) {
		t.Fatalf("DataTooLargeError must be returned: %v", err)
	}
}

func TestCombinedDataTooLargeError(t *testing.T) {
	t.Parallel()
	err := CombinedDataTooLargeError([]string{"b.json", "a.json"}, 10)
	if !errors.Is(err, ErrDataTooLarge) {
		t.Fatal("the error must wrap ErrDataTooLarge")
	}
	exp := "the total size of the data files [a.json, b.json] exceeds max_data_bytes (10 bytes): the data file exceeds max_data_bytes"
	if err.Error() != exp {
		t.Fatalf("got %q, wanted %q", err.Error(), exp)
	}
}

func TestDataFileParser_Parse(t *testing.T) {
	t.Parallel()
	t.Run("small file", func(t *testing.T) {
		t.Parallel()
		mem := afero.NewMemMapFs()
		if err := afero.WriteFile(mem, "foo.json", []byte(`{"name":"foo"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		dp := NewDataFileParser(mem)
		tla, err := dp.Parse(&domain.Path{Raw: "foo.json", Abs: "foo.json"}, 100)
		if err != nil {
			t.Fatal(err)
		}
		if tla.Data.FileType != "json" {
			t.Fatalf("got %s, wanted json", tla.Data.FileType)
		}
		m, ok := tla.Data.Value.(map[string]any)
		if !ok || m["name"] != "foo" {
			t.Fatalf("unexpected value: %v", tla.Data.Value)
		}
	})
	t.Run("oversized file", func(t *testing.T) {
		t.Parallel()
		mem := afero.NewMemMapFs()
		if err := afero.WriteFile(mem, "foo.txt", []byte(strings.Repeat("x", 100)), 0o644); err != nil {
			t.Fatal(err)
		}
		dp := NewDataFileParser(mem)
		_, err := dp.Parse(&domain.Path{Raw: "foo.txt", Abs: "foo.txt"}, 10)
		var e *DataTooLargeError
		if !errors.As(err, &e) {
			t.Fatalf("DataTooLargeError must be returned: %v", err)
		}
		if e.Path != "foo.txt" {
			t.Fatalf("got %q, wanted foo.txt", e.Path)
		}
	})
	t.Run("unlimited", func(t *testing.T) {
		t.Parallel()
		mem := afero.NewMemMapFs()
		if err := afero.WriteFile(mem, "foo.txt", []byte(strings.Repeat("x", 100)), 0o644); err != nil {
			t.Fatal(err)
		}
		dp := NewDataFileParser(mem)
		if _, err := dp.Parse(&domain.Path{Raw: "foo.txt", Abs: "foo.txt"}, 0); err != nil {
			t.Fatal(err)
		}
	})
}
