package lintfile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	gojsonnet "github.com/google/go-jsonnet"
	"github.com/lintnet/lintnet/pkg/domain"
	"github.com/lintnet/lintnet/pkg/jsonnet"
)

type Evaluator struct {
	importer gojsonnet.Importer
}

func NewEvaluator(importer gojsonnet.Importer) *Evaluator {
	return &Evaluator{
		importer: importer,
	}
}

func (le *Evaluator) Evaluate(ctx context.Context, tla *domain.TopLevelArgument, lintFile jsonnet.Node) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if tla.Config == nil {
		tla.Config = map[string]any{}
	}
	tlaB, err := json.Marshal(tla)
	if err != nil {
		return "", fmt.Errorf("marshal a top level argument as JSON: %w", err)
	}
	vm := jsonnet.NewVM(string(tlaB), le.importer)
	result, err := vm.Evaluate(lintFile)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// A module import triggered by the evaluation was canceled.
			return "", ctxErr
		}
		return "", fmt.Errorf("evaluate a lint file as Jsonnet: %w", err)
	}
	return result, nil
}

// Evaluates evaluates lint files.
// If ctx is canceled, it returns the cancellation error immediately instead of
// recording it as a per-file result, so cancellation is never rendered as a
// lint finding.
func (le *Evaluator) Evaluates(ctx context.Context, tla *domain.TopLevelArgument, lintFiles []*domain.Node) ([]*domain.Result, error) {
	results := make([]*domain.Result, len(lintFiles))
	for i, lintFile := range lintFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		tla := &domain.TopLevelArgument{
			Data:         tla.Data,
			CombinedData: tla.CombinedData,
			Config:       lintFile.Config,
		}
		s, err := le.Evaluate(ctx, tla, lintFile.Node)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				if ctxErr := ctx.Err(); ctxErr != nil {
					return nil, ctxErr
				}
				return nil, err
			}
			results[i] = &domain.Result{
				LintFile: lintFile.Key,
				Error:    err.Error(),
			}
			continue
		}
		rs, a, err := parseResult([]byte(s))

		if lintFile.Link != "" {
			// Append the module link to each result
			for _, r := range rs {
				appendLink(r, lintFile.Link)
			}
		}

		results[i] = &domain.Result{
			LintFile:  lintFile.Key,
			RawResult: rs,
			RawOutput: s,
			Interface: a,
		}
		if err != nil {
			results[i].Error = err.Error()
		}
	}
	return results, nil
}

func appendLink(r *domain.JsonnetResult, link string) {
	for _, l := range r.Links {
		if l.Link == link {
			return
		}
	}
	r.Links = append(r.Links, &domain.Link{
		Title: "Module source",
		Link:  link,
	})
}

func parseResult(result []byte) ([]*domain.JsonnetResult, any, error) {
	var rs any
	if err := json.Unmarshal(result, &rs); err != nil {
		return nil, nil, fmt.Errorf("unmarshal the result as JSON: %w", err)
	}

	out := []*domain.JsonnetResult{}
	if err := json.Unmarshal(result, &out); err != nil {
		return nil, rs, fmt.Errorf("unmarshal the result as JSON: %w", err)
	}
	return out, rs, nil
}
