// Package gemini implements llm.Provider against the Google Gemini API
// via its OpenAI-compatible endpoint. Internally it delegates to the
// openai.Client so no separate HTTP logic is needed.
package gemini

import (
	"context"
	"errors"

	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/llm/openai"
)

// DefaultBaseURL is the Gemini OpenAI-compatible endpoint.
// See https://ai.google.dev/gemini-api/docs/openai
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta/openai/"

// ErrNoAPIKey is returned by Decide when neither
// CCGATE_GEMINI_API_KEY nor GEMINI_API_KEY is set.
var ErrNoAPIKey = errors.New("gemini: no API key set (CCGATE_GEMINI_API_KEY / GEMINI_API_KEY)")

// Client implements llm.Provider against the Gemini OpenAI-compatible
// endpoint. APIKey is required; BaseURL overrides the default endpoint
// for testing.
type Client struct {
	APIKey  string
	BaseURL string

	// ReasoningEffort is forwarded to the OpenAI-compatible endpoint
	// as `reasoning_effort`. Empty omits it.
	ReasoningEffort string
}

// Decide delegates to openai.Client pointed at the Gemini endpoint.
func (c *Client) Decide(ctx context.Context, p llm.Prompt) (llm.Result, error) {
	if c.APIKey == "" {
		return llm.Result{}, ErrNoAPIKey
	}
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	inner := &openai.Client{
		APIKey:          c.APIKey,
		BaseURL:         baseURL,
		ReasoningEffort: reasoningEffort(c.ReasoningEffort),
	}
	return inner.Decide(ctx, p)
}

// reasoningEffort lowers llm.ReasoningEffortNone to "minimal", the
// least Gemini will do.
//
// Gemini's compatibility layer maps reasoning_effort onto thinking
// levels, and its table bottoms out at minimal: "Reasoning cannot be
// turned off for Gemini 2.5 Pro or 3 models", and every model a new
// API user can reach is a 3.x one. Sending "none" is a 400 there, so
// passing it through would break the default on every current model
// while asking for the same thing minimal already delivers.
//
// Other values go out unchanged and Gemini decides.
func reasoningEffort(effort string) string {
	if effort == llm.ReasoningEffortNone {
		return llm.ReasoningEffortMinimal
	}
	return effort
}
