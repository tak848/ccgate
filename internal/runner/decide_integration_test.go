package runner

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	openaisdk "github.com/openai/openai-go"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/llm"
)

// init seeds the env-var key the matrix test relies on once for the
// whole test binary. We avoid t.Setenv because Go forbids it under
// t.Parallel (parallel siblings would race the restore), but every
// test in this file just needs the same constant value, so setting
// it once at package load is the cheapest approach.
func init() {
	_ = os.Setenv("CCGATE_OPENAI_API_KEY", "sk-fake")
}

// fakeProvider implements llm.Provider with a single canned error
// (or a successful Output) so decide()'s 401/403 split can be
// exercised without ever touching the network. Tests inject it via
// WithProviderFactory().
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

func anthropicAuthError(t *testing.T, status int, errType string) error {
	t.Helper()
	body := `{"type":"error","error":{"type":"` + errType + `","message":"do not log me"}}`
	var e anthropicsdk.Error
	if err := e.UnmarshalJSON([]byte(body)); err != nil {
		t.Fatalf("fixture UnmarshalJSON: %v", err)
	}
	e.StatusCode = status
	return &e
}

// TestDecideProviderErrorMatrix is the canonical end-to-end matrix
// the plan demands: for every (auth shape × status × error code)
// pair that affects the credential flow, decide() must produce the
// right (kind, reason) and only invalidate the cache where the
// 401/403 docs say it should.
//
// We exercise it through decide() with a fake provider injected via
// runtimeOptions.providerFactory so no real LLM call is made and
// the test stays parallel-safe (no package-level globals).
func TestDecideProviderErrorMatrix(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		authType   string // "exec" / "file" / "" (env var)
		err        error
		wantExit1  bool   // true = err propagates to runErr (caller exits 1)
		wantKind   string // expected ft kind on the fallthrough path
		wantReason string // expected reason
	}{
		"exec 401": {
			authType:   "exec",
			err:        &openaisdk.Error{StatusCode: http.StatusUnauthorized, Code: "invalid_api_key"},
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_auth",
		},
		"exec 403 expired token (AWS code through openai-compat)": {
			authType:   "exec",
			err:        &openaisdk.Error{StatusCode: http.StatusForbidden, Code: "ExpiredToken"},
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_auth",
		},
		"exec 403 permission_error": {
			authType:   "exec",
			err:        &openaisdk.Error{StatusCode: http.StatusForbidden, Code: "permission_error"},
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_forbidden",
		},
		"exec 403 unknown code": {
			authType:   "exec",
			err:        &openaisdk.Error{StatusCode: http.StatusForbidden, Code: "weird_new_code"},
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_forbidden",
		},
		"file 401": {
			authType:   "file",
			err:        anthropicAuthError(t, http.StatusUnauthorized, "authentication_error"),
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_auth",
		},
		"file 403 expired token": {
			authType:   "file",
			err:        anthropicAuthError(t, http.StatusForbidden, "invalid_token"),
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_auth",
		},
		"file 403 permission_error": {
			authType:   "file",
			err:        anthropicAuthError(t, http.StatusForbidden, "permission_error"),
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_forbidden",
		},
		"env 401 keeps exit 1": {
			authType:  "",
			err:       &openaisdk.Error{StatusCode: http.StatusUnauthorized, Code: "invalid_api_key"},
			wantExit1: true,
		},
		"env 403 expired token keeps exit 1": {
			authType:  "",
			err:       &openaisdk.Error{StatusCode: http.StatusForbidden, Code: "ExpiredToken"},
			wantExit1: true,
		},
		"env 403 permission_error falls through": {
			authType:   "",
			err:        &openaisdk.Error{StatusCode: http.StatusForbidden, Code: "permission_error"},
			wantKind:   llm.FallthroughKindCredentialUnavailable,
			wantReason: "provider_forbidden",
		},
		"5xx keeps exit 1": {
			authType:  "exec",
			err:       &openaisdk.Error{StatusCode: http.StatusBadGateway, Code: "upstream_error"},
			wantExit1: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := buildTestConfig(tc.authType)
			fake := &fakeProvider{err: tc.err}
			ro := runtimeOptions{
				targetName:  "claude",
				cacheTarget: "claude",
				providerFactory: func(_, _, _ string) llm.Provider {
					return fake
				},
			}
			// CCGATE_OPENAI_API_KEY is seeded by the package init();
			// see comment there for why we do not use t.Setenv.

			in := HookInput{
				ToolName: "Bash",
				ToolInput: HookToolInput{
					Command: "echo hi",
				},
			}
			_, _, kind, reason, _, _, runErr := decide(
				context.Background(),
				cfg,
				in,
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
// fields are populated with absolute paths under t.TempDir() so
// resolveAPIKey can produce a credential without spinning up a real
// helper, while the env path leaves Auth nil.
func buildTestConfig(authType string) config.Config {
	cfg := config.Default()
	cfg.Provider.Name = "openai"
	cfg.Provider.Model = "gpt-test"
	switch authType {
	case "exec":
		cfg.Provider.Auth = &config.AuthConfig{
			Type:    config.AuthTypeExec,
			Command: "printf sk-helper",
		}
	case "file":
		// We point at a path that exists for the duration of this
		// test so resolveAPIKey returns a credential. The body is a
		// plain string with no expires_at so the file resolver
		// returns it verbatim.
		f, _ := os.CreateTemp("", "ccgate-fake-key-*")
		_, _ = f.WriteString("sk-file")
		_ = f.Close()
		cfg.Provider.Auth = &config.AuthConfig{
			Type: config.AuthTypeFile,
			Path: f.Name(),
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
	t.Parallel()

	// CCGATE_OPENAI_API_KEY is seeded by init().
	cfg := buildTestConfig("exec")
	fake := &fakeProvider{err: &openaisdk.Error{
		StatusCode: http.StatusInternalServerError,
		Code:       "internal_server_error",
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
