package encoding

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/lintnet/lintnet/pkg/domain"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

// ErrDataTooLarge is returned when a data file exceeds max_data_bytes.
var ErrDataTooLarge = errors.New("the data file exceeds max_data_bytes")

// DataTooLargeError means a data file is larger than MaxDataBytes.
type DataTooLargeError struct {
	Path         string
	MaxDataBytes int64
}

func (e *DataTooLargeError) Error() string {
	return fmt.Sprintf("the data file %q is larger than max_data_bytes (%d bytes)", e.Path, e.MaxDataBytes)
}

func (e *DataTooLargeError) Unwrap() error {
	return ErrDataTooLarge
}

// CombinedDataTooLargeError builds an error which reports all files participating
// in a combined data set whose cumulative size exceeds max_data_bytes.
func CombinedDataTooLargeError(files []string, maxDataBytes int64) error {
	names := slices.Clone(files)
	slices.Sort(names)
	return fmt.Errorf("the total size of the data files [%s] exceeds max_data_bytes (%d bytes): %w",
		strings.Join(names, ", "), maxDataBytes, ErrDataTooLarge)
}

type DataFileParser struct {
	fs afero.Fs
}

func NewDataFileParser(fs afero.Fs) *DataFileParser {
	return &DataFileParser{
		fs: fs,
	}
}

// readFile reads a file.
// If maxDataBytes is greater than zero, it must not read more than maxDataBytes bytes.
// If the file is larger than maxDataBytes, it returns a DataTooLargeError without
// reading the excess bytes, so a file growing after it was found can't cross the limit.
func readFile(fs afero.Fs, p string, maxDataBytes int64) ([]byte, error) {
	if maxDataBytes <= 0 {
		b, err := afero.ReadFile(fs, p)
		if err != nil {
			return nil, fmt.Errorf("read a file: %w", err)
		}
		return b, nil
	}
	f, err := fs.Open(p)
	if err != nil {
		return nil, fmt.Errorf("read a file: %w", err)
	}
	defer f.Close()
	// Read at most maxDataBytes + 1 bytes so the overflow can be detected.
	b, err := io.ReadAll(io.LimitReader(f, maxDataBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read a file: %w", err)
	}
	if int64(len(b)) > maxDataBytes {
		return nil, &DataTooLargeError{
			Path:         p,
			MaxDataBytes: maxDataBytes,
		}
	}
	return b, nil
}

// Parse reads and decodes a data file.
// maxDataBytes limits the number of bytes read from the file.
// If maxDataBytes is zero, there is no limit.
func (dp *DataFileParser) Parse(filePath *domain.Path, maxDataBytes int64) (*domain.TopLevelArgument, error) {
	unmarshaler, fileType, err := NewUnmarshaler(filePath.Abs)
	if err != nil {
		return nil, slogerr.With(err, "file_path", filePath.Raw) //nolint:wrapcheck
	}
	b, err := readFile(dp.fs, filePath.Abs, maxDataBytes)
	if err != nil {
		var tooLarge *DataTooLargeError
		if errors.As(err, &tooLarge) {
			tooLarge.Path = filePath.Raw
			return nil, tooLarge
		}
		return nil, err
	}
	input, err := unmarshaler.Unmarshal(b)
	if err != nil {
		return nil, fmt.Errorf("decode a file: %w", err)
	}

	return &domain.TopLevelArgument{
		Data: &domain.Data{
			Text:     string(b),
			FilePath: filePath.Raw,
			FileType: fileType,
			Value:    input,
		},
	}, nil
}
