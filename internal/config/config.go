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
	DefaultTimeoutMS      = 20_000
	DefaultModel          = string(anthropic.ModelClaudeHaiku4_5)
	DefaultProvider       = "anthropic"
	DefaultLogMaxSize     = 5 * 1024 * 1024 // 5MB
	DefaultMetricsMaxSize = 2 * 1024 * 1024 // 2MB
	BaseConfigName        = "ccgate.jsonnet"
	LocalConfigName       = "ccgate.local.jsonnet"
)

type Config struct {
	Provider        ProviderConfig `json:"provider"`
	LogPath         string         `json:"log_path"`
	LogDisabled     *bool          `json:"log_disabled"`
	LogMaxSize      *int64         `json:"log_max_size"`
	MetricsPath     string         `json:"metrics_path"`
	MetricsDisabled *bool          `json:"metrics_disabled"`
	MetricsMaxSize  *int64         `json:"metrics_max_size"`
	Allow           []string       `json:"allow"`
	Deny            []string       `json:"deny"`
	Environment     []string       `json:"environment"`
}

type ProviderConfig struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	TimeoutMS *int   `json:"timeout_ms"`
}

// GetTimeoutMS returns the timeout in milliseconds.
// nil defaults to DefaultTimeoutMS; 0 means no timeout.
func (p ProviderConfig) GetTimeoutMS() int {
	if p.TimeoutMS == nil {
		return DefaultTimeoutMS
	}
	return *p.TimeoutMS
}

func Default() Config {
	sd := stateDir()
	return Config{
		Provider: ProviderConfig{
			Name:      DefaultProvider,
			Model:     DefaultModel,
			TimeoutMS: intPtr(DefaultTimeoutMS),
		},
		LogPath:        filepath.Join(sd, "ccgate.log"),
		LogMaxSize:     int64Ptr(DefaultLogMaxSize),
		MetricsPath:    filepath.Join(sd, "metrics.jsonl"),
		MetricsMaxSize: int64Ptr(DefaultMetricsMaxSize),
	}
}

func intPtr(v int) *int       { return &v }
func int64Ptr(v int64) *int64 { return &v }

// GetTimeoutMS returns the provider timeout in milliseconds.
// nil defaults to DefaultTimeoutMS.
func (c Config) GetTimeoutMS() int {
	return c.Provider.GetTimeoutMS()
}

// IsLogDisabled returns whether logging is disabled.
func (c Config) IsLogDisabled() bool {
	return c.LogDisabled != nil && *c.LogDisabled
}

// IsMetricsDisabled returns whether metrics collection is disabled.
func (c Config) IsMetricsDisabled() bool {
	return c.MetricsDisabled != nil && *c.MetricsDisabled
}

// GetLogMaxSize returns the log max size, defaulting to DefaultLogMaxSize.
// 0 means no rotation.
func (c Config) GetLogMaxSize() int64 {
	if c.LogMaxSize == nil {
		return DefaultLogMaxSize
	}
	return *c.LogMaxSize
}

// GetMetricsMaxSize returns the metrics max size, defaulting to DefaultMetricsMaxSize.
// 0 means no rotation.
func (c Config) GetMetricsMaxSize() int64 {
	if c.MetricsMaxSize == nil {
		return DefaultMetricsMaxSize
	}
	return *c.MetricsMaxSize
}

// stateDir returns the XDG_STATE_HOME-based directory for ccgate state (logs, metrics).
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "ccgate")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "ccgate")
	}
	return "."
}

// resolvePath expands ~ prefix in a path.
func resolvePath(p string) string {
	if after, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, after)
		}
	}
	return p
}

// ResolveLogPath returns the resolved log file path.
func (c Config) ResolveLogPath() string {
	if c.LogPath == "" {
		return filepath.Join(stateDir(), "ccgate.log")
	}
	return resolvePath(c.LogPath)
}

// ResolveMetricsPath returns the resolved metrics file path.
func (c Config) ResolveMetricsPath() string {
	if c.MetricsPath == "" {
		return filepath.Join(stateDir(), "metrics.jsonl")
	}
	return resolvePath(c.MetricsPath)
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
	if override.Provider.TimeoutMS != nil {
		cfg.Provider.TimeoutMS = override.Provider.TimeoutMS
	}
	if override.LogPath != "" {
		cfg.LogPath = override.LogPath
	}
	if override.LogDisabled != nil {
		cfg.LogDisabled = override.LogDisabled
	}
	if override.LogMaxSize != nil {
		cfg.LogMaxSize = override.LogMaxSize
	}
	if override.MetricsPath != "" {
		cfg.MetricsPath = override.MetricsPath
	}
	if override.MetricsDisabled != nil {
		cfg.MetricsDisabled = override.MetricsDisabled
	}
	if override.MetricsMaxSize != nil {
		cfg.MetricsMaxSize = override.MetricsMaxSize
	}

	cfg.Allow = append(cfg.Allow, override.Allow...)
	cfg.Deny = append(cfg.Deny, override.Deny...)
	cfg.Environment = append(cfg.Environment, override.Environment...)

	return nil
}
