package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// setHomeEnv sets the env var that os.UserHomeDir consults on the current OS.
// Mirrors homeEnvName in the Go stdlib (cmd/go/internal/vcweb/script.go) so
// tests that need to redirect the user home dir work identically on Windows
// (USERPROFILE), plan9 (home), and everything else (HOME).
func setHomeEnv(t *testing.T, dir string) {
	t.Helper()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("USERPROFILE", dir)
	case "plan9":
		t.Setenv("home", dir)
	default:
		t.Setenv("HOME", dir)
	}
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := Default()
	if cfg.Provider.Name != DefaultProvider {
		t.Fatalf("provider.name = %q, want %q", cfg.Provider.Name, DefaultProvider)
	}
	if cfg.Provider.Model != DefaultModel {
		t.Fatalf("provider.model = %q, want %q", cfg.Provider.Model, DefaultModel)
	}
	if cfg.Provider.GetTimeoutMS() != DefaultTimeoutMS {
		t.Fatalf("provider.timeout_ms = %d, want %d", cfg.Provider.GetTimeoutMS(), DefaultTimeoutMS)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	negTimeout := -1
	bogusStrategy := "block"
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "empty provider name",
			cfg:  Config{Provider: ProviderConfig{Name: "", Model: "m", TimeoutMS: intPtr(1000)}},
		},
		{
			name: "empty model",
			cfg:  Config{Provider: ProviderConfig{Name: "anthropic", Model: "", TimeoutMS: intPtr(1000)}},
		},
		{
			name: "negative timeout",
			cfg:  Config{Provider: ProviderConfig{Name: "anthropic", Model: "m", TimeoutMS: &negTimeout}},
		},
		{
			name: "invalid fallthrough_strategy",
			cfg: Config{
				Provider:            ProviderConfig{Name: "anthropic", Model: "m", TimeoutMS: intPtr(1000)},
				FallthroughStrategy: &bogusStrategy,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if err := tt.cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateAuthFields(t *testing.T) {
	t.Parallel()

	base := func(a *AuthConfig) Config {
		// Required fields filled in so the only validation outcome we
		// observe is whatever the table case targets.
		return Config{Provider: ProviderConfig{
			Name:      "anthropic",
			Model:     "m",
			TimeoutMS: intPtr(1000),
			Auth:      a,
		}}
	}

	// Build OS-absolute paths so the file-branch cases pass on
	// Windows too (filepath.IsAbs rejects bare `/k` on Windows; it
	// wants a drive letter or UNC).
	abs := func(rel string) string {
		p, err := filepath.Abs(rel)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	absKey := abs("k")

	cases := map[string]struct {
		auth    *AuthConfig
		wantErr bool
	}{
		// auth omit = env var path, must validate cleanly.
		"omit auth": {auth: nil, wantErr: false},

		// type=exec: command required, refresh_margin/timeout/cache_key optional.
		"exec ok":                 {auth: &AuthConfig{Type: "exec", Command: "echo"}, wantErr: false},
		"exec missing command":    {auth: &AuthConfig{Type: "exec"}, wantErr: true},
		"exec with path":          {auth: &AuthConfig{Type: "exec", Command: "x", Path: stringPtr("y")}, wantErr: true},
		"exec with cache_key":     {auth: &AuthConfig{Type: "exec", Command: "x", CacheKey: "prod"}, wantErr: false},
		"exec with cache_key var": {auth: &AuthConfig{Type: "exec", Command: "x", CacheKey: "${AWS_PROFILE}"}, wantErr: false},

		// refresh_margin_ms: >= 0 accepted, 0 allowed (disables guard),
		// negative rejected.
		"refresh_margin 30000": {auth: &AuthConfig{Type: "exec", Command: "x", RefreshMarginMS: intPtr(30000)}, wantErr: false},
		"refresh_margin 0":     {auth: &AuthConfig{Type: "exec", Command: "x", RefreshMarginMS: intPtr(0)}, wantErr: false},
		"refresh_margin -1":    {auth: &AuthConfig{Type: "exec", Command: "x", RefreshMarginMS: intPtr(-1)}, wantErr: true},

		// timeout_ms: > 0 required, 0 rejected (would wedge hot path).
		"timeout 5000":     {auth: &AuthConfig{Type: "exec", Command: "x", TimeoutMS: intPtr(5000)}, wantErr: false},
		"timeout 0":        {auth: &AuthConfig{Type: "exec", Command: "x", TimeoutMS: intPtr(0)}, wantErr: true},
		"timeout negative": {auth: &AuthConfig{Type: "exec", Command: "x", TimeoutMS: intPtr(-1)}, wantErr: true},

		// type=file: path required (absolute or ~/), command/timeout_ms/cache_key forbidden,
		// refresh_margin_ms allowed (minimum-remaining-TTL guard).
		"file abs":               {auth: &AuthConfig{Type: "file", Path: stringPtr(absKey)}, wantErr: false},
		"file home":              {auth: &AuthConfig{Type: "file", Path: stringPtr("~/.ccgate/key")}, wantErr: false},
		"file relative dot":      {auth: &AuthConfig{Type: "file", Path: stringPtr("./key")}, wantErr: false},
		"file relative bare":     {auth: &AuthConfig{Type: "file", Path: stringPtr("key")}, wantErr: false},
		"file path omitted":      {auth: &AuthConfig{Type: "file"}, wantErr: false},
		"file path empty string": {auth: &AuthConfig{Type: "file", Path: stringPtr("")}, wantErr: true},
		"file bare ~":            {auth: &AuthConfig{Type: "file", Path: stringPtr("~")}, wantErr: true},
		"file bare ~/":           {auth: &AuthConfig{Type: "file", Path: stringPtr("~/")}, wantErr: true},
		"file with command":      {auth: &AuthConfig{Type: "file", Path: stringPtr(absKey), Command: "x"}, wantErr: true},
		"file with timeout":      {auth: &AuthConfig{Type: "file", Path: stringPtr(absKey), TimeoutMS: intPtr(5000)}, wantErr: false},
		"file with zero timeout": {auth: &AuthConfig{Type: "file", Path: stringPtr(absKey), TimeoutMS: intPtr(0)}, wantErr: true},
		"file with cache_key":    {auth: &AuthConfig{Type: "file", Path: stringPtr(absKey), CacheKey: "x"}, wantErr: true},
		"file refresh_margin":    {auth: &AuthConfig{Type: "file", Path: stringPtr(absKey), RefreshMarginMS: intPtr(60000)}, wantErr: false},

		// Unknown type values are rejected — keeps the discriminator
		// closed so editors and validate agree on what's accepted.
		"unknown type":   {auth: &AuthConfig{Type: "wif"}, wantErr: true},
		"empty type":     {auth: &AuthConfig{Type: ""}, wantErr: true},
		"missing fields": {auth: &AuthConfig{Type: "exec"}, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := base(tc.auth).Validate()
			if tc.wantErr && err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestAuthDurationDefaults(t *testing.T) {
	t.Parallel()

	a := AuthConfig{}
	wantMargin := time.Duration(DefaultAuthRefreshMarginMS) * time.Millisecond
	wantTimeout := time.Duration(DefaultAuthTimeoutMS) * time.Millisecond
	if got := a.GetRefreshMargin(); got != wantMargin {
		t.Fatalf("GetRefreshMargin() default = %s, want %s", got, wantMargin)
	}
	if got := a.GetTimeout(); got != wantTimeout {
		t.Fatalf("GetTimeout() default = %s, want %s", got, wantTimeout)
	}

	a.RefreshMarginMS = intPtr(90000)
	a.TimeoutMS = intPtr(12000)
	if got := a.GetRefreshMargin(); got != 90*time.Second {
		t.Fatalf("GetRefreshMargin() = %s, want 90s", got)
	}
	if got := a.GetTimeout(); got != 12*time.Second {
		t.Fatalf("GetTimeout() = %s, want 12s", got)
	}
}

// TestRejectUnknownFields makes sure the JSON decoder rejects any
// field the Config struct does not declare. We exercise both
// previously-proposed names (the api_key_* set, which were renamed
// to provider.auth before any release shipped them) and a generic
// typo (`base_url_typo`) to keep the contract uniform: there is no
// special-cased "migrate from X" path for any specific field, the
// rejection is the same shape regardless of which key was wrong.
// Catches typos in any layer of the config — provider, top-level,
// or otherwise — without us having to anticipate which fields users
// might mistype.
func TestRejectUnknownFields(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		snippet string
		wantKey string
	}{
		"api_key_command":         {`{"provider": {"name": "anthropic", "api_key_command": "echo"}}`, "api_key_command"},
		"api_key_file":            {`{"provider": {"name": "anthropic", "api_key_file": "/x"}}`, "api_key_file"},
		"api_key_refresh_margin":  {`{"provider": {"name": "anthropic", "api_key_refresh_margin": "30s"}}`, "api_key_refresh_margin"},
		"api_key_command_timeout": {`{"provider": {"name": "anthropic", "api_key_command_timeout": "5s"}}`, "api_key_command_timeout"},
		"provider typo":           {`{"provider": {"name": "anthropic", "base_url_typo": "x"}}`, "base_url_typo"},
		"top-level typo":          {`{"alllow": ["x"]}`, "alllow"},
		"auth typo":               {`{"provider": {"name": "anthropic", "auth": {"type": "exec", "command": "echo", "cmd": "x"}}}`, "cmd"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			err := mergeConfigJSON(tc.snippet, &cfg)
			if err == nil {
				t.Fatalf("expected unknown-field error for %q", name)
			}
			// The Go decoder produces `unknown field "X"` — assert on
			// the field name only, no special-cased migration phrasing.
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("error %q must mention the offending key %q", err.Error(), tc.wantKey)
			}
		})
	}
}

func TestFallthroughStrategy(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		jsonnet string
		want    string
		wantNil bool
	}{
		"unset returns ask default": {
			jsonnet: `{}`,
			want:    FallthroughStrategyAsk,
			wantNil: true,
		},
		"explicit ask": {
			jsonnet: `{ fallthrough_strategy: 'ask' }`,
			want:    FallthroughStrategyAsk,
		},
		"explicit allow": {
			jsonnet: `{ fallthrough_strategy: 'allow' }`,
			want:    FallthroughStrategyAllow,
		},
		"explicit deny": {
			jsonnet: `{ fallthrough_strategy: 'deny' }`,
			want:    FallthroughStrategyDeny,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := Default()
			if err := mergeConfigString(tc.jsonnet, &cfg); err != nil {
				t.Fatalf("merge: %v", err)
			}
			if tc.wantNil && cfg.FallthroughStrategy != nil {
				t.Fatalf("expected nil pointer, got %q", *cfg.FallthroughStrategy)
			}
			if got := cfg.GetFallthroughStrategy(); got != tc.want {
				t.Fatalf("GetFallthroughStrategy = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateZeroTimeoutIsValid(t *testing.T) {
	t.Parallel()

	cfg := Default()
	cfg.Provider.TimeoutMS = intPtr(0)
	if err := cfg.Validate(); err != nil {
		t.Fatalf("timeout_ms=0 should be valid (unlimited), got: %v", err)
	}
}

func TestMergeConfigFileLoadsGuidance(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "ccgate.local.jsonnet")
	if err := os.WriteFile(path, []byte(`{ allow: ['Read-only test guidance'] }`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	if err := mergeConfigFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	// Default() ships an empty allow list, so replace and append both
	// observe the same result here. The replace-vs-append distinction
	// when the base is non-empty lives in TestLoadLayerSemantics.
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "Read-only test guidance" {
		t.Fatalf("unexpected allow: %v", cfg.Allow)
	}
}

func TestMergeConfigFileNotFound(t *testing.T) {
	t.Parallel()

	cfg := Default()
	err := mergeConfigFile("/nonexistent/path.jsonnet", &cfg)
	if !os.IsNotExist(err) {
		t.Fatalf("expected os.ErrNotExist, got %v", err)
	}
}

// TestMergeConfigFileReplacesProviderBlock verifies that `provider`
// is merged atomically: when an override layer writes the block, the
// whole struct is replaced so unrelated fields (e.g. a base_url left
// over from a lower layer's proxy config) cannot leak into a new
// provider's settings. timeout_ms uses *int so a missing override
// falls back to the package default via GetTimeoutMS().
func TestMergeConfigFileReplacesProviderBlock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonnet")
	content := `{ provider: {
		name: "openai",
		model: "custom-model",
		base_url: "https://proxy.example/v1",
		auth: {
			type: "exec",
			command: "echo sk-test",
			refresh_margin_ms: 45000,
			timeout_ms: 8000,
			cache_key: "prod",
		},
		timeout_ms: 30000,
	} }`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	// Pre-seed a base_url that should NOT leak through after the
	// override replaces the block.
	cfg.Provider.BaseURL = "https://stale.example/v1"

	if err := mergeConfigFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Name != "openai" {
		t.Fatalf("name = %q, want %q", cfg.Provider.Name, "openai")
	}
	if cfg.Provider.Model != "custom-model" {
		t.Fatalf("model = %q, want %q", cfg.Provider.Model, "custom-model")
	}
	if cfg.Provider.BaseURL != "https://proxy.example/v1" {
		t.Fatalf("base_url = %q, want %q", cfg.Provider.BaseURL, "https://proxy.example/v1")
	}
	auth := cfg.Provider.Auth
	if auth == nil {
		t.Fatal("auth is nil after merge")
	}
	if auth.Type != "exec" {
		t.Fatalf("auth.type = %q, want %q", auth.Type, "exec")
	}
	if auth.Command != "echo sk-test" {
		t.Fatalf("auth.command = %q, want %q", auth.Command, "echo sk-test")
	}
	if auth.RefreshMarginMS == nil || *auth.RefreshMarginMS != 45000 {
		t.Fatalf("auth.refresh_margin_ms = %v, want 45000", auth.RefreshMarginMS)
	}
	if got := auth.GetRefreshMargin(); got != 45*time.Second {
		t.Fatalf("GetRefreshMargin() = %s, want 45s", got)
	}
	if auth.TimeoutMS == nil || *auth.TimeoutMS != 8000 {
		t.Fatalf("auth.timeout_ms = %v, want 8000", auth.TimeoutMS)
	}
	if got := auth.GetTimeout(); got != 8*time.Second {
		t.Fatalf("GetTimeout() = %s, want 8s", got)
	}
	if auth.CacheKey != "prod" {
		t.Fatalf("auth.cache_key = %q, want %q", auth.CacheKey, "prod")
	}
	if cfg.Provider.GetTimeoutMS() != 30000 {
		t.Fatalf("timeout_ms = %d, want 30000", cfg.Provider.GetTimeoutMS())
	}
}

// TestMergeConfigFileProviderBlockClearsBaseURL guards the specific
// regression that motivated the atomic-block semantics: switching
// provider in an upper layer must not leave the previous provider's
// base_url behind.
func TestMergeConfigFileProviderBlockClearsBaseURL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonnet")
	// Upper layer switches to anthropic without specifying base_url.
	content := `{ provider: { name: "anthropic", model: "claude-haiku-4-5" } }`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	// Lower layer had been configured for an OpenAI-compatible proxy
	// (base_url override pointing at the proxy's /v1 endpoint).
	cfg.Provider = ProviderConfig{
		Name:    "openai",
		Model:   "anthropic/claude-haiku-4-5",
		BaseURL: "http://localhost:4000/v1",
	}

	if err := mergeConfigFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Name != "anthropic" {
		t.Fatalf("name = %q, want %q", cfg.Provider.Name, "anthropic")
	}
	if cfg.Provider.BaseURL != "" {
		t.Fatalf("base_url = %q, want empty (atomic replace must clear stale URL)", cfg.Provider.BaseURL)
	}
}

// TestMergeConfigFileOmittedProviderKeepsExisting verifies that a
// layer that does not write `provider` at all leaves the carried-over
// block intact (only an explicit `provider: {...}` triggers the
// atomic replace).
func TestMergeConfigFileOmittedProviderKeepsExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonnet")
	content := `{ allow: ["something"] }`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	cfg.Provider = ProviderConfig{
		Name:    "openai",
		Model:   "anthropic/claude-haiku-4-5",
		BaseURL: "http://localhost:4000/v1",
	}

	if err := mergeConfigFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Name != "openai" || cfg.Provider.BaseURL != "http://localhost:4000/v1" {
		t.Fatalf("provider was clobbered when override omitted the key: %+v", cfg.Provider)
	}
}

func TestProjectLocalConfigPaths(t *testing.T) {
	t.Parallel()

	const cwd = "/tmp/repo/subdir"
	relativePaths := []string{
		filepath.Join(".claude", LocalConfigName),
		filepath.Join(".codex", LocalConfigName),
	}
	got := projectLocalConfigPaths(cwd, relativePaths)

	// Contract: each relative path is anchored at the repo root (or
	// cwd when not in a git repo) and returned in the order given.
	// Path separators are OS-native; expected values are composed
	// with filepath.Join (mirrors Go stdlib's cross-platform pattern
	// in path/filepath/path_test.go).
	want := []string{
		filepath.Join(cwd, ".claude", LocalConfigName),
		filepath.Join(cwd, ".codex", LocalConfigName),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projectLocalConfigPaths(%q) = %v, want %v", cwd, got, want)
	}
}

func TestProjectLocalConfigPathsEmpty(t *testing.T) {
	t.Parallel()

	if got := projectLocalConfigPaths("", []string{".claude/" + LocalConfigName}); got != nil {
		t.Fatalf("empty cwd: expected nil, got %v", got)
	}
	if got := projectLocalConfigPaths("/tmp/repo", nil); got != nil {
		t.Fatalf("empty relativePaths: expected nil, got %v", got)
	}
}

func TestSafeProjectLocalConfigPathsSkipsTrackedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	relPath := filepath.Join(".claude", LocalConfigName)
	if err := os.WriteFile(filepath.Join(claudeDir, LocalConfigName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	gitRun(t, dir, "init")
	gitRun(t, dir, "config", "user.email", "test@test.com")
	gitRun(t, dir, "config", "user.name", "test")
	gitRun(t, dir, "add", "-f", relPath)

	got := safeProjectLocalConfigPaths(dir, []string{relPath})
	if len(got) != 0 {
		t.Fatalf("expected tracked file to be skipped, got %v", got)
	}
}

// fakeLoadOptions returns a target-agnostic LoadOptions used by the
// generic Load tests below. The real per-target LoadOptions live in
// the cmd/<target>/ packages and are tested there.
func fakeLoadOptions(home string) LoadOptions {
	return LoadOptions{
		GlobalConfigPath:          filepath.Join(home, ".fake", BaseConfigName),
		ProjectLocalRelativePaths: []string{filepath.Join(".fake", LocalConfigName)},
		EmbedDefaults:             `{ provider: { name: 'anthropic', model: 'claude-haiku-4-5' }, allow: ['default-allow'], deny: ['default-deny'] }`,
		DefaultLogPath:            filepath.Join(home, ".local/state/ccgate/fake/ccgate.log"),
		DefaultMetricsPath:        filepath.Join(home, ".local/state/ccgate/fake/metrics.jsonl"),
	}
}

func TestLoadFallsBackToEmbedDefaultsWhenNoGlobalConfig(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel.
	dir := t.TempDir()
	setHomeEnv(t, dir)

	lr, err := Load(fakeLoadOptions(dir), "")
	if err != nil {
		t.Fatal(err)
	}
	if lr.Source != SourceEmbeddedDefaults {
		t.Fatalf("source = %q, want %q", lr.Source, SourceEmbeddedDefaults)
	}
	if got := lr.Config.Allow; len(got) != 1 || got[0] != "default-allow" {
		t.Fatalf("unexpected allow from embed defaults: %v", got)
	}
}

func TestLoadLayerSemantics(t *testing.T) {
	// fakeLoadOptions seeds the embedded layer with
	// allow=["default-allow"] and deny=["default-deny"]; each test
	// layers a global config on top with a different shape so the
	// per-field merge contract is exercised end-to-end (replace via
	// `allow`, extend via `append_allow`, scalar overwrite, omitted
	// fields fall through to the embedded value).
	type want struct {
		allow []string
		deny  []string
		model string
	}
	cases := map[string]struct {
		global string
		want   want
	}{
		"global omits lists -- embedded survives": {
			// `provider` is merged atomically (not per-field), so an
			// override that wants to bump just the model must restate
			// the whole block; otherwise embedded name/timeout are
			// also replaced. This case verifies that embedded list
			// fields survive when the global only writes `provider`.
			global: `{ provider: { name: 'anthropic', model: 'claude-sonnet-4-6' } }`,
			want: want{
				allow: []string{"default-allow"},
				deny:  []string{"default-deny"},
				model: "claude-sonnet-4-6",
			},
		},
		"global allow replaces embedded allow": {
			global: `{ allow: ['Custom allow'] }`,
			want: want{
				allow: []string{"Custom allow"},
				deny:  []string{"default-deny"},
				model: "claude-haiku-4-5",
			},
		},
		"global append_allow extends embedded allow": {
			global: `{ append_allow: ['Custom allow'] }`,
			want: want{
				allow: []string{"default-allow", "Custom allow"},
				deny:  []string{"default-deny"},
				model: "claude-haiku-4-5",
			},
		},
		"global allow=[] replaces embedded allow with empty": {
			global: `{ allow: [] }`,
			want: want{
				allow: []string{},
				deny:  []string{"default-deny"},
				model: "claude-haiku-4-5",
			},
		},
		"global allow + append_allow stack": {
			global: `{ allow: ['Replaced'], append_allow: ['Then appended'] }`,
			want: want{
				allow: []string{"Replaced", "Then appended"},
				deny:  []string{"default-deny"},
				model: "claude-haiku-4-5",
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// t.Setenv is incompatible with t.Parallel.
			dir := t.TempDir()
			fakeDir := filepath.Join(dir, ".fake")
			if err := os.MkdirAll(fakeDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(fakeDir, BaseConfigName), []byte(tc.global), 0o644); err != nil {
				t.Fatal(err)
			}
			setHomeEnv(t, dir)

			lr, err := Load(fakeLoadOptions(dir), "")
			if err != nil {
				t.Fatal(err)
			}
			if lr.Source != SourceGlobalConfig {
				t.Fatalf("source = %q, want %q", lr.Source, SourceGlobalConfig)
			}
			if !reflect.DeepEqual(lr.Config.Allow, tc.want.allow) {
				t.Errorf("allow = %v, want %v", lr.Config.Allow, tc.want.allow)
			}
			if !reflect.DeepEqual(lr.Config.Deny, tc.want.deny) {
				t.Errorf("deny = %v, want %v", lr.Config.Deny, tc.want.deny)
			}
			if lr.Config.Provider.Model != tc.want.model {
				t.Errorf("provider.model = %q, want %q", lr.Config.Provider.Model, tc.want.model)
			}
			// AppendAllow / AppendDeny / AppendEnvironment are
			// parse-time-only knobs. They must not leak into the
			// resolved Config, otherwise downstream consumers (e.g.
			// schema-export, debug dumps) would see duplicate state.
			if lr.Config.AppendAllow != nil || lr.Config.AppendDeny != nil || lr.Config.AppendEnvironment != nil {
				t.Errorf("append_* fields leaked into resolved config: allow=%v deny=%v env=%v",
					lr.Config.AppendAllow, lr.Config.AppendDeny, lr.Config.AppendEnvironment)
			}
		})
	}
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
