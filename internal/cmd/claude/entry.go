package claude

import (
	"time"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/gate"
	"github.com/tak848/ccgate/internal/hookctx"
	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/metrics"
)

const maxTruncateLen = 200

// buildMetricsEntry builds the per-invocation metrics record for the
// Claude Code hook. Mirrors the legacy main.buildMetricsEntry verbatim
// — it lives here now so cmd/claude owns both the hook orchestration
// and its metrics shape.
func buildMetricsEntry(start time.Time, elapsed time.Duration, input hookctx.HookInput, cfg config.Config, result gate.DecisionResult, err error) metrics.Entry {
	entry := metrics.Entry{
		Timestamp:      start,
		SessionID:      input.SessionID,
		ToolName:       input.ToolName,
		PermissionMode: input.PermissionMode,
		Model:          cfg.Provider.Model,
		ElapsedMS:      elapsed.Milliseconds(),
	}

	if err != nil {
		entry.Decision = "error"
		entry.Error = truncateStr(err.Error(), maxTruncateLen)
	} else if result.HasDecision {
		entry.Decision = result.Decision.Behavior
		// deny_msg historically only carried deny rationale; do not
		// pollute it with the forced-allow explanation message.
		if result.Decision.Behavior == gate.BehaviorDeny {
			entry.DenyMessage = result.Decision.Message
		}
		entry.Reason = truncateStr(result.LLMReason, maxTruncateLen)
		// LLM was uncertain but fallthrough_strategy forced a decision.
		if result.FallthroughKind == llm.FallthroughKindLLM {
			entry.FallthroughKind = result.FallthroughKind
			entry.Forced = true
		}
	} else {
		entry.Decision = "fallthrough"
		entry.FallthroughKind = result.FallthroughKind
		entry.Reason = truncateStr(result.LLMReason, maxTruncateLen)
	}

	if result.Usage != nil {
		entry.InputTokens = result.Usage.InputTokens
		entry.OutputTokens = result.Usage.OutputTokens
	}

	cmd, fp, path, pattern := input.MetricsFields()
	entry.ToolInput = metrics.CapToolInput(metrics.ToolInputFields{
		Command:  cmd,
		FilePath: fp,
		Path:     path,
		Pattern:  pattern,
	})

	return entry
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
