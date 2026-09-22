package jsonnet

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/go-jsonnet"
	"github.com/google/go-jsonnet/ast"
	"github.com/spf13/afero"
)

func Read(ctx context.Context, fs afero.Fs, filePath, tla string, importer jsonnet.Importer, dest any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	vm := NewVM(tla, importer)
	node, err := ReadToNode(ctx, fs, filePath)
	if err != nil {
		return fmt.Errorf("parse a file as Jsonnet: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	result, err := vm.Evaluate(node)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// The evaluation failed because the work was canceled
			// (e.g. a module import triggered by the evaluation was canceled).
			return ctxErr
		}
		return fmt.Errorf("evaluate a file as Jsonnet: %w", err)
	}
	if err := json.Unmarshal([]byte(result), dest); err != nil {
		return fmt.Errorf("unmarshal as JSON: %w", err)
	}
	return nil
}

func ReadToNode(ctx context.Context, fs afero.Fs, filePath string) (ast.Node, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := afero.ReadFile(fs, filePath)
	if err != nil {
		return nil, fmt.Errorf("read a jsonnet file: %w", err)
	}
	ja, err := jsonnet.SnippetToAST(filePath, string(b))
	if err != nil {
		return nil, fmt.Errorf("parse a jsonnet file: %w", err)
	}
	return ja, nil
}
