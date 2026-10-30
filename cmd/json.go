package cmd

import (
	"encoding/json"
	"io"
)

// writeJSON writes v to w as one JSON document indented two spaces.
func writeJSON(w io.Writer, v any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(v)
}
