package anthropic

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"

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

// TestOutputSchemaWireOrder marshals the schema through the Anthropic
// SDK's own encoder (the JSONOutputFormatParam path used by Decide) and
// asserts the property order survives as reason, behavior, deny_message.
// Unlike the std-lib check in internal/llm, this guards against an SDK
// upgrade changing how it serializes json.RawMessage values nested in a
// map[string]any -- which would silently revert order to alphabetical.
func TestOutputSchemaWireOrder(t *testing.T) {
	m, err := llm.OutputSchemaMap()
	if err != nil {
		t.Fatalf("OutputSchemaMap: %v", err)
	}

	wire, err := json.Marshal(anthropicsdk.JSONOutputFormatParam{Schema: m})
	if err != nil {
		t.Fatalf("marshal JSONOutputFormatParam: %v", err)
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
