//go:build unix

// The exec-helper script in this matrix is a `printf` one-liner
// that depends on a POSIX shell. Limit the test to Unix builds so
// the Windows runner does not try to run the same script through
// pwsh and trip on POSIX-shell syntax. The keystore code itself
// works on both platforms; only the test fixture is Unix-shaped.
package runner

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	openaisdk "github.com/openai/openai-go"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/llm"
)

// fakeProvider implements llm.Provider with a single canned error
// (or a successful Output) so decide()'s 401/403 handling can be
// exercised without ever touching the network. Tests inject it via
// runtimeOptions.providerFactory.
type fakeProvider struct {
	err error
	out llm.Output
}

func (f *fakeProvider) Decide(_ context.Context, _ llm.Prompt) (llm.Result, error) {
	if f.err != nil {
		return llm.Result{}, f.err
	}
	return llm.Result{Output: f.out}, nil
}

// TestDecideProviderErrorMatrix exercises decide() across every
// (auth shape × HTTP status) pair we need to lock in. We do NOT
// call t.Parallel() on this test (or its subtests): the env-path
// case requires CCGATE_OPENAI_API_KEY to be present, and Go forbids
// using t.Setenv from a parallel test (siblings would race the
// restore). Sequential is fine — the matrix is fast.
func TestDecideProviderErrorMatrix(t *testing.T) {
	t.Setenv("CCGATE_OPENAI_API_KEY", "sk-fake")

	cases := map[string]struct {
		authType   string // "exec" / "file" / "" (env var)
		err        error
		wantExit1  bool   // true = err propagates to runErr (caller exits 1)
		wantKind   string // expected ft kind on the fallthrough path
		wantReason string // expected reason
	}{
		"exec 401": {
			authType:   "exec",
			err:        &openaisdk.Error{StatusCode: http.StatusUnauthorized},
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_auth",
		},
		"exec 403": {
			authType:   "exec",
			err:        &openaisdk.Error{StatusCode: http.StatusForbidden},
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_auth",
		},
		"file 401": {
			authType:   "file",
			err:        &openaisdk.Error{StatusCode: http.StatusUnauthorized},
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_auth",
		},
		"file 403": {
			authType:   "file",
			err:        &openaisdk.Error{StatusCode: http.StatusForbidden},
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_auth",
		},
		"env 401 keeps exit 1": {
			authType:  "",
			err:       &openaisdk.Error{StatusCode: http.StatusUnauthorized},
			wantExit1: true,
		},
		"env 403 keeps exit 1": {
			authType:  "",
			err:       &openaisdk.Error{StatusCode: http.StatusForbidden},
			wantExit1: true,
		},
		"5xx keeps exit 1": {
			authType:  "exec",
			err:       &openaisdk.Error{StatusCode: http.StatusBadGateway},
			wantExit1: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := buildTestConfig(t, tc.authType)
			fake := &fakeProvider{err: tc.err}
			ro := runtimeOptions{
				targetName:  "claude",
				cacheTarget: "claude",
				providerFactory: func(_, _, _ string) llm.Provider {
					return fake
				},
			}
			_, _, kind, reason, _, _, runErr := decide(
				context.Background(),
				cfg,
				HookInput{ToolName: "Bash", ToolInput: HookToolInput{Command: "echo hi"}},
				ro,
			)
			if tc.wantExit1 {
				if runErr == nil {
					t.Fatalf("expected runErr (exit 1 path), got kind=%q reason=%q", kind, reason)
				}
				return
			}
			if runErr != nil {
				t.Fatalf("unexpected runErr: %v (kind=%q reason=%q)", runErr, kind, reason)
			}
			if kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", kind, tc.wantKind)
			}
			if reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

// buildTestConfig assembles a minimal config.Config that exercises
// the requested auth shape. For type=exec / type=file the auth
// fields are populated so resolveAPIKey can produce a credential
// without spinning up a real helper; the env path leaves Auth nil
// and relies on the test setting CCGATE_OPENAI_API_KEY itself.
func buildTestConfig(t *testing.T, authType string) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Provider.Name = "openai"
	cfg.Provider.Model = "gpt-test"
	switch authType {
	case "exec":
		// Point the keystore cache at a tempdir so the test does not
		// touch the developer's real $HOME/.cache. t.Setenv requires
		// the test to be non-parallel, which the matrix already is.
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
		cfg.Provider.Auth = &config.AuthConfig{
			Type:    config.AuthTypeExec,
			Command: "printf sk-helper",
		}
	case "file":
		// File body is a plain string with no expires_at so the
		// file resolver returns it verbatim. t.TempDir cleans up
		// for us so we don't leak fake-credential files into /tmp.
		path := filepath.Join(t.TempDir(), "key")
		if err := os.WriteFile(path, []byte("sk-file"), 0o600); err != nil {
			t.Fatalf("write fake key: %v", err)
		}
		cfg.Provider.Auth = &config.AuthConfig{
			Type: config.AuthTypeFile,
			Path: &path,
		}
	}
	return cfg
}

// TestDecideRedactsRawErrorBody guards the contract that
// redactProviderError strips the SDK Error's response body before
// the error reaches runErr (which the caller logs and writes to
// metrics.Entry.Error). Both anthropic-sdk-go and openai-go embed
// the body in Error.Error(), and a misbehaving proxy could echo a
// credential there.
func TestDecideRedactsRawErrorBody(t *testing.T) {
	t.Setenv("CCGATE_OPENAI_API_KEY", "sk-fake")

	cfg := buildTestConfig(t, "exec")
	fake := &fakeProvider{err: &openaisdk.Error{
		StatusCode: http.StatusInternalServerError,
	}}
	ro := runtimeOptions{
		targetName:  "claude",
		cacheTarget: "claude",
		providerFactory: func(_, _, _ string) llm.Provider {
			return fake
		},
	}
	_, _, _, _, _, _, runErr := decide(
		context.Background(),
		cfg,
		HookInput{ToolName: "Bash", ToolInput: HookToolInput{Command: "echo"}},
		ro,
	)
	if runErr == nil {
		t.Fatal("expected exit-1 path on 5xx, got nil")
	}
	if !strings.Contains(runErr.Error(), "API error (status 500)") {
		t.Fatalf("redacted error %q must include the short summary", runErr.Error())
	}
}
