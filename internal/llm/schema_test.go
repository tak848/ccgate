package llm

import (
	"bytes"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// orderedPropertyKeys returns the keys of a JSON object in source order
// (encoding/json into a map would lose it).
func orderedPropertyKeys(t *testing.T, obj json.RawMessage) []string {
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
		key, ok := keyTok.(string)
		if !ok {
			t.Fatalf("expected string key, got %v", keyTok)
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatalf("skip value for %q: %v", key, err)
		}
	}
	return keys
}

// TestOutputSchemaPropertyOrder is the regression guard for the bug
// where the schema reached the model with properties in alphabetical
// order (behavior, deny_message, reason), forcing the model to emit its
// decision before its reasoning. reason MUST come first. The check runs
// against both producers, marshaling the map exactly as an SDK would.
func TestOutputSchemaPropertyOrder(t *testing.T) {
	wantOrder := []string{"reason", "behavior", "deny_message"}

	producers := map[string]func() ([]byte, error){
		"raw": func() ([]byte, error) {
			r, err := OutputSchemaRaw()
			return []byte(r), err
		},
		"map": func() ([]byte, error) {
			m, err := OutputSchemaMap()
			if err != nil {
				return nil, err
			}
			return json.Marshal(m)
		},
	}

	for name, produce := range producers {
		t.Run(name, func(t *testing.T) {
			data, err := produce()
			if err != nil {
				t.Fatalf("produce schema: %v", err)
			}

			var shape struct {
				Properties json.RawMessage `json:"properties"`
				Required   []string        `json:"required"`
			}
			if err := json.Unmarshal(data, &shape); err != nil {
				t.Fatalf("unmarshal schema: %v", err)
			}

			gotOrder := orderedPropertyKeys(t, shape.Properties)
			if !reflect.DeepEqual(gotOrder, wantOrder) {
				t.Errorf("property order = %v, want %v", gotOrder, wantOrder)
			}

			// All fields must be required: Anthropic places required
			// fields before optional ones, so a non-required field would
			// be reordered and break the contract.
			gotRequired := append([]string(nil), shape.Required...)
			sort.Strings(gotRequired)
			wantRequired := []string{"behavior", "deny_message", "reason"}
			if !reflect.DeepEqual(gotRequired, wantRequired) {
				t.Errorf("required = %v, want all of %v", shape.Required, wantRequired)
			}
		})
	}
}

// TestOutputSchemaBehaviorEnum verifies behavior is constrained to the
// three valid decisions so the model cannot return an out-of-band value.
func TestOutputSchemaBehaviorEnum(t *testing.T) {
	raw, err := OutputSchemaRaw()
	if err != nil {
		t.Fatalf("OutputSchemaRaw: %v", err)
	}
	var shape struct {
		Properties struct {
			Behavior struct {
				Enum []string `json:"enum"`
			} `json:"behavior"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	got := append([]string(nil), shape.Properties.Behavior.Enum...)
	sort.Strings(got)
	want := []string{"allow", "deny", "fallthrough"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("behavior enum = %v, want %v", shape.Properties.Behavior.Enum, want)
	}
}
