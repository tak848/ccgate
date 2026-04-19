package metrics

import "time"

// ToolInputFields is the structured subset of HookToolInput recorded in metrics.
// Only command/file_path/path/pattern are captured; content/content_updates are
// intentionally omitted to keep JSONL compact and avoid storing file contents.
type ToolInputFields struct {
	Command  string `json:"command,omitempty"`
	FilePath string `json:"file_path,omitempty"`
	Path     string `json:"path,omitempty"`
	Pattern  string `json:"pattern,omitempty"`
}

// Entry represents a single ccgate invocation for metrics.
//
// Forced is true when an LLM fallthrough was overridden by
// fallthrough_strategy=allow|deny. In that case Decision holds the forced
// allow/deny while FallthroughKind retains "llm" so that audits can recover
// the original uncertainty signal.
type Entry struct {
	Timestamp       time.Time       `json:"ts"`
	SessionID       string          `json:"sid,omitempty"`
	ToolName        string          `json:"tool"`
	PermissionMode  string          `json:"perm_mode"`
	Decision        string          `json:"decision"`
	FallthroughKind string          `json:"ft_kind,omitempty"`
	Forced          bool            `json:"forced,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	DenyMessage     string          `json:"deny_msg,omitempty"`
	Model           string          `json:"model,omitempty"`
	InputTokens     int64           `json:"in_tok,omitempty"`
	OutputTokens    int64           `json:"out_tok,omitempty"`
	ElapsedMS       int64           `json:"elapsed_ms"`
	Error           string          `json:"error,omitempty"`
	ToolInput       ToolInputFields `json:"tool_input,omitzero"`
}
