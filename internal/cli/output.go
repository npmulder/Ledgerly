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

func writeRowsTable(w io.Writer, headers []string, rows [][]string, footers ...string) error {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(strings.ToUpper(header))
	}
	for _, row := range rows {
		for i := range headers {
			value := ""
			if i < len(row) {
				value = row[i]
			}
			if len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}

	if len(headers) > 0 {
		headerRow := make([]string, len(headers))
		for i, header := range headers {
			headerRow[i] = strings.ToUpper(header)
		}
		if err := writeTableLine(w, widths, headerRow); err != nil {
			return err
		}
	}
	for _, row := range rows {
		if err := writeTableLine(w, widths, row); err != nil {
			return err
		}
	}
	for _, footer := range footers {
		if strings.TrimSpace(footer) == "" {
			continue
		}
		if _, err := fmt.Fprintln(w, footer); err != nil {
			return err
		}
	}
	return nil
}

func writeTableLine(w io.Writer, widths []int, values []string) error {
	for i, width := range widths {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		if i == len(widths)-1 {
			if _, err := fmt.Fprint(w, value); err != nil {
				return err
			}
			break
		}
		if _, err := fmt.Fprintf(w, "%-*s  ", width, value); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}
