package runner

import (
	"errors"
	"strings"
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

// TestReasoningEffortHint checks that a provider rejecting the
// reasoning parameter produces the pointer to the config key, and that
// unrelated failures stay quiet.
func TestReasoningEffortHint(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		err      error
		wantHint bool
	}{
		"openai rejecting the parameter": {
			err:      errors.New(`openai API error (status 400), type=invalid_request_error: Unrecognized request argument supplied: reasoning_effort`),
			wantHint: true,
		},
		"openai rejecting the value": {
			err:      errors.New(`openai API error (status 400): Unsupported value: 'reasoning_effort' does not support 'none' with this model.`),
			wantHint: true,
		},
		"anthropic rejecting adaptive thinking": {
			err:      errors.New(`anthropic API error (status 400): adaptive thinking is not supported on this model`),
			wantHint: true,
		},
		"anthropic rejecting the effort parameter": {
			err:      errors.New(`anthropic API error (status 400): This model does not support the effort parameter.`),
			wantHint: true,
		},
		"an unrelated failure": {
			err: errors.New(`openai API error (status 401), type=authentication_error: invalid api key`),
		},
		"no error at all": {},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := config.ProviderConfig{Name: "openai", Model: "m"}
			got := reasoningEffortHint(p, tc.err)
			if tc.wantHint && got == "" {
				t.Fatal("no hint, want one naming provider.reasoning_effort")
			}
			if !tc.wantHint && got != "" {
				t.Fatalf("hint = %q, want none", got)
			}
			if tc.wantHint && !strings.Contains(got, "provider.reasoning_effort") {
				t.Fatalf("hint = %q, want it to name the config key", got)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
