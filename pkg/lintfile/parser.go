package lintfile

import (
	"context"
	"strings"

	"github.com/lintnet/lintnet/pkg/config"
	"github.com/lintnet/lintnet/pkg/domain"
	"github.com/lintnet/lintnet/pkg/jsonnet"
	"github.com/spf13/afero"
	"github.com/suzuki-shunsuke/slog-error/slogerr"
)

type Parser struct {
	fs afero.Fs
}

func NewParser(fs afero.Fs) *Parser {
	return &Parser{
		fs: fs,
	}
}

func (p *Parser) Parse(ctx context.Context, lintFile *config.LintFile) (*domain.Node, error) {
	node, err := jsonnet.ReadToNode(ctx, p.fs, lintFile.Path)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}
	return &domain.Node{
		Node:    node,
		Key:     lintFile.ID,
		Config:  lintFile.Config,
		Link:    lintFile.Link,
		Combine: strings.HasSuffix(lintFile.Path, "_combine.jsonnet"),
	}, nil
}

func (p *Parser) Parses(ctx context.Context, lintFiles []*config.LintFile) ([]*domain.Node, error) {
	nodes := make([]*domain.Node, 0, len(lintFiles))
	for _, lintFile := range lintFiles {
		if err := ctx.Err(); err != nil {
			// Don't start reading a new lint file after cancellation.
			return nil, err
		}
		node, err := p.Parse(ctx, lintFile)
		if err != nil {
			return nil, slogerr.With(err, "file_path", lintFile.Path) //nolint:wrapcheck
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}
