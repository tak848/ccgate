package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	anthropicconfig "github.com/anthropics/anthropic-sdk-go/config"

	"github.com/tak848/ccgate/internal/llm"
)

// fixtureProfileConfig writes a minimal user_oauth profile config at
// $ANTHROPIC_CONFIG_DIR/configs/<profile>.json. Returns the resolved
// credentials path so the caller can choose whether or not to populate
// it.
func fixtureProfileConfig(t *testing.T, profile, baseURL string) string {
	t.Helper()
	dir := os.Getenv("ANTHROPIC_CONFIG_DIR")
	if dir == "" {
		t.Fatalf("ANTHROPIC_CONFIG_DIR must be set before fixtureProfileConfig")
	}
	cfg := &anthropicconfig.Config{
		BaseURL: baseURL,
		AuthenticationInfo: &anthropicconfig.AuthenticationInfo{
			Type:      anthropicconfig.AuthenticationTypeUserOAuth,
			UserOAuth: &anthropicconfig.UserOAuth{ClientID: "test-client"},
		},
	}
	if err := anthropicconfig.SaveProfile(dir, profile, cfg); err != nil {
		t.Fatalf("SaveProfile %q: %v", profile, err)
	}
	return anthropicconfig.ProfileCredentialsPath(dir, profile)
}

// writeCredentials publishes a fake credentials file at path. The
// access token embeds the profile name so the mock server can pin
// "the right profile's token reached us".
func writeCredentials(t *testing.T, path, profile string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir credentials parent: %v", err)
	}
	exp := time.Now().Add(time.Hour)
	creds := anthropicconfig.Credentials{
		AccessToken:  "fake-access-" + profile,
		RefreshToken: "fake-refresh-" + profile,
		ExpiresAt:    &exp,
	}
	if err := anthropicconfig.WriteCredentials(path, creds); err != nil {
		t.Fatalf("WriteCredentials: %v", err)
	}
}

// unsetEnvForTest removes an env var for the duration of the test,
// restoring any prior value on cleanup. t.Setenv cannot express
// "unset", and an exported-empty value is not equivalent (e.g. the SDK
// treats ANTHROPIC_PROFILE="" as an explicit empty profile name).
func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		}
	})
}

// setActiveConfig writes the active_config pointer used by
// LoadConfig() (empty Profile path).
func setActiveConfig(t *testing.T, profile string) {
	t.Helper()
	dir := os.Getenv("ANTHROPIC_CONFIG_DIR")
	if dir == "" {
		t.Fatalf("ANTHROPIC_CONFIG_DIR must be set")
	}
	if err := anthropicconfig.SetActiveProfile(dir, profile); err != nil {
		t.Fatalf("SetActiveProfile: %v", err)
	}
}

