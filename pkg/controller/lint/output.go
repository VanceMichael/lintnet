package lint

import (
	"context"
	"errors"
	"log/slog"

	"github.com/lintnet/lintnet/pkg/domain"
	"github.com/lintnet/lintnet/pkg/errlevel"
	"github.com/lintnet/lintnet/pkg/output"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

type Outputter interface {
	Output(result *output.Output) error
}

func (c *Controller) Output(ctx context.Context, logger *slog.Logger, errLevel, shownErrLevel errlevel.Level, results []*domain.Result, outputters []Outputter, outputSuccess bool) error {
	if err := ctx.Err(); err != nil {
		// A canceled run must not write a partial success document.
		return err
	}
	fes := &output.Output{
		Errors:         output.FormatResults(logger, results, shownErrLevel),
		LintnetVersion: c.param.Version,
		Env:            c.param.Env,
	}
	failed, err := isFailed(fes.Errors, errLevel)
	if err != nil {
		return err
	}
	if !outputSuccess && len(fes.Errors) == 0 {
		return nil
	}
	for _, outputter := range outputters {
		if err := outputter.Output(fes); err != nil {
			slogerr.WithError(logger, err).Error("output errors")
		}
	}
	if failed {
		return errors.New("lint failed")
	}
	return nil
}
