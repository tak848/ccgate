// Package anthropic implements llm.Provider against the Anthropic
// Messages API. The client is target-agnostic: callers (cmd/claude,
// cmd/codex) build their own Prompt and feed it through Decide.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/invopop/jsonschema"

	"github.com/tak848/ccgate/internal/llm"
)

const (
	maxTokens  = 4096
	maxRetries = 5
)

// ErrNoAPIKey is returned by Decide when neither
// CCGATE_ANTHROPIC_API_KEY nor ANTHROPIC_API_KEY is set. Callers
// should treat this as a fallthrough (not a hard error) so the hook
// degrades gracefully when the user has not configured an API key.
var ErrNoAPIKey = errors.New("anthropic: no API key set (CCGATE_ANTHROPIC_API_KEY / ANTHROPIC_API_KEY)")

// Client is a stateless wrapper around the Anthropic SDK that
// implements llm.Provider. APIKey is required; BaseURL lets tests
// point the client at a httptest.Server.
type Client struct {
	APIKey  string
	BaseURL string
}

// Decide sends a single classification request and parses the
// structured response into llm.Result.
func (c *Client) Decide(ctx context.Context, p llm.Prompt) (llm.Result, error) {
	if c.APIKey == "" {
		return llm.Result{}, ErrNoAPIKey
	}

	if p.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	opts := []option.RequestOption{
		option.WithAPIKey(c.APIKey),
		option.WithMaxRetries(maxRetries),
	}
	if c.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(c.BaseURL))
	}
	client := anthropicsdk.NewClient(opts...)

	schema, err := outputSchema()
	if err != nil {
		return llm.Result{}, fmt.Errorf("generate output schema: %w", err)
	}

	message, err := client.Messages.New(ctx, anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model(p.Model),
		MaxTokens: maxTokens,
		System:    []anthropicsdk.TextBlockParam{{Text: p.System}},
		Messages: []anthropicsdk.MessageParam{
			anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(p.User)),
		},
		OutputConfig: anthropicsdk.OutputConfigParam{
			Format: anthropicsdk.JSONOutputFormatParam{Schema: schema},
		},
		Temperature: anthropicsdk.Float(0),
	})
	if err != nil {
		return llm.Result{}, fmt.Errorf("anthropic API: %w", err)
	}

	usage := &llm.Usage{
		InputTokens:  message.Usage.InputTokens,
		OutputTokens: message.Usage.OutputTokens,
	}

	if message.StopReason == anthropicsdk.StopReasonMaxTokens || message.StopReason == anthropicsdk.StopReasonRefusal {
		slog.Warn("anthropic response truncated or refused", "stop_reason", message.StopReason)
		return llm.Result{Usage: usage, Unusable: true}, nil
	}

	text := extractMessageText(message)
	slog.Info("anthropic response", "raw", text)
	if text == "" {
		slog.Warn("anthropic response had no text content")
		return llm.Result{Usage: usage, Unusable: true}, nil
	}

	var output llm.Output
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		return llm.Result{Usage: usage}, fmt.Errorf("parse LLM response: %w", err)
	}
	if output.Behavior == llm.BehaviorDeny && strings.TrimSpace(output.DenyMessage) == "" {
		output.DenyMessage = llm.DefaultDenyMessage
	}

	return llm.Result{Output: output, Usage: usage}, nil
}

func outputSchema() (map[string]any, error) {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	schema := reflector.Reflect(llm.Output{})
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	return out, nil
}

func extractMessageText(message *anthropicsdk.Message) string {
	if message == nil {
		return ""
	}
	var text strings.Builder
	for _, block := range message.Content {
		switch variant := block.AsAny().(type) {
		case anthropicsdk.TextBlock:
			text.WriteString(variant.Text)
		}
	}
	return strings.TrimSpace(text.String())
}
