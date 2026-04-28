package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
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

func TestMergeConfigFileAppendsGuidance(t *testing.T) {
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

func TestMergeConfigFileOverridesProvider(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonnet")
	content := `{ provider: { model: "custom-model", timeout_ms: 30000 } }`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := Default()
	if err := mergeConfigFile(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider.Model != "custom-model" {
		t.Fatalf("model = %q, want %q", cfg.Provider.Model, "custom-model")
	}
	if cfg.Provider.GetTimeoutMS() != 30000 {
		t.Fatalf("timeout_ms = %d, want 30000", cfg.Provider.GetTimeoutMS())
	}
	// Name should remain default
	if cfg.Provider.Name != DefaultProvider {
		t.Fatalf("name = %q, want %q", cfg.Provider.Name, DefaultProvider)
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

func TestLoadUsesGlobalConfigWhenPresent(t *testing.T) {
	// t.Setenv is incompatible with t.Parallel.
	dir := t.TempDir()
	fakeDir := filepath.Join(dir, ".fake")
	if err := os.MkdirAll(fakeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{ provider: { name: 'anthropic', model: 'claude-haiku-4-5' }, allow: ['Custom allow'] }`
	if err := os.WriteFile(filepath.Join(fakeDir, BaseConfigName), []byte(content), 0o644); err != nil {
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
	if len(lr.Config.Allow) != 1 || lr.Config.Allow[0] != "Custom allow" {
		t.Fatalf("unexpected allow: %v", lr.Config.Allow)
	}
	// Deny should be empty (embed defaults not applied because the
	// global config replaces them).
	if len(lr.Config.Deny) != 0 {
		t.Fatalf("expected no deny rules (embed defaults not applied), got %v", lr.Config.Deny)
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
