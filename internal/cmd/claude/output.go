package claude

import "github.com/tak848/ccgate/internal/llm"

// HookOutput is the JSON payload ccgate writes to stdout for a Claude
// Code PermissionRequest.
type HookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName string                   `json:"hookEventName"`
	Decision      permissionDecisionOutput `json:"decision"`
}

type permissionDecisionOutput struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

// NewPermissionResponse wraps an llm.Decision into the response shape
// Claude Code expects.
func NewPermissionResponse(d llm.Decision) HookOutput {
	return HookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName: "PermissionRequest",
			Decision: permissionDecisionOutput{
				Behavior: d.Behavior,
				Message:  d.Message,
			},
		},
	}
}
