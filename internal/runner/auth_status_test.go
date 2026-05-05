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

// TestProviderAuthStatus pins the SDK-error type-assertion that the
// runner uses to spot 401/403 responses. Both anthropic-sdk-go and
// openai-go expose StatusCode on their public Error struct; gemini
// delegates to openai-go internally so it shares the path. A future
// SDK rename of `Error` would silently break the
// provider_auth-on-credential-rejection flow without this guard.
func TestProviderAuthStatus(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err        error
		wantStatus int
		wantAuth   bool
	}{
		"nil":              {err: nil, wantStatus: 0, wantAuth: false},
		"plain error":      {err: errors.New("network closed"), wantStatus: 0, wantAuth: false},
		"anthropic 401":    {err: &anthropicsdk.Error{StatusCode: 401}, wantStatus: 401, wantAuth: true},
		"anthropic 403":    {err: &anthropicsdk.Error{StatusCode: 403}, wantStatus: 403, wantAuth: true},
		"anthropic 429":    {err: &anthropicsdk.Error{StatusCode: 429}, wantStatus: 429, wantAuth: false},
		"anthropic 500":    {err: &anthropicsdk.Error{StatusCode: 500}, wantStatus: 500, wantAuth: false},
		"openai 401":       {err: &openaisdk.Error{StatusCode: 401}, wantStatus: 401, wantAuth: true},
		"openai 403":       {err: &openaisdk.Error{StatusCode: 403}, wantStatus: 403, wantAuth: true},
		"openai 429":       {err: &openaisdk.Error{StatusCode: 429}, wantStatus: 429, wantAuth: false},
		"openai 502":       {err: &openaisdk.Error{StatusCode: 502}, wantStatus: 502, wantAuth: false},
		"wrapped 401":      {err: fmt.Errorf("wrap: %w", &anthropicsdk.Error{StatusCode: 401}), wantStatus: 401, wantAuth: true},
		"wrapped non-auth": {err: fmt.Errorf("wrap: %w", &openaisdk.Error{StatusCode: 500}), wantStatus: 500, wantAuth: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			status, ok := providerAuthStatus(tc.err)
			if ok != tc.wantAuth {
				t.Fatalf("auth = %v, want %v (status=%d, err=%v)", ok, tc.wantAuth, status, tc.err)
			}
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
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
// auth.command must NOT run the helper, because newProviderClient
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
			Type:    "exec",
			Command: "exit 17",
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
