package codex

import "github.com/tak848/ccgate/internal/llm"

// HookOutput is the JSON payload ccgate writes to stdout for a Codex
// PermissionRequest. behavior is allow|deny only; fallthrough is
// expressed by writing nothing (Codex then runs its own approval
// prompt).
type HookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
	SystemMessage      string             `json:"systemMessage,omitempty"`
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
// Codex CLI expects.
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
