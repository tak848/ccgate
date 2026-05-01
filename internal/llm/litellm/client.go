// Package litellm implements llm.Provider against a user-hosted
// LiteLLM proxy via its OpenAI-compatible Chat Completions endpoint.
// Internally it delegates to the openai.Client so no separate HTTP
// logic is needed. Unlike anthropic / openai / gemini, LiteLLM has no
// hosted default endpoint -- every user runs their own proxy, so
// BaseURL is required.
package litellm

import (
	"context"
	"errors"

	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/llm/openai"
)

// ErrNoAPIKey is returned by Decide when neither
// CCGATE_LITELLM_API_KEY nor LITELLM_API_KEY is set.
var ErrNoAPIKey = errors.New("litellm: no API key set (CCGATE_LITELLM_API_KEY / LITELLM_API_KEY)")

// ErrNoBaseURL is returned by Decide when provider.base_url is empty.
// LiteLLM has no hosted default, so the user must point ccgate at
// their proxy.
var ErrNoBaseURL = errors.New("litellm: no base URL set (provider.base_url is required)")

// Client implements llm.Provider against a LiteLLM proxy. APIKey and
// BaseURL are both required; BaseURL must point at the proxy's
// OpenAI-compatible endpoint (e.g. http://localhost:4000/v1).
type Client struct {
	APIKey  string
	BaseURL string
}

// Decide delegates to openai.Client pointed at the LiteLLM proxy.
func (c *Client) Decide(ctx context.Context, p llm.Prompt) (llm.Result, error) {
	if c.APIKey == "" {
		return llm.Result{}, ErrNoAPIKey
	}
	if c.BaseURL == "" {
		return llm.Result{}, ErrNoBaseURL
	}
	inner := &openai.Client{
		APIKey:  c.APIKey,
		BaseURL: c.BaseURL,
	}
	return inner.Decide(ctx, p)
}
