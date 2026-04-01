package metrics

import "time"

// Entry represents a single ccgate invocation for metrics.
type Entry struct {
	Timestamp       time.Time `json:"ts"`
	SessionID       string    `json:"sid,omitempty"`
	ToolName        string    `json:"tool"`
	PermissionMode  string    `json:"perm_mode"`
	Decision        string    `json:"decision"`
	FallthroughKind string    `json:"ft_kind,omitempty"`
	Reason          string    `json:"reason,omitempty"`
	DenyMessage     string    `json:"deny_msg,omitempty"`
	Model           string    `json:"model,omitempty"`
	InputTokens     int64     `json:"in_tok,omitempty"`
	OutputTokens    int64     `json:"out_tok,omitempty"`
	ElapsedMS       int64     `json:"elapsed_ms"`
	Error           string    `json:"error,omitempty"`
}
