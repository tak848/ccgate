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
	DefaultDenyMessage  = "危険な可能性が高いため、自動許可しません。"
)

type PermissionDecision struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
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
// Returns (decision, true, nil) for allow/deny, (zero, false, nil) for fallthrough,
// and (zero, false, error) on failure.
func DecidePermission(ctx context.Context, cfg config.Config, input hookctx.HookInput) (PermissionDecision, bool, error) {
	if strings.ToLower(cfg.Provider.Name) != "anthropic" {
		slog.Info("provider not anthropic, skipping", "provider", cfg.Provider.Name)
		return PermissionDecision{}, false, nil
	}

	apiKey, ok := resolveAPIKey()
	if !ok {
		slog.Warn("no API key found (CC_AUTOMODE_ANTHROPIC_API_KEY / ANTHROPIC_API_KEY)")
		return PermissionDecision{}, false, nil
	}

	slog.Info("calling anthropic",
		"model", cfg.Provider.Model,
		"timeout_ms", cfg.Provider.TimeoutMS,
		"tool", input.ToolName,
	)

	output, err := callAnthropic(ctx, cfg, input, apiKey)
	if err != nil {
		slog.Error("anthropic API call failed", "error", err, "tool", input.ToolName)
		return PermissionDecision{}, false, err
	}

	slog.Info("LLM decision",
		"behavior", output.Behavior,
		"deny_message", output.DenyMessage,
		"tool", input.ToolName,
	)

	switch output.Behavior {
	case BehaviorAllow:
		return PermissionDecision{Behavior: BehaviorAllow}, true, nil
	case BehaviorDeny:
		message := strings.TrimSpace(output.DenyMessage)
		if message == "" {
			message = DefaultDenyMessage
		}
		return PermissionDecision{Behavior: BehaviorDeny, Message: message}, true, nil
	case BehaviorFallthrough, "":
		return PermissionDecision{}, false, nil
	default:
		slog.Warn("unexpected LLM behavior", "behavior", output.Behavior)
		return PermissionDecision{}, false, nil
	}
}

func resolveAPIKey() (string, bool) {
	if key := strings.TrimSpace(os.Getenv("CC_AUTOMODE_ANTHROPIC_API_KEY")); key != "" {
		return key, true
	}
	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		return key, true
	}
	return "", false
}
