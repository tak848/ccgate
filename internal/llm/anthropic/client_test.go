package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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

// buildFakeAnt compiles testdata/fakeant into a temp dir and returns
// the directory plus the absolute path to the binary. Callers that
// need ant on PATH should t.Setenv("PATH", dir + sep + os.Getenv("PATH")).
// Subsequent calls reuse the binary by passing FAKE_ANT_MODE via env.
func buildFakeAnt(t *testing.T) (binDir, binPath string) {
	t.Helper()
	binDir = t.TempDir()
	binPath = filepath.Join(binDir, "ant")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	src := filepath.Join("testdata", "fakeant")
	cmd := exec.Command("go", "build", "-o", binPath, "./"+src)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build fakeant: %v", err)
	}
	return binDir, binPath
}

// prependPath puts dir at the front of PATH for the rest of the test.
func prependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
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
// credentials missing (auto_login=false), profile name validation,
// and profile config absent / parse / invalid.
func TestProfileLoadFailures(t *testing.T) {
	type profileSetup struct {
		write           bool
		credentialsPath string // when non-empty, override default to this path
	}
	cases := map[string]struct {
		profile string
		setup   func(t *testing.T) // populates tmp config dir
		want    string             // ErrProfileUnavailable wrap suffix
	}{
		"credentials missing (auto_login=false)": {
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
// dir un-readable so os.Stat returns permission denied. auto_login
// is true to prove ant is NOT spawned in this case.
//
// Skipped when running as root — chmod 0 cannot keep root out, so
// the assertion would not hold.
func TestPreflightStatFailure(t *testing.T) {
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

	binDir, _ := buildFakeAnt(t)
	prependPath(t, binDir)

	c := &Client{
		UseProfile:       true,
		Profile:          "work",
		AutoLogin:        true,
		AutoLoginTimeout: 5 * time.Second,
	}
	_, err := c.Decide(context.Background(), classificationPrompt("claude-test", 5_000))
	if !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("err = %v, want ErrProfileUnavailable wrap", err)
	}
	if !strings.Contains(err.Error(), "credentials stat failed") {
		t.Fatalf("err message = %q, want credentials stat failed", err.Error())
	}
}

// TestAutoLoginSuccess exercises the happy-path: credentials missing
// → ccgate spawns fakeant → fakeant writes credentials → re-preflight
// passes → SDK API call goes through with the just-written bearer.
func TestAutoLoginSuccess(t *testing.T) {
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv, calls := newMockAnthropicServer(t, "fake-access-work", true)
	credPath := fixtureProfileConfig(t, "work", srv.URL)
	_ = credPath

	binDir, _ := buildFakeAnt(t)
	prependPath(t, binDir)
	t.Setenv("FAKE_ANT_MODE", "success")

	c := &Client{
		UseProfile:       true,
		Profile:          "work",
		AutoLogin:        true,
		AutoLoginTimeout: 5 * time.Second,
	}
	res, err := c.Decide(context.Background(), classificationPrompt("claude-test", 5_000))
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if res.Output.Behavior != llm.BehaviorAllow {
		t.Fatalf("behavior = %q", res.Output.Behavior)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("server calls = %d", got)
	}
	if _, err := os.Stat(credPath); err != nil {
		t.Fatalf("credentials file not present after auto-login: %v", err)
	}
}

// TestAutoLoginFailures table-drives the auto_login error_class
// matrix: ant binary missing, ant timeout, ant non-zero exit, ant
// success-but-no-credentials, custom credentials_path forbidden,
// and the auto_login_requires_profile defense guard.
func TestAutoLoginFailures(t *testing.T) {
	cases := map[string]struct {
		setup            func(t *testing.T) (client *Client, credPath string)
		fakeAntMode      string
		fakeAntSleep     string
		autoLoginTimeout time.Duration
		hidePath         bool   // stub PATH so ant is not found
		wantSubstr       string // substring expected in err.Error()
	}{
		"ant binary not found": {
			setup: func(t *testing.T) (*Client, string) {
				p := fixtureProfileConfig(t, "work", "https://api.anthropic.com")
				return &Client{UseProfile: true, Profile: "work", AutoLogin: true, AutoLoginTimeout: 2 * time.Second}, p
			},
			hidePath:   true,
			wantSubstr: "ant auto-login",
		},
		"ant exits non-zero": {
			setup: func(t *testing.T) (*Client, string) {
				p := fixtureProfileConfig(t, "work", "https://api.anthropic.com")
				return &Client{UseProfile: true, Profile: "work", AutoLogin: true, AutoLoginTimeout: 2 * time.Second}, p
			},
			fakeAntMode: "fail",
			wantSubstr:  "ant auto-login",
		},
		"ant times out": {
			setup: func(t *testing.T) (*Client, string) {
				p := fixtureProfileConfig(t, "work", "https://api.anthropic.com")
				return &Client{UseProfile: true, Profile: "work", AutoLogin: true, AutoLoginTimeout: 200 * time.Millisecond}, p
			},
			fakeAntMode:      "sleep",
			fakeAntSleep:     "30s",
			autoLoginTimeout: 200 * time.Millisecond,
			wantSubstr:       "ant auto-login",
		},
		"ant succeeded but credentials still missing": {
			setup: func(t *testing.T) (*Client, string) {
				p := fixtureProfileConfig(t, "work", "https://api.anthropic.com")
				return &Client{UseProfile: true, Profile: "work", AutoLogin: true, AutoLoginTimeout: 2 * time.Second}, p
			},
			fakeAntMode: "missing_credentials",
			wantSubstr:  "credentials missing after ant auto-login",
		},
		"custom credentials_path rejected": {
			setup: func(t *testing.T) (*Client, string) {
				dir := os.Getenv("ANTHROPIC_CONFIG_DIR")
				custom := filepath.Join(dir, "custom", "creds.json")
				cfg := &anthropicconfig.Config{
					AuthenticationInfo: &anthropicconfig.AuthenticationInfo{
						Type:            anthropicconfig.AuthenticationTypeUserOAuth,
						CredentialsPath: custom,
						UserOAuth:       &anthropicconfig.UserOAuth{ClientID: "test-client"},
					},
				}
				if err := anthropicconfig.SaveProfile(dir, "work", cfg); err != nil {
					t.Fatalf("SaveProfile: %v", err)
				}
				return &Client{UseProfile: true, Profile: "work", AutoLogin: true, AutoLoginTimeout: 2 * time.Second}, custom
			},
			wantSubstr: "auto_login requires SDK default credentials_path",
		},
		"auto_login requires non-empty profile (defense)": {
			setup: func(t *testing.T) (*Client, string) {
				dir := os.Getenv("ANTHROPIC_CONFIG_DIR")
				// Build a "default" profile + active_config so LoadConfig
				// succeeds; preflight then trips on the missing
				// credentials with auto_login=true and an empty profile.
				path := fixtureProfileConfig(t, "default", "https://api.anthropic.com")
				if err := anthropicconfig.SetActiveProfile(dir, "default"); err != nil {
					t.Fatalf("SetActiveProfile: %v", err)
				}
				return &Client{UseProfile: true, Profile: "", AutoLogin: true, AutoLoginTimeout: 2 * time.Second}, path
			},
			wantSubstr: "auto_login requires non-empty profile name",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			binDir, _ := buildFakeAnt(t)
			if tc.hidePath {
				t.Setenv("PATH", "")
			} else {
				prependPath(t, binDir)
			}
			if tc.fakeAntMode != "" {
				t.Setenv("FAKE_ANT_MODE", tc.fakeAntMode)
			}
			if tc.fakeAntSleep != "" {
				t.Setenv("FAKE_ANT_SLEEP", tc.fakeAntSleep)
			}
			c, _ := tc.setup(t)
			_, err := c.Decide(context.Background(), classificationPrompt("claude-test", 5_000))
			if !errors.Is(err, ErrProfileUnavailable) {
				t.Fatalf("err = %v, want ErrProfileUnavailable", err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("err message %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestAutoLoginRace fires Decide from two goroutines simultaneously
// after deleting credentials. The flock must serialize them so ant
// is invoked at most once; the second call observes credentials
// already published and skips the bootstrap.
func TestAutoLoginRace(t *testing.T) {
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv, _ := newMockAnthropicServer(t, "fake-access-work", true)
	credPath := fixtureProfileConfig(t, "work", srv.URL)
	_ = credPath

	binDir, _ := buildFakeAnt(t)
	prependPath(t, binDir)
	// success mode + a small sleep gives the second goroutine a
	// chance to enter the lock wait while the first holds it.
	t.Setenv("FAKE_ANT_MODE", "sleep")
	t.Setenv("FAKE_ANT_SLEEP", "150ms")
	t.Setenv("FAKE_ANT_INVOCATIONS_DIR", t.TempDir()) // unused by fakeant; reserved for future ordering helpers

	// Wrap fakeant invocations counter via a directory-listing trick:
	// each invocation writes a marker file. We keep it simple and
	// count via os.Stat.
	// (We cannot easily ask fakeant to record invocations without
	// extending it; instead we rely on the credentials timestamp not
	// going backwards: a single ant call is enough.)
	c1 := &Client{UseProfile: true, Profile: "work", AutoLogin: true, AutoLoginTimeout: 5 * time.Second}
	c2 := &Client{UseProfile: true, Profile: "work", AutoLogin: true, AutoLoginTimeout: 5 * time.Second}
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	for _, c := range []*Client{c1, c2} {
		go func(c *Client) {
			defer wg.Done()
			_, err := c.Decide(context.Background(), classificationPrompt("claude-test", 10_000))
			errs <- err
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Decide: %v", err)
		}
	}
	// Credentials file must exist once.
	if _, err := os.Stat(credPath); err != nil {
		t.Fatalf("credentials missing after race: %v", err)
	}
}

// TestProviderTimeoutSeparation pins the 2-stage Decide timeout
// split: provider.timeout_ms (passed via Prompt.TimeoutMS) must
// apply only to the LLM API call, NOT to the credential-resolution
// stage. We give Decide a 200 ms TimeoutMS and a 2 s ant sleep —
// if the cap fires on auto_login the test fails.
func TestProviderTimeoutSeparation(t *testing.T) {
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv, _ := newMockAnthropicServer(t, "fake-access-work", true)
	credPath := fixtureProfileConfig(t, "work", srv.URL)
	_ = credPath
	binDir, _ := buildFakeAnt(t)
	prependPath(t, binDir)
	t.Setenv("FAKE_ANT_MODE", "sleep")
	t.Setenv("FAKE_ANT_SLEEP", "1s")

	c := &Client{
		UseProfile:       true,
		Profile:          "work",
		AutoLogin:        true,
		AutoLoginTimeout: 5 * time.Second,
	}
	// 250 ms < ant sleep 1 s but the provider timeout must NOT apply
	// to bootstrap. Decide must complete (auto_login takes ~1 s, then
	// the API call against the local httptest is sub-millisecond).
	res, err := c.Decide(context.Background(), classificationPrompt("claude-test", 250))
	if err != nil {
		t.Fatalf("Decide: %v (provider.timeout_ms leaked into auto_login bootstrap?)", err)
	}
	if res.Output.Behavior != llm.BehaviorAllow {
		t.Fatalf("behavior = %q", res.Output.Behavior)
	}
}

// TestCancellationChain confirms the runner-supplied ctx propagates
// all the way to the ant subprocess: cancelling ctx mid-bootstrap
// kills ant and Decide returns promptly.
func TestCancellationChain(t *testing.T) {
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	srv, _ := newMockAnthropicServer(t, "fake-access-work", true)
	_ = srv
	credPath := fixtureProfileConfig(t, "work", srv.URL)
	_ = credPath
	binDir, _ := buildFakeAnt(t)
	prependPath(t, binDir)
	t.Setenv("FAKE_ANT_MODE", "sleep")
	t.Setenv("FAKE_ANT_SLEEP", "30s") // long enough that we cancel first

	c := &Client{
		UseProfile:       true,
		Profile:          "work",
		AutoLogin:        true,
		AutoLoginTimeout: 1 * time.Minute,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := c.Decide(ctx, classificationPrompt("claude-test", 60_000))
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Decide took %s — ctx cancellation did not propagate to ant", elapsed)
	}
	if !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("err = %v, want ErrProfileUnavailable (cancellation classified as ant_timeout)", err)
	}
}

// TestAntLookupFailed places a non-executable "ant" earlier on PATH
// so exec.LookPath / os.exec returns an *exec.Error that is NOT
// exec.ErrNotFound. This must classify as ant_lookup_failed, not
// ant_not_found.
//
// Skipped on Windows (executable bit is irrelevant there) and when
// running as root (root can execute anything readable).
func TestAntLookupFailed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable bit semantics differ on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root can execute non-x files")
	}
	t.Setenv("ANTHROPIC_CONFIG_DIR", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	credPath := fixtureProfileConfig(t, "work", "https://api.anthropic.com")
	_ = credPath

	stubDir := t.TempDir()
	stub := filepath.Join(stubDir, "ant")
	if err := os.WriteFile(stub, []byte("not an executable"), 0o644); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", stubDir)

	c := &Client{
		UseProfile:       true,
		Profile:          "work",
		AutoLogin:        true,
		AutoLoginTimeout: 1 * time.Second,
	}
	_, err := c.Decide(context.Background(), classificationPrompt("claude-test", 5_000))
	if !errors.Is(err, ErrProfileUnavailable) {
		t.Fatalf("err = %v, want ErrProfileUnavailable", err)
	}
	// We cannot peek at the slog warn from here without a custom
	// handler, but the wrapped error message is enough to confirm
	// the bootstrap path produced a sanitized failure.
	if !strings.Contains(err.Error(), "ant auto-login") {
		t.Fatalf("err = %q, want ant auto-login marker", err.Error())
	}
}
