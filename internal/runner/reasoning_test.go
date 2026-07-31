package runner

import (
	"testing"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/llm/anthropic"
	"github.com/tak848/ccgate/internal/llm/gemini"
	"github.com/tak848/ccgate/internal/llm/openai"
)

// TestNewProviderClientCarriesReasoningEffort guards the wiring: every
// provider client has its own ReasoningEffort field, so a new provider
// added to the switch without the field set would silently fall back
// to the model's own default reasoning.
func TestNewProviderClientCarriesReasoningEffort(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		provider string
		effort   *string
		want     string
	}{
		"openai gets the default":     {provider: "openai", want: config.DefaultReasoningEffort},
		"gemini gets the default":     {provider: "gemini", want: config.DefaultReasoningEffort},
		"anthropic gets the default":  {provider: "anthropic", want: config.DefaultReasoningEffort},
		"openai gets an explicit set": {provider: "openai", effort: ptr(llm.ReasoningEffortLow), want: llm.ReasoningEffortLow},
		"the opt-out reaches openai":  {provider: "openai", effort: ptr(llm.ReasoningEffortOff), want: llm.ReasoningEffortOff},
		"the opt-out reaches gemini":  {provider: "gemini", effort: ptr(llm.ReasoningEffortOff), want: llm.ReasoningEffortOff},
		"the opt-out reaches claude":  {provider: "anthropic", effort: ptr(llm.ReasoningEffortOff), want: llm.ReasoningEffortOff},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := config.ProviderConfig{Name: tc.provider, Model: "m", ReasoningEffort: tc.effort}
			cli := newProviderClient(tc.provider, p, "key", "")

			var got string
			switch c := cli.(type) {
			case *openai.Client:
				got = c.ReasoningEffort
			case *gemini.Client:
				got = c.ReasoningEffort
			case *anthropic.Client:
				got = c.ReasoningEffort
			default:
				t.Fatalf("unexpected client type %T", cli)
			}
			if got != tc.want {
				t.Fatalf("ReasoningEffort = %q, want %q", got, tc.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
