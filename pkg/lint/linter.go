package lint

import (
	"errors"
	"fmt"

	"github.com/lintnet/lintnet/pkg/config"
	"github.com/lintnet/lintnet/pkg/domain"
	"github.com/lintnet/lintnet/pkg/encoding"
	"github.com/lintnet/lintnet/pkg/filefind"
	"github.com/lintnet/lintnet/pkg/jsonnet"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

type Linter struct {
	dataFileParser    DataFileParser
	lintFileParser    LintFileParser
	lintFileEvaluator LintFileEvaluator
}

func NewLinter(dataFileParser DataFileParser, lintFileParser LintFileParser, lintFileEvaluator LintFileEvaluator) *Linter {
	return &Linter{
		dataFileParser:    dataFileParser,
		lintFileParser:    lintFileParser,
		lintFileEvaluator: lintFileEvaluator,
	}
}

type DataFileParser interface {
	Parse(filePath *domain.Path, maxDataBytes int64) (*domain.TopLevelArgument, error)
}

type LintFileParser interface { //nolint:revive
	Parse(lintFile *config.LintFile) (*domain.Node, error)
	Parses(lintFiles []*config.LintFile) ([]*domain.Node, error)
}

type LintFileEvaluator interface { //nolint:revive
	Evaluate(tla *domain.TopLevelArgument, lintFile jsonnet.Node) (string, error)
	Evaluates(tla *domain.TopLevelArgument, lintFiles []*domain.Node) []*domain.Result
}

func (l *Linter) Lint(targets []*filefind.Target, maxDataBytes int64) ([]*domain.Result, error) {
	results := make([]*domain.Result, 0, len(targets))
	for _, target := range targets {
		rs, err := l.lintTarget(target, maxDataBytes)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			r.TargetID = target.ID
		}
		results = append(results, rs...)
	}
	return results, nil
}

func (l *Linter) lintTarget(target *filefind.Target, maxDataBytes int64) ([]*domain.Result, error) {
	lintFiles, err := l.lintFileParser.Parses(target.LintFiles)
	if err != nil {
		return nil, fmt.Errorf("parse lint files: %w", err)
	}

	combineFiles := []*domain.Node{}
	nonCombineFiles := []*domain.Node{}
	for _, lintFile := range lintFiles {
		if lintFile.Combine {
			combineFiles = append(combineFiles, lintFile)
			continue
		}
		nonCombineFiles = append(nonCombineFiles, lintFile)
	}

	// Results of non-combine rules are kept even if a combined data set exceeds the limit.
	results := l.lintNonCombineFiles(target, nonCombineFiles, maxDataBytes)

	if len(combineFiles) > 0 {
		rs, err := l.lintCombineFiles(target, combineFiles, maxDataBytes)
		if err != nil {
			return nil, err
		}
		results = append(results, rs...)
	}
	return results, nil
}

func (l *Linter) lintCombineFiles(target *filefind.Target, combineFiles []*domain.Node, maxDataBytes int64) ([]*domain.Result, error) {
	tla, err := l.getTLA(&domain.DataSet{
		Files: target.DataFiles,
	}, maxDataBytes)
	if err != nil {
		if errors.Is(err, encoding.ErrDataTooLarge) {
			// A combined rule must not run with a truncated or incomplete data set.
			// Report all files participating in the data set.
			return []*domain.Result{
				{
					DataFiles: target.DataFiles.Raw(),
					Error:     err.Error(),
				},
			}, nil
		}
		return nil, err
	}
	rs := l.lintFileEvaluator.Evaluates(tla, combineFiles)
	for _, r := range rs {
		arr := make([]string, len(target.DataFiles))
		for i, dataFile := range target.DataFiles {
			arr[i] = dataFile.Raw
		}
		r.DataFiles = arr
	}
	return rs, nil
}

func (l *Linter) lintNonCombineFiles(target *filefind.Target, nonCombineFiles []*domain.Node, maxDataBytes int64) []*domain.Result {
	results := make([]*domain.Result, 0, len(target.DataFiles))
	for _, dataFile := range target.DataFiles {
		results = append(results, l.lintNonCombineFile(nonCombineFiles, dataFile, maxDataBytes)...)
	}
	return results
}

func (l *Linter) lintNonCombineFile(nonCombineFiles []*domain.Node, dataFile *domain.Path, maxDataBytes int64) []*domain.Result {
	rs, err := l.lint(&domain.DataSet{
		File: dataFile,
	}, nonCombineFiles, maxDataBytes)
	if err != nil {
		return []*domain.Result{
			{
				DataFile: dataFile.Raw,
				Error:    err.Error(),
			},
		}
	}
	for _, r := range rs {
		r.DataFile = dataFile.Raw
	}
	return rs
}

func (l *Linter) getTLA(dataSet *domain.DataSet, maxDataBytes int64) (*domain.TopLevelArgument, error) {
	if dataSet.File != nil {
		tla, err := l.dataFileParser.Parse(dataSet.File, maxDataBytes)
		if err != nil {
			return nil, fmt.Errorf("parse a data file: %w", err)
		}
		return tla, nil
	}
	if len(dataSet.Files) > 0 {
		return l.parseCombinedDataFiles(dataSet.Files, maxDataBytes)
	}
	return &domain.TopLevelArgument{}, nil
}

// parseCombinedDataFiles parses data files for a combined rule.
// A single budget bounds both the size of each physical file and the cumulative
// size of all files: each file is read with the remaining budget, so neither
// per-file reads nor the total can exceed maxDataBytes.
func (l *Linter) parseCombinedDataFiles(files domain.Paths, maxDataBytes int64) (*domain.TopLevelArgument, error) {
	combinedData := make([]*domain.Data, len(files))
	used := int64(0)
	for i, dataFile := range files {
		limit := int64(0)
		if maxDataBytes > 0 {
			limit = maxDataBytes - used
			if limit <= 0 {
				return nil, encoding.CombinedDataTooLargeError(files.Raw(), maxDataBytes)
			}
		}
		tla, err := l.dataFileParser.Parse(dataFile, limit)
		if err != nil {
			if errors.Is(err, encoding.ErrDataTooLarge) {
				return nil, encoding.CombinedDataTooLargeError(files.Raw(), maxDataBytes)
			}
			return nil, fmt.Errorf("parse a data file: %w", slogerr.With(err, "data_file", dataFile.Raw))
		}
		combinedData[i] = tla.Data
		used += int64(len(tla.Data.Text))
	}
	return &domain.TopLevelArgument{
		CombinedData: combinedData,
	}, nil
}

func (l *Linter) lint(dataSet *domain.DataSet, lintFiles []*domain.Node, maxDataBytes int64) ([]*domain.Result, error) {
	tla, err := l.getTLA(dataSet, maxDataBytes)
	if err != nil {
		return nil, err
	}
	return l.lintFileEvaluator.Evaluates(tla, lintFiles), nil
}
