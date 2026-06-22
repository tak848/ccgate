package llm

import (
	"encoding/json"
	"fmt"

	"github.com/invopop/jsonschema"
)

// reflectOutputSchema reflects Output into a JSON schema. The schema's
// Properties is an ordered map, so marshaling it preserves the struct
// field order (reason, behavior, deny_message).
func reflectOutputSchema() *jsonschema.Schema {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	return reflector.Reflect(Output{})
}

// OutputSchemaRaw returns the Output JSON schema as raw bytes with
// property order preserved (reason, behavior, deny_message). Use it for
// SDK schema fields typed as `any` (e.g. OpenAI): passing a
// json.RawMessage keeps the order intact on the wire because the SDK
// emits json.Marshaler values verbatim.
func OutputSchemaRaw() (json.RawMessage, error) {
	data, err := json.Marshal(reflectOutputSchema())
	if err != nil {
		return nil, fmt.Errorf("marshal output schema: %w", err)
	}
	return json.RawMessage(data), nil
}

// OutputSchemaMap returns the Output JSON schema as a map, required by
// SDK schema fields typed as map[string]any (e.g. Anthropic).
//
// Marshaling a Go map sorts keys alphabetically, which would reorder
// `properties` to behavior, deny_message, reason and defeat the
// reason-first contract (see Output). To prevent that, `properties` is
// injected as a json.RawMessage built from the ordered reflected
// schema; the SDK's apijson encoder emits json.Marshaler values
// verbatim, so the property order survives even though the surrounding
// map keys still get sorted (those remaining keys -- type, required,
// additionalProperties -- are order-insensitive).
func OutputSchemaMap() (map[string]any, error) {
	schema := reflectOutputSchema()
	full, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal output schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(full, &m); err != nil {
		return nil, fmt.Errorf("unmarshal output schema: %w", err)
	}
	props, err := json.Marshal(schema.Properties)
	if err != nil {
		return nil, fmt.Errorf("marshal output schema properties: %w", err)
	}
	m["properties"] = json.RawMessage(props)
	return m, nil
}
