package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	jsonnet "github.com/google/go-jsonnet"
	"github.com/google/go-jsonnet/ast"
	"github.com/invopop/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"

	"github.com/tak848/ccgate/internal/gitutil"
	"github.com/tak848/ccgate/internal/llm"
)

const (
	DefaultTimeoutMS      = 20_000
	DefaultModel          = string(anthropic.ModelClaudeHaiku4_5)
	DefaultProvider       = "anthropic"
	DefaultLogMaxSize     = 5 * 1024 * 1024 // 5MB
	DefaultMetricsMaxSize = 2 * 1024 * 1024 // 2MB
	BaseConfigName        = "ccgate.jsonnet"
	LocalConfigName       = "ccgate.local.jsonnet"

	// DefaultAuthRefreshMarginMS is the early-refresh slack used when
	// deciding whether a cached helper credential is still valid:
	// `now + margin < expires_at` keeps the cache, anything else
	// triggers a refresh. 60 seconds is comfortably larger than the
	// default `provider.timeout_ms` (20 000 ms = 20s) so a cached
	// credential will not race the next API call.
	DefaultAuthRefreshMarginMS = 60_000
	// DefaultAuthTimeoutMS caps one Resolve call. 30 seconds covers
	// AWS STS, gcloud auth print-access-token, and an internal SSO
	// key broker that opens a browser on first run; bump it higher
	// (e.g. 120 000) if your helper takes longer.
	DefaultAuthTimeoutMS = 30_000

	// AuthTypeExec / AuthTypeFile are the AuthConfig.Type values.
	AuthTypeExec = "exec"
	AuthTypeFile = "file"

	// AuthShellBash / AuthShellPowerShell are the accepted
	// AuthConfig.Shell values, mirroring the Claude Code hook
	// `shell` field. AuthShellBash is the default when Shell is
	// empty (matching Claude Code's bash default).
	AuthShellBash       = "bash"
	AuthShellPowerShell = "powershell"
)

// FallthroughStrategy* aliases re-export the canonical values from
// internal/llm so existing call sites continue to compile.
const (
	FallthroughStrategyAsk   = llm.FallthroughStrategyAsk
	FallthroughStrategyAllow = llm.FallthroughStrategyAllow
	FallthroughStrategyDeny  = llm.FallthroughStrategyDeny
)

type Config struct {
	// Schema is the editor `$schema` pointer the embedded defaults
	// (and most users) write at the top of their jsonnet so an IDE
	// can validate the config. ccgate ignores it at runtime; the
	// field exists only to satisfy DisallowUnknownFields without
	// special-casing the key name in the merger.
	Schema              string         `json:"$schema,omitempty"`
	Provider            ProviderConfig `json:"provider"`
	LogPath             string         `json:"log_path,omitempty"`
	LogDisabled         *bool          `json:"log_disabled,omitempty"`
	LogMaxSize          *int64         `json:"log_max_size,omitempty"`
	MetricsPath         string         `json:"metrics_path,omitempty"`
	MetricsDisabled     *bool          `json:"metrics_disabled,omitempty"`
	MetricsMaxSize      *int64         `json:"metrics_max_size,omitempty"`
	FallthroughStrategy *string        `json:"fallthrough_strategy,omitempty"`
	// Allow / Deny / Environment replace the value carried over from
	// previous layers when the layer sets them (even to []). Embedded
	// defaults are always layer 0, so writing `allow: [...]` in your
	// global or project-local config completely overrides ccgate's
	// shipped allow list. Use AppendAllow / AppendDeny / AppendEnvironment
	// when you want to add on top instead.
	Allow       []string `json:"allow,omitempty"`
	Deny        []string `json:"deny,omitempty"`
	Environment []string `json:"environment,omitempty"`
	// AppendAllow / AppendDeny / AppendEnvironment append onto the
	// list carried over from previous layers regardless of whether
	// the same layer also sets the replace-mode field. Typical
	// project-local use is `append_deny: ['<repo-specific>']`.
	AppendAllow       []string `json:"append_allow,omitempty"`
	AppendDeny        []string `json:"append_deny,omitempty"`
	AppendEnvironment []string `json:"append_environment,omitempty"`
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
	Name  string `json:"name"`
	Model string `json:"model"`
	// BaseURL is passed verbatim to the underlying SDK's WithBaseURL.
	// ccgate does NOT normalize the path — each SDK has its own
	// convention for what the base URL should include:
	//   - openai-go     defaults to "https://api.openai.com/v1/" and
	//                   appends "chat/completions" relative to it, so
	//                   overrides must include the "/v1" segment
	//                   (e.g. "https://my-proxy/v1").
	//   - anthropic-sdk-go defaults to "https://api.anthropic.com/" and
	//                   appends "v1/messages" itself, so overrides
	//                   stop at the host root (e.g. "https://my-proxy").
	// Empty value uses the SDK default.
	BaseURL string `json:"base_url,omitempty"`
	// Auth selects an alternative credential source for the provider.
	// When nil, ccgate reads the credential from the regular env vars
	// (`$CCGATE_<PROVIDER>_API_KEY` then `$<PROVIDER>_API_KEY`). When
	// set, env-var fallback is disabled — a misbehaving helper / file
	// surfaces as `credential_unavailable` instead of silently going
	// back to env, which would mask configuration errors.
	//
	// See AuthConfig for the discriminated union of credential
	// sources (`type=exec` shell helper, `type=file` rotator file).
	Auth      *AuthConfig `json:"auth,omitempty"`
	TimeoutMS *int        `json:"timeout_ms,omitempty"`
}

