package claude

import (
	"encoding/json"
	"io"
	"log/slog"

	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/runner"
)

// PermissionModePlan is the Claude Code permission_mode value that
// puts ccgate into plan-mode evaluation.
const PermissionModePlan = "plan"

// target returns the runner.Target adapter for the Claude Code hook.
// Everything that is genuinely Claude-specific (HookInput shape,
// settings.json reading, transcript loading, plan mode, the
// ExitPlanMode / AskUserQuestion / bypassPermissions / dontAsk
// short-circuits, the wire output shape) lives in this package and
// is wired here. The orchestration itself runs in internal/runner.
func target() runner.Target {
	return runner.Target{
		Name:         "claude",
		LoadOptions:  LoadOptions,
		DecodeInput:  decodeInput,
		Prefilter:    prefilter,
		ExtraPayload: extraPayload,
		PlanMode:     planMode,
		RenderOutput: renderOutput,
	}
}

func decodeInput(r io.Reader) (runner.Request, error) {
	var hi HookInput
	if err := json.NewDecoder(r).Decode(&hi); err != nil {
		return runner.Request{}, err
	}
	cmd, fp, path, pattern := hi.MetricsFields()
	return runner.Request{
		SessionID:           hi.SessionID,
		ToolName:            hi.ToolName,
		Cwd:                 hi.Cwd,
		PermissionMode:      hi.PermissionMode,
		ToolInputRaw:        hi.ToolInputRaw,
		TranscriptPath:      hi.TranscriptPath,
		MetricsToolCommand:  cmd,
		MetricsToolFilePath: fp,
		MetricsToolPath:     path,
		MetricsToolPattern:  pattern,
		Extras:              hi,
	}, nil
}

func prefilter(r runner.Request) runner.PrefilterResult {
	switch r.ToolName {
	case "ExitPlanMode", "AskUserQuestion":
		slog.Info("user interaction tool: falling through", "tool", r.ToolName)
		return runner.PrefilterResult{Skip: true, FallthroughKind: llm.FallthroughKindUserInteraction}
	}
	switch r.PermissionMode {
	case "bypassPermissions":
		slog.Info("bypass mode: falling through", "tool", r.ToolName)
		return runner.PrefilterResult{Skip: true, FallthroughKind: llm.FallthroughKindBypass}
	case "dontAsk":
		slog.Info("dontAsk mode: falling through", "tool", r.ToolName)
		return runner.PrefilterResult{Skip: true, FallthroughKind: llm.FallthroughKindDontAsk}
	}
	return runner.PrefilterResult{}
}

func planMode(r runner.Request) bool { return r.PermissionMode == PermissionModePlan }

// extraPayload adds Claude-specific context fields the LLM benefits
// from (and the rest of the runner does not deliver): the user's
// `~/.claude/settings.json` static patterns, the recent transcript
// tail, the per-tool referenced paths, and any structured
// `permission_suggestions` Claude Code attached to the request.
func extraPayload(r runner.Request) map[string]any {
	hi, _ := r.Extras.(HookInput)
	extra := map[string]any{
		"settings_permissions": LoadSettingsPermissions(hi.Cwd),
		"referenced_paths":     referencedPaths(hi),
	}
	if hi.TranscriptPath != "" {
		if t, err := LoadRecentTranscript(hi.TranscriptPath); err == nil {
			extra["recent_transcript"] = t
		} else {
			slog.Warn("failed to load transcript, proceeding without it", "error", err)
		}
	}
	if len(hi.PermissionSuggestions) > 0 {
		extra["permission_suggestions"] = hi.PermissionSuggestions
	}
	return extra
}

func renderOutput(w io.Writer, d llm.Decision) error {
	return json.NewEncoder(w).Encode(NewPermissionResponse(d))
}
