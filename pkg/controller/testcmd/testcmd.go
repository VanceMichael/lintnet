package testcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/lintnet/lintnet/pkg/config"
	"github.com/lintnet/lintnet/pkg/domain"
	"github.com/lintnet/lintnet/pkg/filefilter"
	"github.com/lintnet/lintnet/pkg/jsonnet"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

type ParamTest struct {
	RootDir        string
	ConfigFilePath string
	TargetID       string
	PWD            string
	FilePaths      []string
}

func (p *ParamTest) FilterParam() *filefilter.Param {
	return &filefilter.Param{
		TargetID: p.TargetID,
		PWD:      p.PWD,
	}
}

func (c *Controller) Test(ctx context.Context, logger *slog.Logger, param *ParamTest) error {
	pairs, err := c.listPairs(ctx, logger, param)
	if err != nil {
		return err
	}

	testResultTemplate, err := template.New("_").Parse(string(testResultTemplateByte))
	if err != nil {
		return fmt.Errorf("parse the template of test result: %w", err)
	}

	failedResults := make([]*FailedResult, 0, len(pairs))
	for _, pair := range pairs {
		if err := ctx.Err(); err != nil {
			// Don't start testing a new pair after cancellation.
			return err
		}
		results, err := c.tests(ctx, pair)
		if err != nil {
			return err
		}
		failedResults = append(failedResults, results...)
	}
	if err := ctx.Err(); err != nil {
		// Don't render or write a partial test report.
		return err
	}
	if len(failedResults) == 0 {
		return nil
	}
	// Render the report into a buffer and write it in a single write call,
	// so a canceled process never leaves a half-written report behind.
	var buf bytes.Buffer
	if err := testResultTemplate.Execute(&buf, failedResults); err != nil {
		return fmt.Errorf("render the result: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := c.stdout.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write the result: %w", err)
	}
	return errors.New("test failed")
}

func (c *Controller) listPairs(ctx context.Context, logger *slog.Logger, param *ParamTest) ([]*TestPair, error) {
	if len(param.FilePaths) != 0 {
		return c.listPairsWithFilePaths(ctx, param.FilePaths)
	}

	rawCfg := &config.RawConfig{}
	if err := c.configReader.Read(ctx, param.ConfigFilePath, rawCfg); err != nil {
		if param.ConfigFilePath == "" && errors.Is(err, fs.ErrNotExist) {
			return c.listPairsWithFilePaths(ctx, []string{"."})
		}
		return nil, fmt.Errorf("read a configuration file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if param.TargetID != "" {
		target, err := rawCfg.GetTarget(param.TargetID)
		if err != nil {
			return nil, fmt.Errorf("get a target from configuration file by target id: %w", err)
		}
		rawCfg.Targets = []*config.RawTarget{target}
	}

	cfg, err := rawCfg.Parse()
	if err != nil {
		return nil, fmt.Errorf("parse a configuration file: %w", err)
	}

	cfgDir := filepath.Dir(rawCfg.FilePath)

	lintFiles, err := c.fileFinder.FindLintFiles(ctx, logger, cfg, cfgDir)
	if err != nil {
		return nil, fmt.Errorf("find files: %w", err)
	}

	return c.filterLintFilesWithTest(logger, lintFiles), nil
}

func getTestFilePath(lintFilePath string) string {
	return lintFilePath[:len(lintFilePath)-len(".jsonnet")] + "_test.jsonnet"
}

func getLintFilePath(testFilePath string) string {
	return testFilePath[:len(testFilePath)-len("_test.jsonnet")] + ".jsonnet"
}

func (c *Controller) listPairsWithFilePath(ctx context.Context, filePath string) ([]*TestPair, error) { //nolint:cyclop
	switch {
	case strings.HasSuffix(filePath, "_test.jsonnet"):
		lintFile := getLintFilePath(filePath)
		return []*TestPair{
			{
				LintFilePath: lintFile,
				TestFilePath: filePath,
			},
		}, nil
	case strings.HasSuffix(filePath, ".jsonnet"):
		tp := getTestFilePath(filePath)
		if f, err := afero.Exists(c.fs, tp); err != nil {
			return nil, fmt.Errorf("check if a file exists: %w", err)
		} else if !f {
			return nil, nil
		}
		return []*TestPair{
			{
				LintFilePath: filePath,
				TestFilePath: tp,
			},
		}, nil
	default:
		if b, err := afero.IsDir(c.fs, filePath); err != nil {
			return nil, fmt.Errorf("check if a path is a directory: %w", err)
		} else if !b {
			return nil, nil
		}
		pairs := []*TestPair{}
		if err := doublestar.GlobWalk(afero.NewIOFS(c.fs), filePath+"/**/*_test.jsonnet", func(testFile string, _ fs.DirEntry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			lintFile := getLintFilePath(testFile)
			a, err := afero.Exists(c.fs, lintFile)
			if err != nil {
				return fmt.Errorf("check if a lint file exists: %w", err)
			}
			if !a {
				return nil
			}
			pairs = append(pairs, &TestPair{
				LintFilePath: lintFile,
				TestFilePath: testFile,
			})
			return nil
		}, doublestar.WithNoFollow()); err != nil {
			return nil, fmt.Errorf("search files: %w", err)
		}
		return pairs, nil
	}
}

func (c *Controller) listPairsWithFilePaths(ctx context.Context, filePaths []string) ([]*TestPair, error) {
	pairs := make([]*TestPair, 0, len(filePaths))
	for _, p := range filePaths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ps, err := c.listPairsWithFilePath(ctx, p)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, ps...)
	}
	return pairs, nil
}