// AuthConfig is the discriminated union for short-lived / rotating
// credentials. Type selects the branch; validate rejects fields set
// on the wrong branch. See docs/api-key-helper.md for the full
// reference (output formats, caching rules, examples).
type AuthConfig struct {
	// Type discriminates: AuthTypeExec or AuthTypeFile.
	Type string `json:"type"`
	// Shell selects bash (default) or powershell for type=exec; see
	// keystore.shellInvocation for the launch details.
	Shell string `json:"shell,omitempty"`
	// Command is the shell command for type=exec.
	Command string `json:"command,omitempty"`
	// Path is the file path for type=file. nil = use the per-target
	// default; an empty pointer is rejected.
	Path *string `json:"path,omitempty"`
	// RefreshMarginMS is the early-refresh slack in milliseconds
	// (default 60 000). >= 0; 0 disables the guard.
	RefreshMarginMS *int `json:"refresh_margin_ms,omitempty"`
	// TimeoutMS bounds one Resolve call: lock + helper exec for
	// type=exec, file read for type=file (default 30 000). > 0.
	TimeoutMS *int `json:"timeout_ms,omitempty"`
	// CacheKey is a secret-free salt for the cache fingerprint
	// (type=exec only). Used as-is; pull env values via jsonnet
	// std.native('env') / must_env when you need them.
	CacheKey string `json:"cache_key,omitempty"`
}

// GetTimeoutMS returns the timeout in milliseconds.
// nil defaults to DefaultTimeoutMS; 0 means no timeout.
func (p ProviderConfig) GetTimeoutMS() int {
	if p.TimeoutMS == nil {
		return DefaultTimeoutMS
	}
	return *p.TimeoutMS
}

// GetRefreshMargin returns the refresh margin as a time.Duration,
// falling back to DefaultAuthRefreshMarginMS when unset. Validation
// guarantees the value is non-negative, so this method never returns
// a negative duration.
func (a AuthConfig) GetRefreshMargin() time.Duration {
	ms := DefaultAuthRefreshMarginMS
	if a.RefreshMarginMS != nil {
		ms = *a.RefreshMarginMS
	}
	return time.Duration(ms) * time.Millisecond
}

// GetTimeout returns the helper-exec timeout as a time.Duration,
// falling back to DefaultAuthTimeoutMS when unset. Validation
// guarantees a positive value so this method never returns 0 for an
// auth.type=exec config that reached the runner.
func (a AuthConfig) GetTimeout() time.Duration {
	ms := DefaultAuthTimeoutMS
	if a.TimeoutMS != nil {
		ms = *a.TimeoutMS
	}
	return time.Duration(ms) * time.Millisecond
}

// JSONSchema implements jsonschema.customSchemaImpl so the generated
// schemas/{claude,codex}.schema.json present `provider.auth` as a
// `oneOf` over the `type=exec` and `type=file` branches, mirroring
// the validate() rules (required field per type, additionalProperties
// false). Editor users get the same mutual-exclusion feedback that
// runtime validate would give them at hook fire time.
func (AuthConfig) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Title:       "auth",
		Description: "Discriminated union selecting the credential source for the provider.",
		OneOf: []*jsonschema.Schema{
			authExecBranchSchema(),
			authFileBranchSchema(),
		},
	}
}

