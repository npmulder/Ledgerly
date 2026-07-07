package cli

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"unicode"
)

func confirmAction(runtime *Runtime, action string, renderPreview func(io.Writer) error) error {
	if runtime.yes {
		return nil
	}
	if runtime.stdinIsTTY == nil || !runtime.stdinIsTTY() {
		return newUsageError("confirmation required; rerun with: " + rerunWithYes(runtime.commandArgs))
	}

	if _, err := fmt.Fprintf(runtime.stdout, "Preview: %s\n", action); err != nil {
		return err
	}
	if renderPreview != nil {
		if err := renderPreview(runtime.stdout); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(runtime.stdout, "Continue? [y/N]: "); err != nil {
		return err
	}
	reader := bufio.NewReader(runtime.stdin)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	default:
		return newUsageError("confirmation declined")
	}
}

func rerunWithYes(args []string) string {
	parts := []string{"ledgerly", "--yes"}
	for _, arg := range args {
		if strings.TrimSpace(arg) == "--yes" {
			continue
		}
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	safe := true
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '_', '-', '.', '/', ':', ',', '=', '+', '@', '%':
			continue
		default:
			safe = false
		}
	}
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
