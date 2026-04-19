package hookctx

import (
	"encoding/json"
	"strings"
)

type HookInput struct {
	SessionID             string            `json:"session_id"`
	TranscriptPath        string            `json:"transcript_path"`
	Cwd                   string            `json:"cwd"`
	PermissionMode        string            `json:"permission_mode"`
	HookEventName         string            `json:"hook_event_name"`
	ToolName              string            `json:"tool_name"`
	ToolInput             HookToolInput     `json:"-"`
	ToolInputRaw          json.RawMessage   `json:"tool_input"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions"`
}

type HookToolInput struct {
	Command        string              `json:"command"`
	Description    string              `json:"description"`
	FilePath       string              `json:"file_path"`
	Path           string              `json:"path"`
	Pattern        string              `json:"pattern"`
	Content        string              `json:"content"`
	ContentUpdates []HookContentUpdate `json:"content_updates"`
}

type HookContentUpdate struct {
	OldString string `json:"old_str"`
	NewString string `json:"new_str"`
}

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

// MetricsFields returns the subset of tool_input recorded in metrics.
// Only command/file_path/path/pattern are exposed; description,
// content, content_updates and the raw JSON are intentionally omitted
// to keep metrics compact and avoid leaking descriptive or file-body text.
func (h HookInput) MetricsFields() (command, filePath, path, pattern string) {
	return h.ToolInput.Command, h.ToolInput.FilePath, h.ToolInput.Path, h.ToolInput.Pattern
}

// ToolInputText returns a textual representation of the tool input for path extraction.
func (h HookInput) ToolInputText() string {
	var parts []string
	if h.ToolInput.Command != "" {
		parts = append(parts, h.ToolInput.Command)
	}
	if h.ToolInput.FilePath != "" {
		parts = append(parts, h.ToolInput.FilePath)
	}
	if h.ToolInput.Path != "" {
		parts = append(parts, h.ToolInput.Path)
	}
	if h.ToolInput.Pattern != "" {
		parts = append(parts, h.ToolInput.Pattern)
	}
	if h.ToolInput.Content != "" {
		parts = append(parts, h.ToolInput.Content)
	}
	for _, update := range h.ToolInput.ContentUpdates {
		if update.OldString != "" {
			parts = append(parts, update.OldString)
		}
		if update.NewString != "" {
			parts = append(parts, update.NewString)
		}
	}
	if len(h.ToolInputRaw) > 0 {
		parts = append(parts, string(h.ToolInputRaw))
	}
	return strings.Join(parts, "\n")
}
