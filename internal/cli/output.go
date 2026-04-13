package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// printJSON marshals v as indented JSON and writes it to w followed by a
// newline. Any marshal error is returned to the caller as a formatted error.
func printJSON(w io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}
