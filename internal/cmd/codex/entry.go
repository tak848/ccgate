package codex

import (
	"time"

	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/metrics"
)

const maxTruncateLen = 200

// buildMetricsEntry produces the per-invocation JSONL record for the
// Codex hook. Field shape is a strict subset of metrics.Entry so the
// existing reader/aggregator (`ccgate codex metrics`) reuses the
// Claude Code report code path verbatim. Codex-specific fields
// (turn_id, model from the AI side) are exposed via the existing
// Reason/Model channels so the JSONL stays uniform across targets.
func buildMetricsEntry(
	start time.Time,
	elapsed time.Duration,
	input HookInput,
	model string,
	decision llm.Decision,
	hasDecision bool,
	fallthroughKind string,
	llmReason string,
	usage *llm.Usage,
	err error,
) metrics.Entry {
	entry := metrics.Entry{
		Timestamp: start,
		SessionID: input.SessionID,
		ToolName:  input.ToolName,
		// Codex has no permission_mode field upstream today; leaving
		// PermissionMode empty so old aggregations don't see a noisy
		// "default" bucket they never had before.
		Model:     model,
		ElapsedMS: elapsed.Milliseconds(),
	}

	switch {
	case err != nil:
		entry.Decision = "error"
		entry.Error = truncateStr(err.Error(), maxTruncateLen)
	case hasDecision:
		entry.Decision = decision.Behavior
		if decision.Behavior == llm.BehaviorDeny {
			entry.DenyMessage = decision.Message
		}
		entry.Reason = truncateStr(llmReason, maxTruncateLen)
		if fallthroughKind == llm.FallthroughKindLLM {
			entry.FallthroughKind = fallthroughKind
			entry.Forced = true
		}
	default:
		entry.Decision = "fallthrough"
		entry.FallthroughKind = fallthroughKind
		entry.Reason = truncateStr(llmReason, maxTruncateLen)
	}

	if usage != nil {
		entry.InputTokens = usage.InputTokens
		entry.OutputTokens = usage.OutputTokens
	}

	entry.ToolInput = metrics.CapToolInput(metrics.ToolInputFields{
		Command: input.ToolInput.Command,
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
