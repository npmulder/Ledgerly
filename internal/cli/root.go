package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/npmulder/ledgerly/internal/cli/gen"
)

type Runtime struct {
	stdout     io.Writer
	stderr     io.Writer
	httpClient *http.Client
	configPath string
	json       bool
	yes        bool
}

type Option func(*Runtime)

func WithHTTPClient(client *http.Client) Option {
	return func(runtime *Runtime) {
		runtime.httpClient = client
	}
}

func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, opts ...Option) error {
	runtime := &Runtime{
		stdout: stdout,
		stderr: stderr,
	}
	for _, opt := range opts {
		opt(runtime)
	}

	root := newRootCommand(runtime)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) {
			return err
		}
		return newUsageError(err.Error())
	}
	return nil
}

func newRootCommand(runtime *Runtime) *cobra.Command {
	root := &cobra.Command{
		Use:           "ledgerly",
		Short:         "Ledgerly API client",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVar(&runtime.json, "json", false, "emit JSON output")
	root.PersistentFlags().BoolVar(&runtime.yes, "yes", false, "confirm mutating actions")
	root.PersistentFlags().StringVar(&runtime.configPath, "config", "", "path to config.toml")
	root.AddCommand(newAuthCommand(runtime))
	return root
}

func newAuthCommand(runtime *Runtime) *cobra.Command {
	auth := &cobra.Command{
		Use:   "auth",
		Short: "Manage CLI authentication",
	}

	var loginURL string
	var loginToken string
	login := &cobra.Command{
		Use:   "login --url <url> --token <token>",
		Short: "Store a personal access token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthLogin(cmd.Context(), runtime, loginURL, loginToken)
		},
	}
	login.Flags().StringVar(&loginURL, "url", "", "Ledgerly API URL")
	login.Flags().StringVar(&loginToken, "token", "", "personal access token")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthStatus(cmd.Context(), runtime)
		},
	}

	auth.AddCommand(login, status)
	return auth
}

type authStatusOutput struct {
	URL       string               `json:"url"`
	TokenName string               `json:"token_name"`
	Scope     gen.IdentityPATScope `json:"scope"`
	Reachable bool                 `json:"reachable"`
}

func runAuthLogin(ctx context.Context, runtime *Runtime, rawURL, rawToken string) error {
	url := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	token := strings.TrimSpace(rawToken)
	if url == "" {
		return newUsageError("--url is required")
	}
	if token == "" {
		return newUsageError("--token is required")
	}

	status, err := validateAuth(ctx, runtime, Config{URL: url, Token: token})
	if err != nil {
		return err
	}
	if err := writeConfig(runtime.configPath, Config{URL: url, Token: token}); err != nil {
		return err
	}

	if runtime.json {
		return writeJSON(runtime.stdout, status)
	}
	return writeTable(runtime.stdout, []tableRow{
		{Key: "url", Value: status.URL},
		{Key: "token name", Value: status.TokenName},
		{Key: "scope", Value: string(status.Scope)},
		{Key: "reachable", Value: fmt.Sprintf("%t", status.Reachable)},
	})
}

func runAuthStatus(ctx context.Context, runtime *Runtime) error {
	cfg, err := loadConfig(runtime.configPath)
	if err != nil {
		return err
	}
	status, err := validateAuth(ctx, runtime, cfg)
	if err != nil {
		return err
	}
	if runtime.json {
		return writeJSON(runtime.stdout, status)
	}
	return writeTable(runtime.stdout, []tableRow{
		{Key: "url", Value: status.URL},
		{Key: "token name", Value: status.TokenName},
		{Key: "scope", Value: string(status.Scope)},
		{Key: "reachable", Value: fmt.Sprintf("%t", status.Reachable)},
	})
}

func validateAuth(ctx context.Context, runtime *Runtime, cfg Config) (authStatusOutput, error) {
	client, err := newAPIClient(cfg.URL, cfg.Token, runtime.httpClient)
	if err != nil {
		return authStatusOutput{}, err
	}
	user, err := client.currentUser(ctx, runtime.json)
	if err != nil {
		return authStatusOutput{}, err
	}
	tokenName := ""
	if user.TokenName != nil {
		tokenName = *user.TokenName
	}
	scope := gen.IdentityPATScope("")
	if user.TokenScope != nil {
		scope = *user.TokenScope
	}
	return authStatusOutput{
		URL:       strings.TrimRight(strings.TrimSpace(cfg.URL), "/"),
		TokenName: tokenName,
		Scope:     scope,
		Reachable: true,
	}, nil
}
