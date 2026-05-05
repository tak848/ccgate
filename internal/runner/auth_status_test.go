package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	openaisdk "github.com/openai/openai-go"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/llm"
)

// TestProviderErrorInfo pins the SDK-error type-assertion that the
// runner relies on to extract the secret-free (status, code) pair
// from anthropic-sdk-go and openai-go API errors. Both fields fuel
// the 401 / 403-credential-expired / 403-non-credential split that
// drives whether ccgate invalidates the cache and falls through.
// A future SDK rename of `Error` would silently break this without
// the guard.
func TestProviderErrorInfo(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err        error
		wantOK     bool
		wantStatus int
		wantCode   string
	}{
		"nil":         {err: nil, wantOK: false},
		"plain error": {err: errors.New("network closed"), wantOK: false},

		// Anthropic: code is parsed from RawJSON()'s `error.type`.
		// The fabricated *Error here has empty RawJSON so the code is "".
		"anthropic 401": {err: &anthropicsdk.Error{StatusCode: 401}, wantOK: true, wantStatus: 401},
		"anthropic 403": {err: &anthropicsdk.Error{StatusCode: 403}, wantOK: true, wantStatus: 403},
		"anthropic 429": {err: &anthropicsdk.Error{StatusCode: 429}, wantOK: true, wantStatus: 429},
		"anthropic 500": {err: &anthropicsdk.Error{StatusCode: 500}, wantOK: true, wantStatus: 500},

		// OpenAI: Code / Type are exposed directly. We trust Code first;
		// Type is the fallback when Code is empty.
		"openai 401 code":          {err: &openaisdk.Error{StatusCode: 401, Code: "invalid_api_key"}, wantOK: true, wantStatus: 401, wantCode: "invalid_api_key"},
		"openai 403 type fallback": {err: &openaisdk.Error{StatusCode: 403, Type: "permission_error"}, wantOK: true, wantStatus: 403, wantCode: "permission_error"},
		"openai 502":               {err: &openaisdk.Error{StatusCode: 502}, wantOK: true, wantStatus: 502},

		// errors.As must unwrap fmt.Errorf(%w) wrappers — that's the
		// path real production errors take through llm.* and runner.
		"wrapped anthropic":   {err: fmt.Errorf("wrap: %w", &anthropicsdk.Error{StatusCode: 401}), wantOK: true, wantStatus: 401},
		"wrapped openai code": {err: fmt.Errorf("wrap: %w", &openaisdk.Error{StatusCode: 403, Code: "expired_token"}), wantOK: true, wantStatus: 403, wantCode: "expired_token"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			info, ok := providerErrorInfo(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (info=%+v, err=%v)", ok, tc.wantOK, info, tc.err)
			}
			if !ok {
				return
			}
			if info.status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", info.status, tc.wantStatus)
			}
			if info.code != tc.wantCode {
				t.Fatalf("code = %q, want %q", info.code, tc.wantCode)
			}
		})
	}
}

// TestProviderErrorInfoAnthropicRawJSON exercises the RawJSON()
// extraction path. anthropic-sdk-go v1.37.0 does not expose Type on
// the public Error struct, so the runner has to parse RawJSON()
// for `error.type`. We build the *anthropic.Error via the SDK's
// UnmarshalJSON because JSON.raw is unexported (composite literal
// would not compile) — the same shape a fake provider would use.
func TestProviderErrorInfoAnthropicRawJSON(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body     string
		status   int
		wantCode string
	}{
		"authentication_error": {
			body:     `{"type":"error","error":{"type":"authentication_error","message":"sk-secret"}}`,
			status:   401,
			wantCode: "authentication_error",
		},
		"permission_error": {
			body:     `{"type":"error","error":{"type":"permission_error","message":"forbidden"}}`,
			status:   403,
			wantCode: "permission_error",
		},
		"malformed body": {
			body:     `{not json`,
			status:   500,
			wantCode: "",
		},
		"missing error.type": {
			body:     `{"type":"error","error":{"message":"oops"}}`,
			status:   429,
			wantCode: "",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var e anthropicsdk.Error
			if tc.body != "" {
				// Best-effort: malformed JSON returns a SDK error we
				// ignore; the test only cares about the resulting
				// (RawJSON, StatusCode) pair feeding providerErrorInfo.
				_ = e.UnmarshalJSON([]byte(tc.body))
			}
			e.StatusCode = tc.status
			info, ok := providerErrorInfo(&e)
			if !ok {
				t.Fatalf("providerErrorInfo returned ok=false for %q", tc.body)
			}
			if info.status != tc.status {
				t.Fatalf("status = %d, want %d", info.status, tc.status)
			}
			if info.code != tc.wantCode {
				t.Fatalf("code = %q, want %q", info.code, tc.wantCode)
			}
			// Defence in depth: `message` content must not have
			// leaked into the secret-free code.
			if strings.Contains(info.code, "sk-secret") || strings.Contains(info.code, "oops") {
				t.Fatalf("code %q contains body content; sanitization regressed", info.code)
			}
		})
	}
}

