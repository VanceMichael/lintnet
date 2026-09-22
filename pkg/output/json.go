package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type jsonOutputter struct {
	stdout io.Writer
}

func (o *jsonOutputter) Output(result *Output) error {
	return outputJSON(o.stdout, result)
}

// outputJSON encodes the result into a buffer first and writes it in a single
// write call, so a canceled or failed process never leaves a half-written
// document behind.
func outputJSON(w io.Writer, result any) error {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return fmt.Errorf("encode the result as JSON: %w", err)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write the result: %w", err)
	}
	return nil
}
