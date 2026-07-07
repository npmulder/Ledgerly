package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const configFileMode os.FileMode = 0o600

type Config struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

func defaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(dir, "ledgerly", "config.toml"), nil
}

func loadConfig(path string) (Config, error) {
	cfg, err := loadConfigValues(path)
	if err != nil {
		return Config{}, err
	}
	if cfg.URL == "" || cfg.Token == "" {
		return Config{}, newAuthError("config is missing url or token; run ledgerly auth login")
	}
	return cfg, nil
}

func loadConfigValues(path string) (Config, error) {
	resolved, err := resolveConfigPath(path)
	if err != nil {
		return Config{}, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, newAuthError("not logged in; run ledgerly auth login")
		}
		return Config{}, fmt.Errorf("stat config: %w", err)
	}
	if info.Mode().Perm() != configFileMode {
		return Config{}, newAuthError(fmt.Sprintf("config permissions are %03o, want 600: %s", info.Mode().Perm(), resolved))
	}

	file, err := os.Open(resolved)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, fmt.Errorf("parse config: invalid TOML assignment %q", line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		parsed, err := strconv.Unquote(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse config %s: %w", key, err)
		}
		values[key] = parsed
	}
	if err := scanner.Err(); err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	cfg := Config{
		URL:   strings.TrimSpace(values["url"]),
		Token: strings.TrimSpace(values["token"]),
	}
	return cfg, nil
}

func writeConfig(path string, cfg Config) error {
	resolved, err := resolveConfigPath(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return fmt.Errorf("url is required")
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return fmt.Errorf("token is required")
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	file, err := os.OpenFile(resolved, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, configFileMode)
	if err != nil {
		return fmt.Errorf("open config for write: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	if err := file.Chmod(configFileMode); err != nil {
		return fmt.Errorf("chmod config: %w", err)
	}

	body := fmt.Sprintf("url = %s\ntoken = %s\n", strconv.Quote(strings.TrimSpace(cfg.URL)), strconv.Quote(strings.TrimSpace(cfg.Token)))
	if _, err := file.WriteString(body); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func resolveConfigPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return path, nil
	}
	return defaultConfigPath()
}
