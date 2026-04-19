package gate

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/hookctx"
)

const (
	BehaviorAllow       = "allow"
	BehaviorDeny        = "deny"
	BehaviorFallthrough = "fallthrough"
	DefaultDenyMessage  = "Automatically denied as potentially dangerous."
)

// FallthroughKind* values are stored verbatim in metrics entries.
// Only FallthroughKindLLM is promotable via permission rules — the other
// kinds indicate runtime-mode or configuration conditions.
const (
	FallthroughKindUserInteraction = "user_interaction"
	FallthroughKindBypass          = "bypass"
	FallthroughKindDontAsk         = "dontask"
	FallthroughKindNonAnthropic    = "non_anthropic"
	FallthroughKindNoAPIKey        = "no_apikey"
	FallthroughKindLLM             = "llm"
)

type PermissionDecision struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

// DecisionResult is the rich result from DecidePermission.
// Invariants:
//   - HasDecision=true: Decision is set, FallthroughKind is empty
//   - HasDecision=false: Decision is zero, FallthroughKind describes why
//   - Usage is non-nil only when an API call was made
type DecisionResult struct {
	Decision        PermissionDecision
	HasDecision     bool
	FallthroughKind string    // why fallthrough: user_interaction, bypass, dontask, no_apikey, non_anthropic, llm
	Usage           *APIUsage // nil if no API call
	LLMReason       string
}

// PermissionResponse is the JSON structure written to stdout for Claude Code.
type PermissionResponse struct {
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

// NewPermissionResponse creates the response structure expected by Claude Code.
func NewPermissionResponse(d PermissionDecision) PermissionResponse {
	return PermissionResponse{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName: "PermissionRequest",
			Decision: permissionDecisionOutput{
				Behavior: d.Behavior,
				Message:  d.Message,
			},
		},
	}
}

// DecidePermission calls the LLM to decide whether to allow, deny, or fallthrough.
func DecidePermission(ctx context.Context, cfg config.Config, input hookctx.HookInput) (DecisionResult, error) {
	// Tools that require user interaction must never be auto-decided.
	switch input.ToolName {
	case "ExitPlanMode", "AskUserQuestion":
		slog.Info("user interaction tool: falling through", "tool", input.ToolName)
		return DecisionResult{FallthroughKind: FallthroughKindUserInteraction}, nil
	}

	// Some permission modes should not be overridden by the hook.
	switch input.PermissionMode {
	case "plan":
		// In plan mode, let the LLM decide for non-interaction tools.
	case "bypassPermissions":
		slog.Info("bypass mode: falling through", "tool", input.ToolName)
		return DecisionResult{FallthroughKind: FallthroughKindBypass}, nil
	case "dontAsk":
		slog.Info("dontAsk mode: falling through", "tool", input.ToolName)
		return DecisionResult{FallthroughKind: FallthroughKindDontAsk}, nil
	}

	if strings.ToLower(cfg.Provider.Name) != "anthropic" {
		slog.Info("provider not anthropic, skipping", "provider", cfg.Provider.Name)
		return DecisionResult{FallthroughKind: FallthroughKindNonAnthropic}, nil
	}

	apiKey, ok := resolveAPIKey()
	if !ok {
		slog.Warn("no API key found (CCGATE_ANTHROPIC_API_KEY / ANTHROPIC_API_KEY)")
		return DecisionResult{FallthroughKind: FallthroughKindNoAPIKey}, nil
	}

	slog.Info("calling anthropic",
		"model", cfg.Provider.Model,
		"timeout_ms", cfg.GetTimeoutMS(),
		"tool", input.ToolName,
	)

	output, usage, err := callAnthropic(ctx, cfg, input, apiKey)
	if err != nil {
		slog.Error("anthropic API call failed", "error", err, "tool", input.ToolName)
		return DecisionResult{Usage: usage}, err
	}

	slog.Info("LLM decision",
		"behavior", output.Behavior,
		"reason", output.Reason,
		"deny_message", output.DenyMessage,
		"tool", input.ToolName,
	)

	base := DecisionResult{
		Usage:     usage,
		LLMReason: output.Reason,
	}

	switch output.Behavior {
	case BehaviorAllow:
		base.Decision = PermissionDecision{Behavior: BehaviorAllow}
		base.HasDecision = true
		return base, nil
	case BehaviorDeny:
		message := strings.TrimSpace(output.DenyMessage)
		if message == "" {
			message = DefaultDenyMessage
		}
		base.Decision = PermissionDecision{Behavior: BehaviorDeny, Message: message}
		base.HasDecision = true
		return base, nil
	case BehaviorFallthrough, "":
		base.FallthroughKind = FallthroughKindLLM
		return base, nil
	default:
		slog.Warn("unexpected LLM behavior", "behavior", output.Behavior)
		base.FallthroughKind = FallthroughKindLLM
		return base, nil
	}
}

func resolveAPIKey() (string, bool) {
	if key := strings.TrimSpace(os.Getenv("CCGATE_ANTHROPIC_API_KEY")); key != "" {
		return key, true
	}
	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		return key, true
	}
	return "", false
}
