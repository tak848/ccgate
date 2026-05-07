package anthropic

import (
	"context"
	"encoding/json"
	"errors"
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