// TestIsCredentialExpiredCode pins the AWS / OAuth / Anthropic /
// OpenAI 403 error codes that ccgate promotes to provider_auth (so
// the cache gets invalidated for auth.type=exec and the next fire
// produces a fresh credential). Match is case-insensitive so AWS
// PascalCase and proxy snake_case both work.
func TestIsCredentialExpiredCode(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		code string
		want bool
	}{
		"empty":                     {"", false},
		"unknown":                   {"network_unreachable", false},
		"AWS ExpiredToken":          {"ExpiredToken", true},
		"AWS ExpiredTokenException": {"ExpiredTokenException", true},
		"AWS InvalidClientTokenId":  {"InvalidClientTokenId", true},
		"AWS UnrecognizedClient":    {"UnrecognizedClientException", true},
		"AWS lower":                 {"expiredtoken", true},
		"OAuth invalid_token":       {"invalid_token", true},
		"OAuth expired_token":       {"expired_token", true},
		"Anthropic auth_error":      {"authentication_error", true},
		"Anthropic invalid_api_key": {"invalid_api_key", true},
		"OpenAI Type fallback case": {"AUTHENTICATION_ERROR", true},
		"non-credential 403":        {"permission_error", false},
		"AWS access denied":         {"AccessDenied", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := isCredentialExpiredCode(tc.code)
			if got != tc.want {
				t.Fatalf("isCredentialExpiredCode(%q) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// TestRedactProviderError pins the contract that the SDK error
// string never reaches the runner's log / metrics surface verbatim.
// Both anthropic-sdk-go and openai-go embed the response body in
// Error.Error(), and proxies sometimes echo Authorization headers /
// request signatures there.
func TestRedactProviderError(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err          error
		wantContains string
		wantOmits    string
	}{
		"anthropic 500": {
			// Real anthropic-sdk-go errors render with "POST 'url': 500 ..."
			// and the response body. We can't easily fabricate that body
			// here, but we can verify the redacted message no longer
			// contains the long, body-bearing prefix.
			err:          &anthropicsdk.Error{StatusCode: 500},
			wantContains: "anthropic API error (status 500)",
		},
		"openai 502": {
			err:          &openaisdk.Error{StatusCode: 502},
			wantContains: "openai API error (status 502)",
		},
		"non-sdk error passthrough": {
			err:          errors.New("network read timed out"),
			wantContains: "network read timed out",
		},
		"nil": {err: nil, wantContains: ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			provider := "anthropic"
			if strings.Contains(name, "openai") {
				provider = "openai"
			}
			got := redactProviderError(provider, tc.err)
			if tc.err == nil {
				if got != nil {
					t.Fatalf("nil err must redact to nil, got %v", got)
				}
				return
			}
			if !strings.Contains(got.Error(), tc.wantContains) {
				t.Fatalf("redacted = %q, want substring %q", got.Error(), tc.wantContains)
			}
		})
	}
}

// TestResolveAPIKeyUnknownProviderShortCircuit guards the Blocker
// from the holistic review: a typo'd provider.name plus a configured
// api_key_command must NOT run the helper, because newProviderClient
// would otherwise default to the Anthropic SDK and credentials minted
// for a different provider would be sent to the wrong API.
func TestResolveAPIKeyUnknownProviderShortCircuit(t *testing.T) {
	t.Parallel()

	// We deliberately point auth.command at a script that would
	// fail loudly (`exit 17`) so the test will catch any regression
	// where the unknown-provider guard is removed and the helper
	// actually runs.
	cfg := config.ProviderConfig{
		Name:  "opena1", // deliberate typo
		Model: "x",
		Auth: &config.AuthConfig{
			Type:          "exec",
			Command:       "exit 17",
			RefreshMargin: "30s",
			Timeout:       "5s",
		},
	}
	key, kind, reason, source, err := resolveAPIKey(context.Background(), cfg, "opena1", "claude")
	if err != nil {
		t.Fatalf("err = %v, want nil (unknown provider must short-circuit silently)", err)
	}
	if key != "" {
		t.Fatalf("key = %q, want empty (helper must not run)", key)
	}
	if kind != llm.FallthroughKindUnknownProvider {
		t.Fatalf("kind = %q, want %q", kind, llm.FallthroughKindUnknownProvider)
	}
	if reason != "" || source != "" {
		t.Fatalf("reason/source = %q/%q, want empty (no helper attempt)", reason, source)
	}
}