func authExecBranchSchema() *jsonschema.Schema {
	props := orderedmap.New[string, *jsonschema.Schema]()
	props.Set("type", &jsonschema.Schema{Type: "string", Const: AuthTypeExec})
	props.Set("command", &jsonschema.Schema{Type: "string", MinLength: ptr(uint64(1)), Description: "Shell command. Stdout is the credential. Run via the configured shell (default bash)."})
	props.Set("shell", &jsonschema.Schema{Type: "string", Enum: []any{AuthShellBash, AuthShellPowerShell}, Description: "Shell that runs `command`. \"bash\" runs `bash -c <command>`. \"powershell\" runs `pwsh -Command <command>` when pwsh is on PATH (PowerShell 7+, cross-platform) and falls back to `powershell -Command <command>` (Windows PowerShell 5.1) otherwise. Default: bash."})
	props.Set("refresh_margin_ms", &jsonschema.Schema{Type: "integer", Minimum: json.Number("0"), Description: "Cache early-refresh threshold + minimum remaining TTL guard for fresh credentials, in milliseconds. Default: 60000."})
	props.Set("timeout_ms", &jsonschema.Schema{Type: "integer", Minimum: json.Number("1"), Description: "Hard cap on one Resolve call (lock + helper exec), in milliseconds. Default: 30000."})
	props.Set("cache_key", &jsonschema.Schema{Type: "string", Description: "Secret-free salt added to the cache fingerprint so a single command string can produce per-account cache entries (used as-is; pull env values via jsonnet std.native('env') / std.native('must_env'))."})
	return &jsonschema.Schema{
		Type:                 "object",
		Required:             []string{"type", "command"},
		Properties:           props,
		AdditionalProperties: jsonschema.FalseSchema,
	}
}

func authFileBranchSchema() *jsonschema.Schema {
	props := orderedmap.New[string, *jsonschema.Schema]()
	props.Set("type", &jsonschema.Schema{Type: "string", Const: AuthTypeFile})
	props.Set("path", &jsonschema.Schema{Type: "string", MinLength: ptr(uint64(1)), Description: "Path to the credential file. Absolute, ~/-prefixed, or relative (relative paths resolve from the hook's working directory at fire time, not the config file's directory). Omit the field to use the default $XDG_STATE_HOME/ccgate/<target>/auth_key.json; do not set it to an empty string."})
	props.Set("refresh_margin_ms", &jsonschema.Schema{Type: "integer", Minimum: json.Number("0"), Description: "Minimum remaining TTL guard for file output, in milliseconds. Default: 60000."})
	props.Set("timeout_ms", &jsonschema.Schema{Type: "integer", Minimum: json.Number("1"), Description: "Hard cap on the file read so a stalled mount surfaces as reason=timeout. Default: 30000."})
	return &jsonschema.Schema{
		Type:                 "object",
		Required:             []string{"type"},
		Properties:           props,
		AdditionalProperties: jsonschema.FalseSchema,
	}
}

func ptr[T any](v T) *T { return &v }

// Default returns a Config seeded with the provider/log/metrics
// defaults common to every target. LogPath / MetricsPath are left
// empty on purpose — Load fills them from LoadOptions so each
// target writes under its own subdirectory; Resolve* still falls
// back to the historical stateDir() root if neither is set (kept
// for the legacy file-format backward-compat tests).
func Default() Config {
	return Config{
		Provider: ProviderConfig{
			Name:      DefaultProvider,
			Model:     DefaultModel,
			TimeoutMS: intPtr(DefaultTimeoutMS),
		},
		LogMaxSize:     int64Ptr(DefaultLogMaxSize),
		MetricsMaxSize: int64Ptr(DefaultMetricsMaxSize),
	}
}

func intPtr(v int) *int          { return &v }
func int64Ptr(v int64) *int64    { return &v }
func stringPtr(v string) *string { return &v }

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

