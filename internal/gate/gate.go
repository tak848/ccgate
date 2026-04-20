package gate

import (
	"context"
	"log/slog"
	"os"
	"strconv"
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
//
// FallthroughKindAPIUnusable means the API truncated/refused the response
// or returned no parseable text. It is intentionally NOT subject to
// fallthrough_strategy because the LLM never actually expressed an
// uncertain decision — auto-allowing on a refused/truncated response
// would silently weaken security.
const (
	FallthroughKindUserInteraction = "user_interaction"
	FallthroughKindBypass          = "bypass"
	FallthroughKindDontAsk         = "dontask"
	FallthroughKindNonAnthropic    = "non_anthropic"
	FallthroughKindNoAPIKey        = "no_apikey"
	FallthroughKindLLM             = "llm"
	FallthroughKindAPIUnusable     = "api_unusable"
)

type PermissionDecision struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

// DecisionResult is the rich result from DecidePermission.
// Invariants:
//   - HasDecision=true && FallthroughKind=="": LLM (or upstream) returned a clear allow/deny
//   - HasDecision=true && FallthroughKind=="llm": LLM was uncertain but fallthrough_strategy forced a decision
//   - HasDecision=false: real fallthrough; FallthroughKind describes why
//   - Usage is non-nil only when an API call was made
type DecisionResult struct {
	Decision        PermissionDecision
	HasDecision     bool
	FallthroughKind string    // why fallthrough: user_interaction, bypass, dontask, no_apikey, non_anthropic, llm, api_unusable
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

	callResult, err := callAnthropic(ctx, cfg, input, apiKey)
	if err != nil {
		slog.Error("anthropic API call failed", "error", err, "tool", input.ToolName)
		return DecisionResult{Usage: callResult.Usage}, err
	}

	if !callResult.Unusable {
		slog.Info("LLM decision",
			"behavior", callResult.Output.Behavior,
			"reason", callResult.Output.Reason,
			"deny_message", callResult.Output.DenyMessage,
			"tool", input.ToolName,
		)
	}

	return decideFromLLMResult(cfg, callResult), nil
}

// decideFromLLMResult turns a single LLM call result into the final
// DecisionResult. Split out from DecidePermission so that the
// fallthrough_strategy promotion rules can be exercised by tests
// without spinning up the Anthropic client.
func decideFromLLMResult(cfg config.Config, callResult LLMCallResult) DecisionResult {
	// API truncated/refused: NOT an LLM uncertainty signal, so
	// fallthrough_strategy must not promote it to allow/deny.
	if callResult.Unusable {
		return DecisionResult{
			Usage:           callResult.Usage,
			FallthroughKind: FallthroughKindAPIUnusable,
		}
	}

	output := callResult.Output
	base := DecisionResult{
		Usage:     callResult.Usage,
		LLMReason: output.Reason,
	}

	switch output.Behavior {
	case BehaviorAllow:
		base.Decision = PermissionDecision{Behavior: BehaviorAllow}
		base.HasDecision = true
		return base
	case BehaviorDeny:
		message := strings.TrimSpace(output.DenyMessage)
		if message == "" {
			message = DefaultDenyMessage
		}
		base.Decision = PermissionDecision{Behavior: BehaviorDeny, Message: message}
		base.HasDecision = true
		return base
	case BehaviorFallthrough, "":
		base.FallthroughKind = FallthroughKindLLM
		if d, ok := applyForcedStrategy(cfg, output.Reason); ok {
			base.Decision = d
			base.HasDecision = true
		}
		return base
	default:
		slog.Warn("unexpected LLM behavior", "behavior", output.Behavior)
		base.FallthroughKind = FallthroughKindLLM
		if d, ok := applyForcedStrategy(cfg, output.Reason); ok {
			base.Decision = d
			base.HasDecision = true
		}
		return base
	}
}

// applyForcedStrategy converts an LLM fallthrough into a forced allow/deny
// based on cfg.FallthroughStrategy. Returns ok=false when the strategy is
// "ask" (or unset), preserving the original fallthrough behavior.
//
// On the message field: Claude Code's PermissionRequest spec only delivers
// decision.message to Claude when behavior is "deny" (allow-side message is
// silently ignored as of 2026-04-20, see
// https://code.claude.com/docs/en/hooks). We still populate the allow
// message so that (a) it shows up in our own logs / metrics for auditing
// and (b) it works as a forward-compatible hint if Claude Code ever starts
// delivering allow-side messages.
func applyForcedStrategy(cfg config.Config, llmReason string) (PermissionDecision, bool) {
	switch cfg.GetFallthroughStrategy() {
	case config.FallthroughStrategyAllow:
		return PermissionDecision{
			Behavior: BehaviorAllow,
			Message:  buildForcedMessage(BehaviorAllow, llmReason),
		}, true
	case config.FallthroughStrategyDeny:
		return PermissionDecision{
			Behavior: BehaviorDeny,
			Message:  buildForcedMessage(BehaviorDeny, llmReason),
		}, true
	default:
		return PermissionDecision{}, false
	}
}

// buildForcedMessage explains to Claude that the hook auto-decided what
// would normally have prompted the user. The wording covers: who decided
// (an LLM-based permission hook), what the hook actually returned
// (fallthrough), why that became a fixed decision (to keep unattended
// automation running), and — for deny — that Claude must not ask the user
// or work around the restriction.
func buildForcedMessage(behavior, llmReason string) string {
	reason := strings.TrimSpace(llmReason)
	var head string
	if reason == "" {
		head = "LLM-based permission hook returned fallthrough."
	} else {
		// strconv.Quote escapes embedded quotes/newlines so the message
		// stays unambiguous regardless of what the LLM emitted.
		head = "LLM-based permission hook returned fallthrough; LLM reason: " + strconv.Quote(reason) + "."
	}

	switch behavior {
	case BehaviorAllow:
		return head + " Auto-approved to keep unattended automation running — proceed with care."
	case BehaviorDeny:
		return head + " Auto-denied for safety to keep unattended automation running — do not ask the user, and do not attempt to bypass this decision via alternative commands or workarounds."
	}
	return head
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
