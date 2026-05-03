package runner

import (
	"errors"
	"fmt"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	openaisdk "github.com/openai/openai-go"
)

// TestProviderAuthStatus pins the SDK-error type-assertion that the
// runner relies on to distinguish "credential rejected" (401/403)
// from other API errors. The promotion path is what lets ccgate
// invalidate the cache and fall through gracefully on rotated /
// revoked credentials, so a future SDK rename of `Error` would
// silently break it without this guard.
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
