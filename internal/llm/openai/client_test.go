package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tak848/ccgate/internal/llm"
)

// TestDecide_RetriesWithoutTemperatureOnUnsupported verifies that when the
// upstream returns the documented 400 for `temperature` (reasoning-only
// models such as gpt-5-nano), Decide retries once with `temperature`
// stripped and parses the second response normally.
func TestDecide_RetriesWithoutTemperatureOnUnsupported(t *testing.T) {
	t.Parallel()

	var (
		calls    int
		bodies   []string
		respText = `{"behavior":"allow","reason":"read-only","deny_message":""}`
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		calls++

		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{
				"error": {
					"message": "Unsupported value: 'temperature' does not support 0 with this model. Only the default (1) value is supported.",
					"type": "invalid_request_error",
					"param": "temperature",
					"code": "unsupported_value"
				}
			}`))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 0,
			"model":   "gpt-5-nano",
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": respText,
				},
			}},
			"usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer srv.Close()

	client := &Client{APIKey: "test-key", BaseURL: srv.URL + "/"}
	got, err := client.Decide(context.Background(), llm.Prompt{
		System: "sys",
		User:   "usr",
		Model:  "gpt-5-nano",
	})
	if err != nil {
		t.Fatalf("Decide returned error: %v", err)
	}

	if calls != 2 {
		t.Fatalf("want 2 upstream calls (1 reject + 1 retry), got %d", calls)
	}
	if !strings.Contains(bodies[0], `"temperature"`) {
		t.Errorf("first request should have included temperature, got: %s", bodies[0])
	}
	if strings.Contains(bodies[1], `"temperature"`) {
		t.Errorf("retry request must omit temperature, got: %s", bodies[1])
	}
	if got.Output.Behavior != llm.BehaviorAllow {
		t.Errorf("behavior = %q, want %q", got.Output.Behavior, llm.BehaviorAllow)
	}
	if got.Usage == nil || got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 5 {
		t.Errorf("usage = %+v, want {10, 5}", got.Usage)
	}
}

// TestDecide_PassesThroughOtherErrors verifies that a 400 with a different
// `param` is not treated as a temperature problem: it must surface as an
// error and not trigger a retry.
func TestDecide_PassesThroughOtherErrors(t *testing.T) {
	t.Parallel()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{
			"error": {
				"message": "boom",
				"type": "invalid_request_error",
				"param": "model",
				"code": "model_not_found"
			}
		}`))
	}))
	defer srv.Close()

	client := &Client{APIKey: "test-key", BaseURL: srv.URL + "/"}
	_, err := client.Decide(context.Background(), llm.Prompt{Model: "nope"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("want 1 upstream call (no retry), got %d", calls)
	}
}

// TestIsTemperatureUnsupported_NilAndPlain ensures the predicate does not
// panic on nil and rejects errors that are not OpenAI API errors.
func TestIsTemperatureUnsupported_NilAndPlain(t *testing.T) {
	t.Parallel()

	if isTemperatureUnsupported(nil) {
		t.Error("nil error should not be treated as temperature-unsupported")
	}
	if isTemperatureUnsupported(io.EOF) {
		t.Error("non-API error should not be treated as temperature-unsupported")
	}
}
