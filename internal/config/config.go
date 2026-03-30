package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	jsonnet "github.com/google/go-jsonnet"

	"github.com/tak848/ccgate/internal/gitutil"
)

const (
	DefaultTimeoutMS = 20_000
	DefaultModel     = string(anthropic.ModelClaudeHaiku4_5)
	DefaultProvider  = "anthropic"
	DefaultLogPath   = "~/.claude/logs/ccgate.log"
	BaseConfigName   = "permission-gate.jsonnet"
	LocalConfigName  = "permission-gate.local.jsonnet"
)

type Config struct {
	Provider    ProviderConfig `json:"provider"`
	LogPath     string         `json:"log_path"`
	LogDisabled bool           `json:"log_disabled"`
	Allow       []string       `json:"allow"`
	Deny        []string       `json:"deny"`
	Environment []string       `json:"environment"`
}

type ProviderConfig struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	TimeoutMS int    `json:"timeout_ms"`
}

func Default() Config {
	return Config{
		Provider: ProviderConfig{
			Name:      DefaultProvider,
			Model:     DefaultModel,
			TimeoutMS: DefaultTimeoutMS,
		},
		LogPath: DefaultLogPath,
	}
}

// ResolveLogPath expands ~ in LogPath and returns the absolute path.
func (c Config) ResolveLogPath() string {
	p := c.LogPath
	if p == "" {
		p = DefaultLogPath
	}
	if after, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, after)
		}
	}
	return p
}

// Load reads the base config from ~/.claude/ and merges project-local overrides.
func Load(cwd string) (Config, error) {
	cfg := Default()

	home, err := os.UserHomeDir()
	if err != nil {
		return cfg, fmt.Errorf("user home dir: %w", err)
	}

	basePath := filepath.Join(home, ".claude", BaseConfigName)
	if err := mergeConfigFile(basePath, &cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		return cfg, fmt.Errorf("base config %s: %w", basePath, err)
	}

	for _, path := range safeProjectLocalConfigPaths(cwd) {
		if err := mergeConfigFile(path, &cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
			return cfg, fmt.Errorf("local config %s: %w", path, err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return cfg, fmt.Errorf("config validation: %w", err)
	}

	return cfg, nil
}

func projectLocalConfigPaths(cwd string) []string {
	if cwd == "" {
		return nil
	}

	root := cwd
	if repoRoot, err := gitutil.RepoRoot(cwd); err == nil {
		root = repoRoot
	}

	return []string{
		filepath.Join(root, LocalConfigName),
		filepath.Join(root, ".claude", LocalConfigName),
	}
}

func safeProjectLocalConfigPaths(cwd string) []string {
	root := cwd
	if repoRoot, err := gitutil.RepoRoot(cwd); err == nil {
		root = repoRoot
	}

	var safe []string
	for _, path := range projectLocalConfigPaths(cwd) {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		tracked, err := gitutil.IsTracked(root, path)
		if err != nil {
			slog.Warn("skipping local config: git check failed", "path", path, "error", err)
			continue
		}
		if tracked {
			continue
		}
		safe = append(safe, path)
	}
	return safe
}

func mergeConfigFile(path string, cfg *Config) error {
	vm := jsonnet.MakeVM()
	data, err := vm.EvaluateFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		// go-jsonnet wraps file-not-found in its own error type
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return fmt.Errorf("evaluate jsonnet: %w", err)
	}

	var override Config
	if err := json.Unmarshal([]byte(data), &override); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}

	if override.Provider.Name != "" {
		cfg.Provider.Name = override.Provider.Name
	}
	if override.Provider.Model != "" {
		cfg.Provider.Model = override.Provider.Model
	}
	if override.Provider.TimeoutMS > 0 {
		cfg.Provider.TimeoutMS = override.Provider.TimeoutMS
	}
	if override.LogPath != "" {
		cfg.LogPath = override.LogPath
	}
	if override.LogDisabled {
		cfg.LogDisabled = true
	}

	cfg.Allow = append(cfg.Allow, override.Allow...)
	cfg.Deny = append(cfg.Deny, override.Deny...)
	cfg.Environment = append(cfg.Environment, override.Environment...)

	return nil
}
