package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newDocsCommand(runtime *Runtime) *cobra.Command {
	docs := &cobra.Command{
		Use:    "docs",
		Short:  "Generate CLI documentation",
		Hidden: true,
	}
	var output string
	generate := &cobra.Command{
		Use:    "generate",
		Short:  "Generate docs/cli.md",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			body, err := generateCLIDocs()
			if err != nil {
				return err
			}
			if strings.TrimSpace(output) == "-" {
				_, err := io.WriteString(runtime.stdout, body)
				return err
			}
			return os.WriteFile(output, []byte(body), 0o644)
		},
	}
	generate.Flags().StringVar(&output, "output", "docs/cli.md", "output markdown path or -")
	docs.AddCommand(generate)
	return docs
}

func generateCLIDocs() (string, error) {
	var buf bytes.Buffer
	docRuntime := &Runtime{
		stdout: io.Discard,
		stderr: io.Discard,
		stdin:  strings.NewReader(""),
	}
	docRuntime.stdinIsTTY = func() bool { return false }
	root := newRootCommand(docRuntime)

	writeCLIIntro(&buf)
	writeCommandReference(&buf, root)
	return buf.String(), nil
}

func writeCLIIntro(buf *bytes.Buffer) {
	buf.WriteString("# Ledgerly CLI\n\n" +
		"## Install\n\n" +
		"Ledgerly ships as a single `ledgerly` binary. Build it locally with:\n\n" +
		"```sh\n" +
		"go build -trimpath -o ledgerly ./cmd/ledgerly\n" +
		"```\n\n" +
		"Put the binary on `PATH`, then run `ledgerly version` to verify it.\n\n" +
		"## Quickstart\n\n" +
		"```sh\n" +
		"ledgerly auth login --url https://ledgerly.example --token \"$LEDGERLY_PAT\"\n" +
		"ledgerly bank import statement.csv --account 1 --yes\n" +
		"ledgerly bank confirm 42 --yes\n" +
		"ledgerly report pl --from 2026-04-01 --to 2026-06-30\n" +
		"```\n\n" +
		"Money-moving commands require an interactive y/N prompt or `--yes` in scripts.\n\n" +
		"## Scripting\n\n" +
		"Use `--json` for stable machine output:\n\n" +
		"```sh\n" +
		"ledgerly --json invoice list --status sent | jq '.invoices[].number'\n" +
		"ledgerly --json bank feed --state suggested | jq '.transactions[].id'\n" +
		"```\n\n" +
		"Non-interactive money-moving commands fail with exit 2 unless `--yes` is present.\n" +
		"409/already-done responses return exit 1 and print the server explanation.\n\n" +
		"## PAT Scopes\n\n" +
		"Use read-only personal access tokens for reporting, dashboards, and review\n" +
		"commands. Use full-scope tokens only for write commands such as invoice sending,\n" +
		"bank reconciliation, DLA entries, and dividend declarations. Store tokens in the\n" +
		"CLI config via `ledgerly auth login`; the config file is written with 0600\n" +
		"permissions.\n\n")
}

func writeCommandReference(buf *bytes.Buffer, root *cobra.Command) {
	buf.WriteString("## Command Reference\n\n")
	writeCommandDoc(buf, root)
	for _, command := range sortedVisibleCommands(root) {
		writeCommandTree(buf, command)
	}
}

func writeCommandTree(buf *bytes.Buffer, command *cobra.Command) {
	writeCommandDoc(buf, command)
	for _, child := range sortedVisibleCommands(command) {
		writeCommandTree(buf, child)
	}
}

func writeCommandDoc(buf *bytes.Buffer, command *cobra.Command) {
	fmt.Fprintf(buf, "### `%s`\n\n", command.CommandPath())
	if strings.TrimSpace(command.Short) != "" {
		fmt.Fprintf(buf, "%s\n\n", strings.TrimSpace(command.Short))
	}
	buf.WriteString("Usage:\n\n```text\n")
	buf.WriteString(command.UseLine())
	buf.WriteString("\n```\n\n")

	if command == command.Root() {
		writeFlagDocs(buf, "Global Flags", command.PersistentFlags())
		return
	}
	writeFlagDocs(buf, "Flags", command.NonInheritedFlags())
}

func writeFlagDocs(buf *bytes.Buffer, title string, flags *pflag.FlagSet) {
	var rows []string
	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden {
			return
		}
		name := "--" + flag.Name
		if flag.Shorthand != "" {
			name = "-" + flag.Shorthand + ", " + name
		}
		usage := strings.TrimSpace(flag.Usage)
		if usage == "" {
			usage = "no description"
		}
		if flag.DefValue != "" && flag.DefValue != "false" {
			usage += " (default " + flag.DefValue + ")"
		}
		rows = append(rows, fmt.Sprintf("- `%s`: %s", name, usage))
	})
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(buf, "%s:\n\n", title)
	for _, row := range rows {
		buf.WriteString(row)
		buf.WriteString("\n")
	}
	buf.WriteString("\n")
}

func sortedVisibleCommands(command *cobra.Command) []*cobra.Command {
	commands := make([]*cobra.Command, 0, len(command.Commands()))
	for _, child := range command.Commands() {
		if child.Hidden {
			continue
		}
		commands = append(commands, child)
	}
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].CommandPath() < commands[j].CommandPath()
	})
	return commands
}