// newMockAnthropicServer stands up an httptest server that pretends
// to be /v1/messages. Returns the server (caller is responsible for
// Close) and an int32 atomic counter of received requests so tests
// can assert call counts.
func newMockAnthropicServer(t *testing.T, wantBearer string, allow bool) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if wantBearer != "" {
			got := r.Header.Get("Authorization")
			if got != "Bearer "+wantBearer {
				t.Errorf("Authorization header = %q, want %q", got, "Bearer "+wantBearer)
				http.Error(w, "wrong bearer", http.StatusUnauthorized)
				return
			}
		}
		body := `{"behavior": "allow"}`
		if !allow {
			body = `{"behavior": "deny", "deny_message": "test deny"}`
		}
		resp := map[string]any{
			"id":    "msg_test",
			"type":  "message",
			"role":  "assistant",
			"model": "claude-test",
			"content": []map[string]any{
				{"type": "text", "text": body},
			},
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens":  10,
				"output_tokens": 3,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// classificationPrompt builds the prompt shape Decide expects.
func classificationPrompt(model string, timeoutMS int) llm.Prompt {
	return llm.Prompt{
		Model:     model,
		System:    "you are a permission gate",
		User:      `{"tool_name":"Bash"}`,
		TimeoutMS: timeoutMS,
	}
}

// TestProfileLoadPaths covers the happy-path resolutions: named
// profile, default profile via active_config, env-shadow defense,
// base_url override, and ANTHROPIC_PROFILE bypass for named profiles.
//
// Cannot run t.Parallel — every sub-test mutates process env via
// t.Setenv (ANTHROPIC_CONFIG_DIR, ANTHROPIC_API_KEY, ANTHROPIC_PROFILE).
func TestProfileLoadPaths(t *testing.T) {
	cases := map[string]struct {
		profile        string
		clientProfile  string
		envProfile     string // value of ANTHROPIC_PROFILE during test
		envAPIKey      string // value of ANTHROPIC_API_KEY during test
		profileBaseURL string // base_url written into the profile config
		clientBaseURL  bool   // when true, override server URL via Client.BaseURL
	}{
		"named profile (LoadProfile)":              {profile: "work", clientProfile: "work"},
		"default profile via active_config":        {profile: "default", clientProfile: "", envProfile: ""},
		"env shadow defended (ANTHROPIC_API_KEY)":  {profile: "work", clientProfile: "work", envAPIKey: "sk-ant-shadow"},
		"client base_url overrides profile cfg":    {profile: "work", clientProfile: "work", profileBaseURL: "https://should-be-overridden.example", clientBaseURL: true},
		"named profile bypasses ANTHROPIC_PROFILE": {profile: "work", clientProfile: "work", envProfile: "other"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
			if tc.envAPIKey != "" {
				t.Setenv("ANTHROPIC_API_KEY", tc.envAPIKey)
			}
			if tc.envProfile != "" {
				t.Setenv("ANTHROPIC_PROFILE", tc.envProfile)
			}
			srv, calls := newMockAnthropicServer(t, "fake-access-"+tc.profile, true)
			profileBaseURL := srv.URL
			if tc.profileBaseURL != "" {
				profileBaseURL = tc.profileBaseURL
			}
			credPath := fixtureProfileConfig(t, tc.profile, profileBaseURL)
			writeCredentials(t, credPath, tc.profile)
			if tc.clientProfile == "" {
				setActiveConfig(t, tc.profile)
			}
			c := &Client{UseProfile: true, Profile: tc.clientProfile}
			if tc.clientBaseURL {
				c.BaseURL = srv.URL
			}
			res, err := c.Decide(context.Background(), classificationPrompt("claude-test", 5_000))
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}
			if res.Output.Behavior != llm.BehaviorAllow {
				t.Fatalf("behavior = %q, want allow", res.Output.Behavior)
			}
			if got := atomic.LoadInt32(calls); got != 1 {
				t.Fatalf("server calls = %d, want 1", got)
			}
		})
	}
}

