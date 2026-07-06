package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type tableRow struct {
	Key   string
	Value string
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeTable(w io.Writer, rows []tableRow) error {
	width := 0
	for _, row := range rows {
		if len(row.Key) > width {
			width = len(row.Key)
		}
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(w, "%-*s  %s\n", width, strings.ToUpper(row.Key), row.Value); err != nil {
			return err
		}
	}
	return nil
}
