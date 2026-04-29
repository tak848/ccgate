// Package openai implements llm.Provider against the OpenAI Chat
// Completions API. The client is target-agnostic: callers build their
// own Prompt and feed it through Decide.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	openaigo "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"

	"github.com/tak848/ccgate/internal/llm"
)

const (
	maxTokens  = 4096
	maxRetries = 5
)

// ErrNoAPIKey is returned by Decide when neither
// CCGATE_OPENAI_API_KEY nor OPENAI_API_KEY is set.
var ErrNoAPIKey = errors.New("openai: no API key set (CCGATE_OPENAI_API_KEY / OPENAI_API_KEY)")

// Client is a stateless wrapper around the OpenAI SDK that implements
// llm.Provider. APIKey is required; BaseURL lets callers point the
// client at a compatible endpoint (e.g. Gemini, local LLMs).
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
	client := openaigo.NewClient(opts...)

	schema, err := outputSchema()
	if err != nil {
		return llm.Result{}, fmt.Errorf("generate output schema: %w", err)
	}

	message, err := client.Chat.Completions.New(ctx, openaigo.ChatCompletionNewParams{
		Model: openaigo.ChatModel(p.Model),
		Messages: []openaigo.ChatCompletionMessageParamUnion{
			openaigo.SystemMessage(p.System),
			openaigo.UserMessage(p.User),
		},
		ResponseFormat: openaigo.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &openaigo.ResponseFormatJSONSchemaParam{
				JSONSchema: openaigo.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "permission_decision",
					Strict: param.NewOpt(true),
					Schema: schema,
				},
			},
		},
		MaxCompletionTokens: param.NewOpt(int64(maxTokens)),
		Temperature:         param.NewOpt(float64(0)),
	})
	if err != nil {
		return llm.Result{}, fmt.Errorf("openai API: %w", err)
	}

	usage := &llm.Usage{
		InputTokens:  message.Usage.PromptTokens,
		OutputTokens: message.Usage.CompletionTokens,
	}

	if len(message.Choices) == 0 {
		slog.Warn("openai response had no choices")
		return llm.Result{Usage: usage, Unusable: true}, nil
	}

	choice := message.Choices[0]
	switch choice.FinishReason {
	case "length", "content_filter":
		slog.Warn("openai response truncated or filtered", "finish_reason", choice.FinishReason)
		return llm.Result{Usage: usage, Unusable: true}, nil
	}

	text := strings.TrimSpace(choice.Message.Content)
	slog.Info("openai response", "raw", text)
	if text == "" {
		slog.Warn("openai response had no text content")
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