// TestNonProfileSuppressesEnvDefaults proves that the WithAPIKey
// (exec / file / *_API_KEY) path passes option.WithoutEnvironmentDefaults
// so the SDK's env autoload never runs. Without it, an empty/unset
// ANTHROPIC_API_KEY makes the SDK fall through to an on-disk fallback
// profile (active_config / "default"), which our explicit key then
// shadows — emitting the misleading "ANTHROPIC_API_KEY is set ... takes
// precedence over the profile" warning. The explicit key is used either
// way (it shadows the profile), so the only observable signal of the
// fix is the absence of that warning. This is the only ccgate test that
// exercises the autoload-shadow path, so the SDK's once-per-process
// warn dedupe is pristine when it runs.
//
// Cannot run t.Parallel — mutates process env via t.Setenv.
func TestNonProfileSuppressesEnvDefaults(t *testing.T) {
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
	// The user's exact scenario: ANTHROPIC_API_KEY exported but empty, so
	// the SDK skips it and falls through to the on-disk fallback profile.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	// ANTHROPIC_PROFILE must be UNSET (not empty): an exported-empty value
	// makes LoadConfig error with "profile name is empty" instead of
	// falling through to active_config / the literal "default" profile,
	// which would defeat the autoload this test needs to provoke.
	unsetEnvForTest(t, "ANTHROPIC_PROFILE")

	// Stage a fallback "default" profile the SDK would autoload if env
	// defaults were not suppressed.
	credPath := fixtureProfileConfig(t, "default", "https://should-not-be-used.example")
	writeCredentials(t, credPath, "default")
	setActiveConfig(t, "default")

	var gotAuth, gotAPIKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_test", "type": "message", "role": "assistant", "model": "claude-test",
			"content":     []map[string]any{{"type": "text", "text": `{"behavior":"allow"}`}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	t.Cleanup(srv.Close)

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	c := &Client{APIKey: "sk-ant-explicit", BaseURL: srv.URL}
	if _, err := c.Decide(context.Background(), classificationPrompt("claude-test", 5_000)); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	if gotAPIKey != "sk-ant-explicit" {
		t.Errorf("X-Api-Key = %q, want sk-ant-explicit", gotAPIKey)
	}
	if gotAuth != "" {
		t.Errorf("autoloaded profile credential leaked: Authorization = %q", gotAuth)
	}
	if out := logBuf.String(); strings.Contains(out, "takes precedence over the profile") {
		t.Errorf("env defaults not suppressed: SDK emitted shadow warning: %q", out)
	}
}

// TestProfileLoadFailures groups the credential_unavailable paths
// where ccgate detects the failure before the SDK runs:
// credentials missing, profile name validation, and profile config
// absent / parse / invalid.
func TestProfileLoadFailures(t *testing.T) {
	cases := map[string]struct {
		profile string
		setup   func(t *testing.T) // populates tmp config dir
		want    string             // ErrProfileUnavailable wrap suffix
	}{
		"credentials missing": {
			profile: "work",
			setup: func(t *testing.T) {
				path := fixtureProfileConfig(t, "work", "https://api.anthropic.com")
				_ = path // intentionally not writing credentials
			},
			want: "credentials missing (preflight)",
		},
		"profile name validate (slash)": {
			profile: "bad/slash",
			setup:   func(t *testing.T) {},
			want:    "load profile",
		},
		"profile config missing": {
			profile: "ghost",
			setup:   func(t *testing.T) {},
			want:    "load profile",
		},
		"profile config parse error": {
			profile: "broken",
			setup: func(t *testing.T) {
				dir := os.Getenv("ANTHROPIC_CONFIG_DIR")
				path := filepath.Join(dir, "configs", "broken.json")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
					t.Fatalf("write broken: %v", err)
				}
			},
			want: "load profile",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
			tc.setup(t)
			c := &Client{UseProfile: true, Profile: tc.profile}
			_, err := c.Decide(context.Background(), classificationPrompt("claude-test", 5_000))
			if !errors.Is(err, ErrProfileUnavailable) {
				t.Fatalf("err = %v, want ErrProfileUnavailable wrap", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err message %q does not contain %q", err.Error(), tc.want)
			}
		})
	}
}

// TestPreflightStatFailure exercises the credentials_stat_failed
// path: the credentials file is not "missing" (fs.ErrNotExist), the
// stat just fails. We trigger this by making the credentials parent
// dir un-readable so os.Stat returns permission denied.
//
// Skipped when running as root (chmod 0 cannot keep root out) and on
// Windows (chmod 0 has no equivalent effect on the NTFS DACL ladder
// the test relies on).
func TestPreflightStatFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod 0 does not produce stat failure on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("permission test cannot run as root")
	}
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
	credPath := fixtureProfileConfig(t, "work", "https://api.anthropic.com")
	parent := filepath.Dir(credPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatalf("mkdir credentials parent: %v", err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatalf("chmod 0: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	c := &Client{UseProfile: true, Profile: "work"}
	_, err := c.Decide(context.Background(), classificationPrompt("claude-test", 5_000))
	if !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("err = %v, want ErrProfileUnavailable wrap", err)
	}
	if !strings.Contains(err.Error(), "credentials stat failed") {
		t.Fatalf("err message = %q, want credentials stat failed", err.Error())
	}
}
