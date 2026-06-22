package openai

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	openaigo "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/tak848/ccgate/internal/llm"
)

// orderedKeys returns the keys of a JSON object in source order.
func orderedKeys(t *testing.T, obj json.RawMessage) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(obj))
	tok, err := dec.Token()
	if err != nil {
		t.Fatalf("read opening token: %v", err)
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		t.Fatalf("expected object, got %v", tok)
	}
	var keys []string
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			t.Fatalf("read key token: %v", err)
		}
		keys = append(keys, keyTok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip value: %v", err)
		}
	}
	return keys
}

// TestOutputSchemaWireOrder marshals the schema through the OpenAI SDK's
// own encoder (the ResponseFormatJSONSchema path used by Decide) and
// asserts the property order survives as reason, behavior, deny_message.
// This guards against an SDK upgrade changing how it serializes the
// json.RawMessage schema value, which would silently revert order to
// alphabetical and reintroduce decide-before-reason.
func TestOutputSchemaWireOrder(t *testing.T) {
	raw, err := llm.OutputSchemaRaw()
	if err != nil {
		t.Fatalf("OutputSchemaRaw: %v", err)
	}

	wire, err := json.Marshal(openaigo.ResponseFormatJSONSchemaJSONSchemaParam{
		Name:   "permission_decision",
		Strict: param.NewOpt(true),
		Schema: raw,
	})
	if err != nil {
		t.Fatalf("marshal ResponseFormatJSONSchemaJSONSchemaParam: %v", err)
	}

	var format struct {
		Schema struct {
			Properties json.RawMessage `json:"properties"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(wire, &format); err != nil {
		t.Fatalf("unmarshal wire payload: %v", err)
	}

	got := orderedKeys(t, format.Schema.Properties)
	want := []string{"reason", "behavior", "deny_message"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wire property order = %v, want %v\npayload: %s", got, want, wire)
	}
}
