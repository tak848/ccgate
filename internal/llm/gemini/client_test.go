package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tak848/ccgate/internal/llm"
)

// TestReasoningEffortReachesTheWire pins the Gemini-specific lowering.
// Gemini rejects "none" on every model a new API user can reach, so
// the default has to arrive as "minimal" or the provider is unusable
// out of the box.
func TestReasoningEffortReachesTheWire(t *testing.T) {
	tests := map[string]struct {
		effort     string
		wantEffort any // nil = the key must be absent
	}{
		"none is lowered to minimal": {
			effort:     llm.ReasoningEffortNone,
			wantEffort: "minimal",
		},
		"a named level passes through": {
			effort:     llm.ReasoningEffortLow,
			wantEffort: "low",
		},
		"minimal passes through": {
			effort:     llm.ReasoningEffortMinimal,
			wantEffort: "minimal",
		},
		"the opt-out sends nothing": {
			effort: llm.ReasoningEffortOff,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var body map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request body: %v", err)
					http.Error(w, "bad body", http.StatusBadRequest)
					return
				}
				resp := map[string]any{
					"id":     "chatcmpl-test",
					"object": "chat.completion",
					"model":  "gemini-test",
					"choices": []map[string]any{{
						"index":         0,
						"finish_reason": "stop",
						"message":       map[string]any{"role": "assistant", "content": `{"reason":"ok","behavior":"allow"}`},
					}},
					"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 3},
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(resp)
			}))
			t.Cleanup(srv.Close)

			c := &Client{APIKey: "test-key", BaseURL: srv.URL, ReasoningEffort: tc.effort}
			p := llm.Prompt{Model: "gemini-test", System: "s", User: `{"tool_name":"Bash"}`, TimeoutMS: 5_000}
			if _, err := c.Decide(context.Background(), p); err != nil {
				t.Fatalf("Decide: %v", err)
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

func TestDecideWithoutAPIKey(t *testing.T) {
	c := &Client{}
	if _, err := c.Decide(context.Background(), llm.Prompt{Model: "gemini-test"}); err == nil {
		t.Fatal("Decide with no API key returned nil error")
	}
}
