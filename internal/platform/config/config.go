// Package config loads Ledgerly runtime configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

const (
	// Prefix is applied to all environment variable names read by this package.
	Prefix = "LEDGERLY_"

	DefaultHTTPAddr = ":8080"

	DefaultJurisdiction = "isle-of-man@1.0"
)

const (
	keyDatabaseURL  = Prefix + "DATABASE_URL"
	keyDataDir      = Prefix + "DATA_DIR"
	keyHTTPAddr     = Prefix + "HTTP_ADDR"
	keyEnv          = Prefix + "ENV"
	keyLogLevel     = Prefix + "LOG_LEVEL"
	keyJurisdiction = Prefix + "JURISDICTION"
)

// Env describes the runtime environment.
type Env string

const (
	EnvDev  Env = "dev"
	EnvProd Env = "prod"
)

// Config contains all runtime configuration required by the application shell.
type Config struct {
	DatabaseURL  string
	DataDir      string
	HTTPAddr     string
	Env          Env
	LogLevel     slog.Level
	Jurisdiction string
}

// Load reads configuration from process environment variables.
func Load() (Config, error) {
	return LoadFrom(os.LookupEnv)
}

// LoadFrom reads configuration with the supplied lookup function. It exists so
// tests can exercise parsing without mutating process-wide environment state.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	var missing []string

	databaseURL, ok := nonEmptyEnv(lookup, keyDatabaseURL)
	if !ok {
		missing = append(missing, keyDatabaseURL)
	}

	dataDir, ok := nonEmptyEnv(lookup, keyDataDir)
	if !ok {
		missing = append(missing, keyDataDir)
	}

	envValue, ok := nonEmptyEnv(lookup, keyEnv)
	if !ok {
		missing = append(missing, keyEnv)
	}

	logLevelValue, ok := nonEmptyEnv(lookup, keyLogLevel)
	if !ok {
		missing = append(missing, keyLogLevel)
	}

	if len(missing) > 0 {
		return Config{}, MissingKeysError{Keys: missing}
	}

	env, err := parseEnv(envValue)
	if err != nil {
		return Config{}, err
	}

	logLevel, err := parseLogLevel(logLevelValue)
	if err != nil {
		return Config{}, err
	}

	httpAddr, ok := nonEmptyEnv(lookup, keyHTTPAddr)
	if !ok {
		httpAddr = DefaultHTTPAddr
	}

	jurisdiction, ok := nonEmptyEnv(lookup, keyJurisdiction)
	if !ok {
		jurisdiction = DefaultJurisdiction
	}

	return Config{
		DatabaseURL:  databaseURL,
		DataDir:      dataDir,
		HTTPAddr:     httpAddr,
		Env:          env,
		LogLevel:     logLevel,
		Jurisdiction: jurisdiction,
	}, nil
}

// MissingKeysError reports all required environment variables missing from a load.
type MissingKeysError struct {
	Keys []string
}

func (e MissingKeysError) Error() string {
	return "missing required config keys: " + strings.Join(e.Keys, ", ")
}

func nonEmptyEnv(lookup func(string) (string, bool), key string) (string, bool) {
	value, ok := lookup(key)
	if !ok {
		return "", false
	}

	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}

	return value, true
}

func parseEnv(value string) (Env, error) {
	env := Env(strings.ToLower(value))
	switch env {
	case EnvDev, EnvProd:
		return env, nil
	default:
		return "", fmt.Errorf("invalid %s %q: must be one of %s, %s", keyEnv, value, EnvDev, EnvProd)
	}
}

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return 0, errors.Join(fmt.Errorf("invalid %s %q", keyLogLevel, value), err)
	}

	return level, nil
}