// LoadOptions describes target-specific config search paths, the
// embedded defaults snippet, and default log/metrics destinations.
// Callers (cmd/claude, cmd/codex) supply their own values so Load
// itself stays target-agnostic.
type LoadOptions struct {
	// GlobalConfigPath is the absolute path of the per-user config
	// (e.g. ~/.claude/ccgate.jsonnet, ~/.codex/ccgate.jsonnet).
	GlobalConfigPath string
	// ProjectLocalRelativePaths lists project-local config locations
	// relative to the repo root (or cwd when not in a git repo).
	// Each candidate is read in order and layered on top of the
	// global / embedded base using the same replace-or-append-*
	// semantics every layer follows (see Load). Tracked files are
	// skipped via gitutil.
	ProjectLocalRelativePaths []string
	// EmbedDefaults is the embedded jsonnet snippet always applied
	// as the first layer (the always-present base ccgate ships
	// with). Targets ship their own defaults via //go:embed.
	EmbedDefaults string
	// DefaultLogPath is used when neither the global nor any
	// project-local config sets log_path. Empty string falls back
	// to the historical stateDir() root (Resolve* compat path).
	DefaultLogPath string
	// DefaultMetricsPath behaves like DefaultLogPath but for metrics_path.
	DefaultMetricsPath string
}

// StateDir returns the $XDG_STATE_HOME/ccgate/<sub>/ directory used
// for log / metrics files. `sub` is the per-target subdirectory
// ("claude", "codex", ...). When XDG_STATE_HOME is unset, it falls
// back to ~/.local/state/ccgate/<sub>/.
func StateDir(sub string) string {
	return filepath.Join(stateDir(), sub)
}

// DefaultAuthPath is the path ccgate uses for `auth.type=file` when
// the config leaves `auth.path` empty: a single per-target file
// under StateDir.
func DefaultAuthPath(target string) string {
	return filepath.Join(StateDir(target), "auth_key.json")
}

