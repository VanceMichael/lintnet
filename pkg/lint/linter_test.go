package lint

import (
	"context"
	"errors"
	"testing"

	"github.com/lintnet/lintnet/pkg/config"
	"github.com/lintnet/lintnet/pkg/domain"
	"github.com/lintnet/lintnet/pkg/filefind"
	"github.com/lintnet/lintnet/pkg/jsonnet"
)

type stubDataFileParser struct {
	parse func(ctx context.Context, filePath *domain.Path) (*domain.TopLevelArgument, error)
}

func (s *stubDataFileParser) Parse(ctx context.Context, filePath *domain.Path) (*domain.TopLevelArgument, error) {
	return s.parse(ctx, filePath)
}

type stubLintFileParser struct {
	parses func(ctx context.Context, lintFiles []*config.LintFile) ([]*domain.Node, error)
}

func (s *stubLintFileParser) Parse(_ context.Context, _ *config.LintFile) (*domain.Node, error) {
	return nil, nil
}

func (s *stubLintFileParser) Parses(ctx context.Context, lintFiles []*config.LintFile) ([]*domain.Node, error) {
	return s.parses(ctx, lintFiles)
}

type stubEvaluator struct {
	evaluates func(ctx context.Context, tla *domain.TopLevelArgument, lintFiles []*domain.Node) ([]*domain.Result, error)
}

func (s *stubEvaluator) Evaluate(_ context.Context, _ *domain.TopLevelArgument, _ jsonnet.Node) (string, error) {
	return "", nil
}

func (s *stubEvaluator) Evaluates(ctx context.Context, tla *domain.TopLevelArgument, lintFiles []*domain.Node) ([]*domain.Result, error) {
	return s.evaluates(ctx, tla, lintFiles)
}

func newTargets(n int) []*filefind.Target {
	targets := make([]*filefind.Target, 0, n)
	for range n {
		targets = append(targets, &filefind.Target{
			LintFiles: []*config.LintFile{{ID: "rule.jsonnet", Path: "rule.jsonnet"}},
			DataFiles: domain.Paths{{Raw: "foo.json", Abs: "foo.json"}},
		})
	}
	return targets
}

func TestLinter_Lint_canceledBetweenTargets(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	parser := &stubLintFileParser{
		parses: func(_ context.Context, _ []*config.LintFile) ([]*domain.Node, error) {
			return []*domain.Node{{Key: "rule.jsonnet"}}, nil
		},
	}
	dataParser := &stubDataFileParser{
		parse: func(context.Context, *domain.Path) (*domain.TopLevelArgument, error) {
			return &domain.TopLevelArgument{}, nil
		},
	}
	evaluator := &stubEvaluator{
		evaluates: func(_ context.Context, _ *domain.TopLevelArgument, _ []*domain.Node) ([]*domain.Result, error) {
			// Cancellation arrives while the first target is being evaluated.
			cancel()
			return []*domain.Result{{LintFile: "rule.jsonnet"}}, nil
		},
	}
	l := NewLinter(dataParser, parser, evaluator)
	_, err := l.Lint(ctx, newTargets(2))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Lint() error = %v, want context.Canceled", err)
	}
}

func TestLinter_Lint_canceledDuringDataParseIsNotFinding(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	parser := &stubLintFileParser{
		parses: func(_ context.Context, _ []*config.LintFile) ([]*domain.Node, error) {
			return []*domain.Node{{Key: "rule.jsonnet"}}, nil
		},
	}
	dataParser := &stubDataFileParser{
		parse: func(ctx context.Context, _ *domain.Path) (*domain.TopLevelArgument, error) {
			cancel()
			return nil, ctx.Err()
		},
	}
	called := false
	evaluator := &stubEvaluator{
		evaluates: func(context.Context, *domain.TopLevelArgument, []*domain.Node) ([]*domain.Result, error) {
			called = true
			return nil, nil
		},
	}
	l := NewLinter(dataParser, parser, evaluator)
	results, err := l.Lint(ctx, newTargets(1))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Lint() error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("rules must not be evaluated after cancellation")
	}
	for _, r := range results {
		if r.Error != "" {
			t.Fatalf("cancellation must not be a lint finding, got %q", r.Error)
		}
	}
}

func TestLinter_Lint_ruleErrorIsStillFinding(t *testing.T) {
	t.Parallel()
	parser := &stubLintFileParser{
		parses: func(_ context.Context, _ []*config.LintFile) ([]*domain.Node, error) {
			return []*domain.Node{{Key: "rule.jsonnet"}}, nil
		},
	}
	dataParser := &stubDataFileParser{
		parse: func(context.Context, *domain.Path) (*domain.TopLevelArgument, error) {
			return &domain.TopLevelArgument{}, nil
		},
	}
	// Ordinary evaluation errors are converted to results with Error inside
	// Evaluates; emulate that conversion here.
	evaluator := &stubEvaluator{
		evaluates: func(context.Context, *domain.TopLevelArgument, []*domain.Node) ([]*domain.Result, error) {
			return []*domain.Result{{LintFile: "rule.jsonnet", Error: "evaluate a lint file as Jsonnet: the rule is broken"}}, nil
		},
	}
	l := NewLinter(dataParser, parser, evaluator)
	results, err := l.Lint(context.Background(), newTargets(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Error == "" {
		t.Fatalf("a rule error must be a lint finding, got %#v", results)
	}
}
