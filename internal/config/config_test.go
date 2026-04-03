package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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

	got := projectLocalConfigPaths("/tmp/repo/subdir")
	if len(got) != 2 {
		t.Fatalf("unexpected path count: %d", len(got))
	}
	if got[0] != "/tmp/repo/subdir/ccgate.local.jsonnet" {
		t.Fatalf("unexpected first path: %s", got[0])
	}
	if got[1] != "/tmp/repo/subdir/.claude/ccgate.local.jsonnet" {
		t.Fatalf("unexpected second path: %s", got[1])
	}
}

func TestProjectLocalConfigPathsEmpty(t *testing.T) {
	t.Parallel()

	got := projectLocalConfigPaths("")
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestSafeProjectLocalConfigPathsSkipsTrackedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, LocalConfigName), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	gitRun(t, dir, "init")
	gitRun(t, dir, "config", "user.email", "test@test.com")
	gitRun(t, dir, "config", "user.name", "test")
	gitRun(t, dir, "add", "-f", LocalConfigName)

	got := safeProjectLocalConfigPaths(dir)
	if len(got) != 0 {
		t.Fatalf("expected tracked file to be skipped, got %v", got)
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
