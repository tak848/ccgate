package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tak848/ccgate/internal/llm"
)

// newMockOpenAIServer stands up a /chat/completions stand-in. The
// decoded request body of the last call lands in *captured so tests
// can assert on the exact wire shape; finishReason and content shape
// the reply.
func newMockOpenAIServer(t *testing.T, captured *map[string]any, finishReason, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		*captured = body

		resp := map[string]any{
			"id":     "chatcmpl-test",
			"object": "chat.completion",
			"model":  "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": finishReason,
				"message":       map[string]any{"role": "assistant", "content": content},
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 3},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testPrompt() llm.Prompt {
	return llm.Prompt{
		Model:     "test-model",
		System:    "you are a permission gate",
		User:      `{"tool_name":"Bash"}`,
		TimeoutMS: 5_000,
	}
}

// TestDecideRequestShape pins what actually goes on the wire. The
// temperature cases are the point of the exercise: newer models reject
// the field outright, so it must never be sent regardless of the other
// settings.
func TestDecideRequestShape(t *testing.T) {
	tests := map[string]struct {
		effort     string
		wantEffort any // nil = the key must be absent
	}{
		"none is sent verbatim": {
			effort:     llm.ReasoningEffortNone,
			wantEffort: "none",
		},
		"a named level is sent verbatim": {
			effort:     llm.ReasoningEffortLow,
			wantEffort: "low",
		},
		// Nothing rejects an unknown value client-side: the SDK types
		// the effort as a plain string with named constants, and a
		// proxy speaking this protocol may define its own levels.
		"an unknown level still reaches the wire": {
			effort:     "ultra",
			wantEffort: "ultra",
		},
		"the opt-out omits the parameter": {
			effort: llm.ReasoningEffortOff,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var body map[string]any
			srv := newMockOpenAIServer(t, &body, "stop", `{"reason":"ok","behavior":"allow"}`)

			c := &Client{APIKey: "test-key", BaseURL: srv.URL, ReasoningEffort: tc.effort}
			if _, err := c.Decide(context.Background(), testPrompt()); err != nil {
				t.Fatalf("Decide: %v", err)
			}

			if _, ok := body["temperature"]; ok {
				t.Errorf("request carried temperature=%v; it must never be sent", body["temperature"])
			}

			got, ok := body["reasoning_effort"]
			switch {
			case tc.wantEffort == nil && ok:
				t.Errorf("reasoning_effort = %v, want the key to be absent", got)
			case tc.wantEffort != nil && !ok:
				t.Errorf("reasoning_effort absent, want %v", tc.wantEffort)
			case tc.wantEffort != nil && got != tc.wantEffort:
				t.Errorf("reasoning_effort = %v, want %v", got, tc.wantEffort)
			}
		})
	}
}

// TestDecideResultClassification covers what Decide makes of the
// reply. The length case matters more now that reasoning tokens share
// the output budget: a model that thinks past the cap must surface as
// unusable rather than a parse error.
func TestDecideResultClassification(t *testing.T) {
	tests := map[string]struct {
		finishReason string
		content      string
		wantUnusable bool
		wantBehavior string
		wantDenyMsg  string
	}{
		"a complete response is parsed": {
			finishReason: "stop",
			content:      `{"reason":"read-only","behavior":"allow"}`,
			wantBehavior: llm.BehaviorAllow,
		},
		"running out of tokens is unusable": {
			finishReason: "length",
			content:      `{"reason":"partial`,
			wantUnusable: true,
		},
		"a filtered response is unusable": {
			finishReason: "content_filter",
			content:      "",
			wantUnusable: true,
		},
		"deny without a message gets the default": {
			finishReason: "stop",
			content:      `{"reason":"blocked","behavior":"deny"}`,
			wantBehavior: llm.BehaviorDeny,
			wantDenyMsg:  llm.DefaultDenyMessage,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var body map[string]any
			srv := newMockOpenAIServer(t, &body, tc.finishReason, tc.content)

			c := &Client{APIKey: "test-key", BaseURL: srv.URL}
			res, err := c.Decide(context.Background(), testPrompt())
			if err != nil {
				t.Fatalf("Decide: %v", err)
			}

			if res.Unusable != tc.wantUnusable {
				t.Fatalf("Unusable = %v, want %v", res.Unusable, tc.wantUnusable)
			}
			if tc.wantUnusable {
				return
			}
			if res.Output.Behavior != tc.wantBehavior {
				t.Errorf("Behavior = %q, want %q", res.Output.Behavior, tc.wantBehavior)
			}
			if tc.wantDenyMsg != "" && res.Output.DenyMessage != tc.wantDenyMsg {
				t.Errorf("DenyMessage = %q, want %q", res.Output.DenyMessage, tc.wantDenyMsg)
			}
		})
	}
}

func TestDecideWithoutAPIKey(t *testing.T) {
	c := &Client{}
	if _, err := c.Decide(context.Background(), testPrompt()); err == nil {
		t.Fatal("Decide with no API key returned nil error")
	}
}
