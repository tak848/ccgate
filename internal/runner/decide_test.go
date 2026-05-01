package runner

import (
	"context"
	"testing"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/llm"
)

// TestDecideLitellmBaseURLFallthrough verifies that an empty or
// whitespace-only `provider.base_url` for the litellm provider
// short-circuits to a graceful FallthroughKindNoBaseURL instead of
// hitting the OpenAI SDK with an invalid URL (which would surface
// as exit 1 rather than deferring to the upstream permission
// prompt). Whitespace coverage protects against templating mistakes
// like `base_url: '   '` from a half-rendered jsonnet.
func TestDecideLitellmBaseURLFallthrough(t *testing.T) {
	cases := map[string]struct {
		baseURL  string
		wantKind string
	}{
		"empty":            {"", llm.FallthroughKindNoBaseURL},
		"whitespace only":  {"   ", llm.FallthroughKindNoBaseURL},
		"tabs and newline": {"\t\n ", llm.FallthroughKindNoBaseURL},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CCGATE_LITELLM_API_KEY", "sk-test")
			cfg := config.Default()
			cfg.Provider = config.ProviderConfig{
				Name:    "litellm",
				Model:   "anthropic/claude-haiku-4-5",
				BaseURL: tc.baseURL,
			}
			in := HookInput{ToolName: "Bash"}
			_, hasDecision, kind, _, _, err := decide(context.Background(), cfg, in, runtimeOptions{})
			if err != nil {
				t.Fatalf("decide returned error: %v", err)
			}
			if hasDecision {
				t.Fatalf("expected fallthrough, got hasDecision=true")
			}
			if kind != tc.wantKind {
				t.Fatalf("ft_kind = %q, want %q", kind, tc.wantKind)
			}
		})
	}
}
