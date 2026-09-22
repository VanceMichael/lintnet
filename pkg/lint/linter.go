package lint

import (
	"context"
	"fmt"

	"github.com/lintnet/lintnet/pkg/config"
	"github.com/lintnet/lintnet/pkg/domain"
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
	Parse(ctx context.Context, filePath *domain.Path) (*domain.TopLevelArgument, error)
}

type LintFileParser interface { //nolint:revive
	Parse(ctx context.Context, lintFile *config.LintFile) (*domain.Node, error)
	Parses(ctx context.Context, lintFiles []*config.LintFile) ([]*domain.Node, error)
}

type LintFileEvaluator interface { //nolint:revive
	Evaluate(ctx context.Context, tla *domain.TopLevelArgument, lintFile jsonnet.Node) (string, error)
	Evaluates(ctx context.Context, tla *domain.TopLevelArgument, lintFiles []*domain.Node) ([]*domain.Result, error)
}

func (l *Linter) Lint(ctx context.Context, targets []*filefind.Target) ([]*domain.Result, error) {
	results := make([]*domain.Result, 0, len(targets))
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			// Don't start linting a new target after cancellation.
			return nil, err
		}
		rs, err := l.lintTarget(ctx, target)
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

func (l *Linter) lintTarget(ctx context.Context, target *filefind.Target) ([]*domain.Result, error) {
	lintFiles, err := l.lintFileParser.Parses(ctx, target.LintFiles)
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

	results, err := l.lintNonCombineFiles(ctx, target, nonCombineFiles)
	if err != nil {
		return nil, err
	}

	if len(combineFiles) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rs, err := l.lintCombineFiles(ctx, target, combineFiles)
		if err != nil {
			return nil, err
		}
		return append(results, rs...), nil
	}
	return results, nil
}

func (l *Linter) lintCombineFiles(ctx context.Context, target *filefind.Target, combineFiles []*domain.Node) ([]*domain.Result, error) {
	rs, err := l.lint(ctx, &domain.DataSet{
		Files: target.DataFiles,
	}, combineFiles)
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		arr := make([]string, len(target.DataFiles))
		for i, dataFile := range target.DataFiles {
			arr[i] = dataFile.Raw
		}
		r.DataFiles = arr
	}
	return rs, nil
}

func (l *Linter) lintNonCombineFiles(ctx context.Context, target *filefind.Target, nonCombineFiles []*domain.Node) ([]*domain.Result, error) {
	results := make([]*domain.Result, 0, len(target.DataFiles))
	for _, dataFile := range target.DataFiles {
		if err := ctx.Err(); err != nil {
			// Don't start reading or linting a new data file after cancellation.
			return nil, err
		}
		rs, err := l.lintNonCombineFile(ctx, nonCombineFiles, dataFile)
		if err != nil {
			return nil, err
		}
		results = append(results, rs...)
	}
	return results, nil
}

func (l *Linter) lintNonCombineFile(ctx context.Context, nonCombineFiles []*domain.Node, dataFile *domain.Path) ([]*domain.Result, error) {
	rs, err := l.lint(ctx, &domain.DataSet{
		File: dataFile,
	}, nonCombineFiles)
	if err != nil {
		if ctxErr := ctx.Err(); err != nil {
			// Cancellation must not be disguised as a lint finding.
			return nil, ctxErr
		}
		return []*domain.Result{
			{
				DataFile: dataFile.Raw,
				Error:    err.Error(),
			},
		}, nil
	}
	for _, r := range rs {
		r.DataFile = dataFile.Raw
	}
	return rs, nil
}

func (l *Linter) getTLA(ctx context.Context, dataSet *domain.DataSet) (*domain.TopLevelArgument, error) {
	if dataSet.File != nil {
		tla, err := l.dataFileParser.Parse(ctx, dataSet.File)
		if err != nil {
			return nil, fmt.Errorf("parse a data file: %w", err)
		}
		return tla, nil
	}
	if len(dataSet.Files) > 0 {
		combinedData := make([]*domain.Data, len(dataSet.Files))
		for i, dataFile := range dataSet.Files {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			data, err := l.dataFileParser.Parse(ctx, dataFile)
			if err != nil {
				return nil, fmt.Errorf("parse a data file: %w", slogerr.With(err, "data_file", dataFile.Raw))
			}
			combinedData[i] = data.Data
		}
		return &domain.TopLevelArgument{
			CombinedData: combinedData,
		}, nil
	}
	return &domain.TopLevelArgument{}, nil
}

func (l *Linter) lint(ctx context.Context, dataSet *domain.DataSet, lintFiles []*domain.Node) ([]*domain.Result, error) {
	tla, err := l.getTLA(ctx, dataSet)
	if err != nil {
		return nil, err
	}
	return l.lintFileEvaluator.Evaluates(ctx, tla, lintFiles)
}
