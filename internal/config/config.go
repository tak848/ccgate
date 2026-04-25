package config

import (
	_ "embed"
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
	"github.com/tak848/ccgate/internal/llm"
)

//go:embed defaults.jsonnet
var DefaultsJsonnet string

//go:embed defaults_project.jsonnet
var DefaultsProjectJsonnet string

const (
	DefaultTimeoutMS      = 20_000
	DefaultModel          = string(anthropic.ModelClaudeHaiku4_5)
	DefaultProvider       = "anthropic"
	DefaultLogMaxSize     = 5 * 1024 * 1024 // 5MB
	DefaultMetricsMaxSize = 2 * 1024 * 1024 // 2MB
	BaseConfigName        = "ccgate.jsonnet"
	LocalConfigName       = "ccgate.local.jsonnet"
)

// FallthroughStrategy* aliases re-export the canonical values from
// internal/llm so existing call sites continue to compile.
const (
	FallthroughStrategyAsk   = llm.FallthroughStrategyAsk
	FallthroughStrategyAllow = llm.FallthroughStrategyAllow
	FallthroughStrategyDeny  = llm.FallthroughStrategyDeny
)

type Config struct {
	Provider            ProviderConfig `json:"provider"`
	LogPath             string         `json:"log_path"`
	LogDisabled         *bool          `json:"log_disabled"`
	LogMaxSize          *int64         `json:"log_max_size"`
	MetricsPath         string         `json:"metrics_path"`
	MetricsDisabled     *bool          `json:"metrics_disabled"`
	MetricsMaxSize      *int64         `json:"metrics_max_size"`
	FallthroughStrategy *string        `json:"fallthrough_strategy"`
	Allow               []string       `json:"allow"`
	Deny                []string       `json:"deny"`
	Environment         []string       `json:"environment"`
}

// GetFallthroughStrategy returns the configured strategy for LLM fallthrough,
// defaulting to FallthroughStrategyAsk (current behavior: defer to Claude Code).
func (c Config) GetFallthroughStrategy() string {
	if c.FallthroughStrategy == nil {
		return FallthroughStrategyAsk
	}
	return *c.FallthroughStrategy
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

// ConfigSource indicates where the base configuration came from.
type ConfigSource string

const (
	SourceEmbeddedDefaults ConfigSource = "embedded_defaults"
	SourceGlobalConfig     ConfigSource = "global_config"
)

// LoadResult holds the loaded config and metadata about the loading process.
type LoadResult struct {
	Config Config
	Source ConfigSource
}

// LoadOptions describes target-specific config search paths and the
// embedded defaults snippet. Callers (cmd/claude, cmd/codex) supply
// their own values so Load itself stays target-agnostic.
type LoadOptions struct {
	// GlobalConfigPath is the absolute path of the per-user config
	// (e.g. ~/.claude/ccgate.jsonnet, ~/.codex/ccgate.jsonnet).
	GlobalConfigPath string
	// ProjectLocalRelativePaths lists project-local config locations
	// relative to the repo root (or cwd when not in a git repo).
	// Each candidate is read in order and **appended** on top of the
	// global / embedded base. Tracked files are skipped via gitutil.
	ProjectLocalRelativePaths []string
	// EmbedDefaults is the embedded jsonnet snippet applied when the
	// global config is absent. Targets ship their own defaults via
	// //go:embed.
	EmbedDefaults string
}

// ClaudeLoadOptions returns the LoadOptions for the Claude Code hook.
// Kept here as a transitional helper so existing callers (main.go,
// tests) keep working until cmd/claude takes over orchestration. New
// callers should construct their own LoadOptions.
func ClaudeLoadOptions() LoadOptions {
	home, _ := os.UserHomeDir()
	return LoadOptions{
		GlobalConfigPath:          filepath.Join(home, ".claude", BaseConfigName),
		ProjectLocalRelativePaths: []string{filepath.Join(".claude", LocalConfigName)},
		EmbedDefaults:             DefaultsJsonnet,
	}
}

// Load reads the base config from opts.GlobalConfigPath and merges
// project-local overrides found at opts.ProjectLocalRelativePaths
// (resolved against the git repo root, or cwd when not in a repo).
// If no global config exists, opts.EmbedDefaults is used as fallback.
func Load(opts LoadOptions, cwd string) (LoadResult, error) {
	cfg := Default()

	source := SourceEmbeddedDefaults
	if err := mergeConfigFile(opts.GlobalConfigPath, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// No global config: use embedded defaults as fallback.
			if err := mergeConfigString(opts.EmbedDefaults, &cfg); err != nil {
				return LoadResult{Config: cfg}, fmt.Errorf("embedded defaults: %w", err)
			}
		} else {
			return LoadResult{Config: cfg}, fmt.Errorf("base config %s: %w", opts.GlobalConfigPath, err)
		}
	} else {
		source = SourceGlobalConfig
	}

	for _, path := range safeProjectLocalConfigPaths(cwd, opts.ProjectLocalRelativePaths) {
		if err := mergeConfigFile(path, &cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
			return LoadResult{Config: cfg}, fmt.Errorf("local config %s: %w", path, err)
		}
	}

	if err := cfg.Validate(); err != nil {
		return LoadResult{Config: cfg}, fmt.Errorf("config validation: %w", err)
	}

	return LoadResult{Config: cfg, Source: source}, nil
}

func projectLocalConfigPaths(cwd string, relativePaths []string) []string {
	if cwd == "" || len(relativePaths) == 0 {
		return nil
	}

	root := cwd
	if repoRoot, err := gitutil.RepoRoot(cwd); err == nil {
		root = repoRoot
	}

	out := make([]string, 0, len(relativePaths))
	for _, rel := range relativePaths {
		out = append(out, filepath.Join(root, rel))
	}
	return out
}

func safeProjectLocalConfigPaths(cwd string, relativePaths []string) []string {
	root := cwd
	if repoRoot, err := gitutil.RepoRoot(cwd); err == nil {
		root = repoRoot
	}

	var safe []string
	for _, path := range projectLocalConfigPaths(cwd, relativePaths) {
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
	return mergeConfigJSON(data, cfg)
}

func mergeConfigString(snippet string, cfg *Config) error {
	vm := jsonnet.MakeVM()
	data, err := vm.EvaluateAnonymousSnippet("defaults.jsonnet", snippet)
	if err != nil {
		return fmt.Errorf("evaluate jsonnet snippet: %w", err)
	}
	return mergeConfigJSON(data, cfg)
}

func mergeConfigJSON(data string, cfg *Config) error {
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
	if override.FallthroughStrategy != nil {
		cfg.FallthroughStrategy = override.FallthroughStrategy
	}

	cfg.Allow = append(cfg.Allow, override.Allow...)
	cfg.Deny = append(cfg.Deny, override.Deny...)
	cfg.Environment = append(cfg.Environment, override.Environment...)

	return nil
}
