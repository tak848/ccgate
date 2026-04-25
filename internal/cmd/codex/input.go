package codex

import "encoding/json"

// HookInput is the JSON payload Codex CLI delivers on stdin for a
// PermissionRequest hook.
//
// Fields kept here are the upstream-verified subset (developers.openai.com/codex/hooks
// as of 2026-04-24). Forks (e.g. stellarlinkco) expose a richer schema
// — when those fields land in upstream we will extend this struct and
// update the Spec Ledger in plan/.
//
// At time of writing Codex always sets ToolName="Bash"; classification
// must therefore happen via Command shape rather than tool kind.
type HookInput struct {
	SessionID      string        `json:"session_id"`
	TranscriptPath string        `json:"transcript_path"`
	Cwd            string        `json:"cwd"`
	HookEventName  string        `json:"hook_event_name"`
	Model          string        `json:"model,omitempty"`
	TurnID         string        `json:"turn_id,omitempty"`
	ToolName       string        `json:"tool_name"`
	ToolInput      HookToolInput `json:"-"`
	ToolInputRaw   json.RawMessage
}

// HookToolInput is the parsed view of Codex's tool_input field. Codex
// hooks currently only invoke Bash, so command + description are the
// fields callers can rely on; everything else is target-future-proofing.
type HookToolInput struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// UnmarshalJSON keeps the raw tool_input bytes around (so the LLM can
// see whatever future fields Codex adds) while also surfacing the
// parsed fields we already understand.
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
		if err := json.Unmarshal(raw.ToolInput, &h.ToolInput); err != nil {
			return err
		}
	}
	return nil
}