// Load composes the runtime config from three layers, all using the
// same merge semantics:
//
//   - lists `allow` / `deny` / `environment` REPLACE the carried-over
//     value when the layer sets them (an explicit empty list clears),
//   - lists `append_allow` / `append_deny` / `append_environment` ADD
//     onto the carried-over value (can coexist with the replace-mode
//     field; replace runs first, append stacks),
//   - the `provider` block is replaced atomically as a unit (see
//     mergeConfigJSON below) — its sub-fields do NOT merge per
//     field across layers,
//   - the remaining scalars (`log_*` / `metrics_*` /
//     `fallthrough_strategy`) overwrite per-field when set.
//
// Layers, applied in order:
//
//  1. opts.EmbedDefaults -- always applied first, the always-present
//     base ccgate ships with.
//  2. opts.GlobalConfigPath -- if the file exists, layered on top.
//  3. opts.ProjectLocalRelativePaths -- each existing untracked file
//     under the repo root, layered on top in the order given.
//
// Pre-v0.6 ccgate skipped step 1 whenever step 2 succeeded, which
// made the global layer "replace" embedded defaults while project
// layers always "appended". v0.6 makes embedded defaults the
// always-present base and adds explicit `append_*` keys for opt-in
// extension; see issue #38 for the discussion.
func Load(opts LoadOptions, cwd string) (LoadResult, error) {
	cfg := Default()

	if err := mergeConfigString(opts.EmbedDefaults, &cfg); err != nil {
		return LoadResult{Config: cfg}, fmt.Errorf("embedded defaults: %w", err)
	}

	source := SourceEmbeddedDefaults
	if err := mergeConfigFile(opts.GlobalConfigPath, &cfg); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
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

	// Apply target-specific log/metrics defaults only when the user
	// did not set explicit paths in any of the merged configs. This
	// is what gives each target its own subdirectory under
	// $XDG_STATE_HOME/ccgate/<target>/ while still respecting any
	// log_path / metrics_path the user wrote in their jsonnet.
	if cfg.LogPath == "" && opts.DefaultLogPath != "" {
		cfg.LogPath = opts.DefaultLogPath
	}
	if cfg.MetricsPath == "" && opts.DefaultMetricsPath != "" {
		cfg.MetricsPath = opts.DefaultMetricsPath
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

// newJsonnetVM returns a jsonnet VM with ccgate's host-language
// extensions registered. The same VM shape is used by every entry
// point that evaluates jsonnet (file or string), so adding a new
// helper happens in one place and stays consistent across global /
// project / embedded layers.
//
// `std.native('env')(name)` returns os.Getenv(name), defaulting to an
// empty string for undefined variables (permissive). Use this when a
// config value should fall back to literal defaults when the env is
// not set.
//
// `std.native('must_env')(name)` returns os.Getenv(name) but raises a
// jsonnet evaluation error when the variable is unset, so misconfig
// surfaces at config load time instead of being silently empty. This
// is the strict variant for places where an unset env means "broken
// config".
//
// Pattern follows ecspresso v2.4+ — both functions are documented in
// docs/api-key-helper.md so users know they can use them in any
// string field, not just `auth.cache_key`.
func newJsonnetVM() *jsonnet.VM {
	vm := jsonnet.MakeVM()
	vm.NativeFunction(&jsonnet.NativeFunction{
		Name:   "env",
		Params: ast.Identifiers{"name"},
		Func: func(args []any) (any, error) {
			name, ok := args[0].(string)
			if !ok {
				return nil, fmt.Errorf("env: expected string name, got %T", args[0])
			}
			return os.Getenv(name), nil
		},
	})
	vm.NativeFunction(&jsonnet.NativeFunction{
		Name:   "must_env",
		Params: ast.Identifiers{"name"},
		Func: func(args []any) (any, error) {
			name, ok := args[0].(string)
			if !ok {
				return nil, fmt.Errorf("must_env: expected string name, got %T", args[0])
			}
			v, ok := os.LookupEnv(name)
			if !ok {
				return nil, fmt.Errorf("must_env: undefined env var %q", name)
			}
			return v, nil
		},
	})
	return vm
}

func mergeConfigFile(path string, cfg *Config) error {
	data, err := newJsonnetVM().EvaluateFile(path)
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
	data, err := newJsonnetVM().EvaluateAnonymousSnippet("defaults.jsonnet", snippet)
	if err != nil {
		return fmt.Errorf("evaluate jsonnet snippet: %w", err)
	}
	return mergeConfigJSON(data, cfg)
}

func mergeConfigJSON(data string, cfg *Config) error {
	// Reject any field the Config struct does not declare. encoding/json
	// would otherwise drop unknown keys silently, so a typo like
	// `mdoel:` or `api_key_commnd:` would leave the user wondering
	// why their value is being ignored. DisallowUnknownFields is a
	// uniform check — no special-casing per field, no fictional
	// "migrate from X" messages — so the report is the same shape
	// regardless of which key was wrong.
	dec := json.NewDecoder(strings.NewReader(data))
	dec.DisallowUnknownFields()
	var override Config
	if err := dec.Decode(&override); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}

	// `provider` is a tightly-coupled block: name / model / base_url /
	// timeout_ms / auth describe one provider together, and per-field
	// merge across layers produces incoherent combinations (e.g. a
	// higher layer switching `name` while a lower layer's `base_url`
	// for a different proxy stays stuck). When a layer specifies
	// `provider`, replace the block atomically.
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(data), &keys); err != nil {
		return fmt.Errorf("unmarshal config keys: %w", err)
	}
	if _, ok := keys["provider"]; ok {
		cfg.Provider = override.Provider
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

	// Lists: `allow` / `deny` / `environment` REPLACE the value
	// carried over from earlier layers when the current layer sets
	// the field (non-nil, even an explicit empty list). Layers that
	// omit the field leave the prior value untouched. `append_*`
	// extends instead of replacing -- both forms can coexist in the
	// same layer, in which case the replace runs first and the
	// append stacks onto the result.
	if override.Allow != nil {
		cfg.Allow = override.Allow
	}
	cfg.Allow = append(cfg.Allow, override.AppendAllow...)
	if override.Deny != nil {
		cfg.Deny = override.Deny
	}
	cfg.Deny = append(cfg.Deny, override.AppendDeny...)
	if override.Environment != nil {
		cfg.Environment = override.Environment
	}
	cfg.Environment = append(cfg.Environment, override.AppendEnvironment...)

	// `append_*` is parse-time-only; clear so the resolved Config
	// reflects the merged final lists in `Allow` / `Deny` /
	// `Environment` and never leaks the per-layer extensions.
	cfg.AppendAllow = nil
	cfg.AppendDeny = nil
	cfg.AppendEnvironment = nil

	return nil
}
