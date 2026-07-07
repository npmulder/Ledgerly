package cli

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/npmulder/ledgerly/internal/mcp"
)

func newMCPCommand(runtime *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run the Ledgerly stdio MCP server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := loadMCPConfig(runtime.configPath)
			if err != nil {
				return err
			}
			auditWriter := runtime.stderr
			if auditWriter == nil {
				auditWriter = io.Discard
			}
			server, err := mcp.New(mcp.Config{
				BaseURL:    cfg.URL,
				Token:      cfg.Token,
				Version:    runtime.version,
				HTTPClient: runtime.httpClient,
				Logger:     slog.New(slog.NewTextHandler(auditWriter, &slog.HandlerOptions{Level: slog.LevelInfo})),
			})
			if err != nil {
				return err
			}
			return server.Serve(cmd.Context(), runtime.stdin, runtime.stdout)
		},
	}
}

func loadMCPConfig(path string) (Config, error) {
	envURL := strings.TrimRight(strings.TrimSpace(os.Getenv("LEDGERLY_URL")), "/")
	envToken := strings.TrimSpace(os.Getenv("LEDGERLY_TOKEN"))

	cfg, err := loadConfigValues(path)
	if err != nil {
		if envURL != "" && envToken != "" {
			return Config{URL: envURL, Token: envToken}, nil
		}
		return Config{}, err
	}
	if envURL != "" {
		cfg.URL = envURL
	}
	if envToken != "" {
		cfg.Token = envToken
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return Config{}, newAuthError("config is missing url; run ledgerly auth login or set LEDGERLY_URL")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return Config{}, newAuthError("config is missing token; run ledgerly auth login or set LEDGERLY_TOKEN")
	}
	return cfg, nil
}
