package codex

import "encoding/json"

// HookInput is the JSON payload Codex CLI delivers on stdin for a
// PermissionRequest hook.
//
// The payload is tool-agnostic: ToolName can be "Bash", "apply_patch",
// an MCP tool name (`mcp__server__tool_name` etc.), or any other
// surface Codex exposes. ToolInput is preserved as raw JSON so we can
// hand the full structure to the LLM regardless of shape, plus a
// best-effort parsed view for the few fields ccgate's metrics layer
// already understands.
//
// Fields kept here are the upstream-verified subset (developers.openai.com/codex/hooks
// as of 2026-04-26). Forks (e.g. stellarlinkco) expose a richer schema
// — when those fields land in upstream, extend this struct and link
// the relevant section of the upstream Codex hooks docs in the PR body.
type HookInput struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	Model          string          `json:"model,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	ToolName       string          `json:"tool_name"`
	ToolInput      HookToolInput   `json:"-"`
	ToolInputRaw   json.RawMessage `json:"-"`
}

// HookToolInput is the best-effort parsed view of Codex's tool_input.
// Only widely-shared fields are surfaced as typed accessors so the
// metrics layer can record them; tool-specific shapes (MCP arguments,
// apply_patch diff payloads, etc.) stay in HookInput.ToolInputRaw and
// are forwarded to the LLM verbatim.
type HookToolInput struct {
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	Path        string `json:"path,omitempty"`
	Pattern     string `json:"pattern,omitempty"`
}

// UnmarshalJSON keeps the raw tool_input bytes so the LLM sees every
// field Codex sends (including MCP arguments and tool-specific shapes
// ccgate does not yet model), while also surfacing the small parsed
// view metrics relies on.
func (h *HookInput) UnmarshalJSON(data []byte) error {
	type alias HookInput
	var raw struct {
		alias
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	*h = HookInput(raw.alias)
	h.ToolInputRaw = raw.ToolInput
	if len(raw.ToolInput) > 0 {
		// Best-effort parse: failure is fine because not every tool
		// shape matches HookToolInput. The raw bytes are still
		// forwarded to the LLM untouched.
		_ = json.Unmarshal(raw.ToolInput, &h.ToolInput)
	}
	return nil
}
