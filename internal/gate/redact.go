package gate

import "encoding/json"

const redactMask = "[REDACTED]"

func redactPromptInput(p PermissionPromptInput) PermissionPromptInput {
	r := p
	if r.ToolInput.Content != "" {
		r.ToolInput.Content = redactMask
	}
	if len(r.ToolInput.ContentUpdates) > 0 {
		r.ToolInput.ContentUpdates = nil
	}
	r.ToolInputRaw = nil
	r.PermissionSuggestions = nil
	return r
}

// mustJSONRedacted returns a redacted JSON string for logging.
// Falls back to "{}" on marshal error (logging should never fail the operation).
func mustJSONRedacted(p PermissionPromptInput) string {
	data, err := json.MarshalIndent(redactPromptInput(p), "", "  ")
	if err != nil {
		return "{}"
	}
	return string(data)
}