func (c *Controller) test(ctx context.Context, pair *TestPair, td *TestData) (*FailedResult, error) { //nolint:cyclop
	if td.DataFile != "" {
		if err := c.readDatafile(ctx, pair, td); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return &FailedResult{
				Error: err.Error(),
			}, nil
		}
	}

	if len(td.DataFiles) != 0 {
		if err := c.readDatafiles(ctx, pair, td); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return &FailedResult{
				Error: err.Error(),
			}, nil
		}
	}

	if td.Param.Config == nil {
		td.Param.Config = map[string]any{}
	}

	tlaB, err := json.Marshal(td.Param)
	if err != nil {
		return &FailedResult{
			Error: fmt.Errorf("marshal param as JSON: %w", err).Error(),
		}, nil
	}
	var results []*TestResult
	if err := jsonnet.Read(ctx, c.fs, pair.LintFilePath, string(tlaB), c.importer, &results); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// Cancellation must not be disguised as a test assertion failure.
			return nil, ctxErr
		}
		return &FailedResult{
			Error: fmt.Errorf("read a lint file: %w", err).Error(),
		}, nil
	}
	rs := make([]any, 0, len(results))
	for _, result := range results {
		if result.Excluded {
			continue
		}
		rs = append(rs, result.Any())
	}
	if len(rs) == 0 && len(td.Result) == 0 {
		return nil, nil
	}
	if diff := cmp.Diff(td.Result, rs); diff != "" {
		return &FailedResult{
			Wanted: td.Result,
			Got:    rs,
			Diff:   diff,
		}, nil
	}
	return nil, nil
}

func (c *Controller) readDatafile(ctx context.Context, pair *TestPair, td *TestData) error {
	p := &domain.Path{
		Raw: td.DataFile,
		Abs: filepath.Join(filepath.Dir(pair.TestFilePath), td.DataFile),
	}
	data, err := c.dataFileParser.Parse(ctx, p)
	if err != nil {
		return fmt.Errorf("read a data file: %w", err)
	}
	if td.Param != nil && td.Param.Data != nil && td.Param.Data.FilePath != "" {
		data.Data.FilePath = td.Param.Data.FilePath
	}
	if td.FakeDataFile != "" {
		data.Data.FilePath = td.FakeDataFile
	}
	if td.Param != nil {
		data.Config = td.Param.Config
	}
	td.Param = data
	return nil
}

func (c *Controller) readDatafiles(ctx context.Context, pair *TestPair, td *TestData) error {
	combinedData := make([]*domain.Data, len(td.DataFiles))
	for i, dataFile := range td.DataFiles {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := &domain.Path{
			Raw: dataFile.Path,
			Abs: filepath.Join(filepath.Dir(pair.TestFilePath), dataFile.Path),
		}
		data, err := c.dataFileParser.Parse(ctx, p)
		if err != nil {
			return fmt.Errorf("read a data file: %w", slogerr.With(err, "data_file", dataFile.Path))
		}
		if dataFile.FakePath != "" {
			data.Data.FilePath = dataFile.FakePath
		}
		if td.Param != nil {
			data.Config = td.Param.Config
		}
		combinedData[i] = data.Data
	}
	if td.Param == nil {
		td.Param = &domain.TopLevelArgument{}
	}
	td.Param.CombinedData = combinedData
	return nil
}

func (c *Controller) tests(ctx context.Context, pair *TestPair) ([]*FailedResult, error) {
	testData := []*TestData{}
	if err := jsonnet.Read(ctx, c.fs, pair.TestFilePath, "{}", c.importer, &testData); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return []*FailedResult{
			{
				LintFilePath: pair.LintFilePath,
				TestFilePath: pair.TestFilePath,
				Error:        fmt.Errorf("read a test file: %w", err).Error(),
			},
		}, nil
	}
	results := make([]*FailedResult, 0, len(testData))
	for _, td := range testData {
		if err := ctx.Err(); err != nil {
			// Don't start a new test case after cancellation.
			return nil, err
		}
		result, err := c.test(ctx, pair, td)
		if err != nil {
			return nil, err
		}
		if result != nil {
			result.Name = td.Name
			result.LintFilePath = pair.LintFilePath
			result.TestFilePath = pair.TestFilePath
			result.Param = td.Param
			results = append(results, result)
		}
	}
	return results, nil
}

func (c *Controller) filterLintFilesWithTest(logger *slog.Logger, lintFiles []*config.LintFile) []*TestPair {
	pairs := []*TestPair{}
	for _, lintFile := range lintFiles {
		if lintFile.Path == "" {
			continue
		}
		baseName := filepath.Base(lintFile.Path)
		ext := filepath.Ext(baseName)
		testFileName := fmt.Sprintf("%s_test%s", strings.TrimSuffix(baseName, filepath.Ext(baseName)), ext)
		testFilePath := filepath.Join(filepath.Dir(lintFile.Path), testFileName)
		f, err := afero.Exists(c.fs, testFilePath)
		if err != nil {
			slogerr.WithError(logger, err).Warn("check if a test file exists")
			continue
		}
		if !f {
			continue
		}
		pairs = append(pairs, &TestPair{
			LintFilePath: lintFile.Path,
			TestFilePath: testFilePath,
		})
	}
	return pairs
}
