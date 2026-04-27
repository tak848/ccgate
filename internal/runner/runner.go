// Package runner is the target-agnostic orchestration for ccgate
// PermissionRequest hooks. Targets (Claude Code, Codex CLI, ...)
// supply only the parts that genuinely differ:
//
//   - the wire format on stdin / stdout (Decode, Render),
//   - the embedded config (LoadOptions),
//   - and target-specific prefilters / extra context (Prefilter, ExtraPayload).
//
// Everything else -- provider/API-key check, prompt assembly, LLM
// call, fallthrough_strategy resolution, metrics entry shape, log
// initialisation -- lives here.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/gitutil"
	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/llm/anthropic"
	"github.com/tak848/ccgate/internal/metrics"
	"github.com/tak848/ccgate/internal/prompt"
)

// Request is the target-agnostic view of a single PermissionRequest
// invocation. Targets fill the fields they actually receive; the
// rest stay zero-valued and are simply omitted from the LLM payload.
type Request struct {
	SessionID      string
	ToolName       string
	Cwd            string
	PermissionMode string // Claude only today
	Description    string
	Model          string          // upstream AI model (Codex surfaces this)
	TurnID         string          // Codex
	TranscriptPath string          // Claude reads, Codex passes through
	ToolInputRaw   json.RawMessage // forwarded to the LLM verbatim

	// MetricsTool* expose a small parsed view of tool_input for the
	// metrics layer. Targets fill whichever fields make sense.
	MetricsToolCommand  string
	MetricsToolFilePath string
	MetricsToolPath     string
	MetricsToolPattern  string

	// Extras carries target-internal data DecodeInput already parsed
	// (e.g. the full HookInput struct) so target hooks like
	// ExtraPayload don't have to reparse stdin. The runner itself
	// never reads it.
	Extras any
}

// PrefilterResult lets a target short-circuit before the LLM is
// called. Skip=true means the runner returns immediately with the
// recorded fallthrough kind (no LLM call, no decision).
type PrefilterResult struct {
	Skip            bool
	FallthroughKind string
}

// Target is the per-target adapter. All fields are required.
type Target struct {
	// Name appears in log lines and Spec Ledger references.
	Name string

	// LoadOptions is the config layer (global path, project-local
	// candidates, embedded defaults, default log/metrics paths).
	LoadOptions func() config.LoadOptions

	// DecodeInput reads stdin and returns a Request the runner can
	// orchestrate.
	DecodeInput func(io.Reader) (Request, error)

	// Prefilter runs before the provider check. Use it for
	// target-specific short-circuits (Claude's
	// ExitPlanMode/AskUserQuestion, bypassPermissions, dontAsk).
	// Return Skip=false to continue into the LLM call.
	Prefilter func(Request) PrefilterResult

	// ExtraPayload returns extra JSON fields injected into the user
	// message above and beyond the common (tool_name, tool_input,
	// cwd, context, ...) shape. Used by Claude to ship
	// settings_permissions / recent_transcript / permission_suggestions.
	// Return nil for targets that have nothing extra.
	ExtraPayload func(Request) map[string]any

	// PlanMode toggles the plan-mode decision rules in the system
	// prompt. Only Claude returns true today.
	PlanMode func(Request) bool

	// RenderOutput writes the resolved decision to stdout in the
	// target's wire format.
	RenderOutput func(io.Writer, llm.Decision) error
}

// Run is the binary entry point for a target. main() / cli wires the
// Target struct and hands it here.
func Run(stdin io.Reader, stdout io.Writer, t Target) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	req, err := t.DecodeInput(stdin)
	if err != nil {
		slog.Error("failed to decode stdin", "error", err)
		return 1
	}

	lr, err := config.Load(t.LoadOptions(), req.Cwd)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return 1
	}
	cfg := lr.Config

	logger, cleanup := initLogger(cfg.ResolveLogPath(), cfg.IsLogDisabled(), cfg.GetLogMaxSize())
	defer cleanup()
	slog.SetDefault(logger)

	if lr.Source == config.SourceGlobalConfig && len(cfg.Allow) == 0 && len(cfg.Deny) == 0 {
		slog.Warn("allow and deny rules are both empty; embedded defaults were not applied because a global config exists")
	}

	slog.Info("hook invoked",
		"target", t.Name,
		"tool", req.ToolName,
		"permission_mode", req.PermissionMode,
		"config_source", string(lr.Source),
	)

	start := time.Now()
	decision, hasDecision, kind, reason, usage, runErr := decide(ctx, t, cfg, req)
	elapsed := time.Since(start)

	if !cfg.IsMetricsDisabled() {
		entry := buildMetricsEntry(start, elapsed, t.Name, req, cfg, decision, hasDecision, kind, reason, usage, runErr)
		metrics.Record(cfg.ResolveMetricsPath(), cfg.GetMetricsMaxSize(), entry)
	}

	if runErr != nil {
		slog.Error("decide failed", "error", runErr, "tool", req.ToolName, "elapsed_ms", elapsed.Milliseconds())
		return 1
	}
	if !hasDecision {
		slog.Info("no decision (fallthrough)", "kind", kind, "tool", req.ToolName, "elapsed_ms", elapsed.Milliseconds())
		return 0
	}

	slog.Info("decision made", "behavior", decision.Behavior, "message", decision.Message, "tool", req.ToolName, "elapsed_ms", elapsed.Milliseconds())
	if err := t.RenderOutput(stdout, decision); err != nil {
		slog.Error("failed to render output", "error", err)
		return 1
	}
	return 0
}

func decide(ctx context.Context, t Target, cfg config.Config, req Request) (llm.Decision, bool, string, string, *llm.Usage, error) {
	if pr := t.Prefilter(req); pr.Skip {
		return llm.Decision{}, false, pr.FallthroughKind, "", nil, nil
	}

	if !strings.EqualFold(cfg.Provider.Name, "anthropic") {
		slog.Info("provider not anthropic, falling through", "provider", cfg.Provider.Name)
		return llm.Decision{}, false, llm.FallthroughKindNonAnthropic, "", nil, nil
	}

	apiKey, ok := resolveAPIKey()
	if !ok {
		slog.Warn("no API key found (CCGATE_ANTHROPIC_API_KEY / ANTHROPIC_API_KEY)")
		return llm.Decision{}, false, llm.FallthroughKindNoAPIKey, "", nil, nil
	}

	p, err := buildUserPrompt(t, cfg, req)
	if err != nil {
		return llm.Decision{}, false, "", "", nil, fmt.Errorf("build prompt: %w", err)
	}

	slog.Info("anthropic request",
		"model", p.Model,
		"timeout_ms", p.TimeoutMS,
		"system_prompt", p.System,
		"user_message", redactedUserMessage(p.User),
	)

	client := &anthropic.Client{APIKey: apiKey}
	res, err := client.Decide(ctx, p)
	if err != nil {
		return llm.Decision{}, false, "", "", res.Usage, err
	}
	if res.Unusable {
		return llm.Decision{}, false, llm.FallthroughKindAPIUnusable, "", res.Usage, nil
	}

	switch res.Output.Behavior {
	case llm.BehaviorAllow:
		return llm.Decision{Behavior: llm.BehaviorAllow}, true, "", res.Output.Reason, res.Usage, nil
	case llm.BehaviorDeny:
		msg := strings.TrimSpace(res.Output.DenyMessage)
		if msg == "" {
			msg = llm.DefaultDenyMessage
		}
		return llm.Decision{Behavior: llm.BehaviorDeny, Message: msg}, true, "", res.Output.Reason, res.Usage, nil
	case llm.BehaviorFallthrough, "":
		if d, ok := llm.ApplyStrategy(cfg.GetFallthroughStrategy(), res.Output.Reason); ok {
			return d, true, llm.FallthroughKindLLM, res.Output.Reason, res.Usage, nil
		}
		return llm.Decision{}, false, llm.FallthroughKindLLM, res.Output.Reason, res.Usage, nil
	default:
		slog.Warn("unexpected LLM behavior", "behavior", res.Output.Behavior)
		if d, ok := llm.ApplyStrategy(cfg.GetFallthroughStrategy(), res.Output.Reason); ok {
			return d, true, llm.FallthroughKindLLM, res.Output.Reason, res.Usage, nil
		}
		return llm.Decision{}, false, llm.FallthroughKindLLM, res.Output.Reason, res.Usage, nil
	}
}

func buildUserPrompt(t Target, cfg config.Config, req Request) (llm.Prompt, error) {
	payload := map[string]any{
		"tool_name":       req.ToolName,
		"tool_input":      req.ToolInputRaw,
		"cwd":             req.Cwd,
		"context":         gitutil.BuildContext(req.Cwd),
		"permission_mode": req.PermissionMode,
		"description":     req.Description,
		"model":           req.Model,
		"turn_id":         req.TurnID,
	}
	if t.ExtraPayload != nil {
		for k, v := range t.ExtraPayload(req) {
			payload[k] = v
		}
	}
	user, err := json.MarshalIndent(stripEmpty(payload), "", "  ")
	if err != nil {
		return llm.Prompt{}, fmt.Errorf("marshal prompt input: %w", err)
	}
	p := prompt.Build(prompt.Args{
		PlanMode:    t.PlanMode(req),
		Allow:       cfg.Allow,
		Deny:        cfg.Deny,
		Environment: cfg.Environment,
		UserPayload: string(user),
	})
	p.Model = cfg.Provider.Model
	p.TimeoutMS = cfg.GetTimeoutMS()
	return p, nil
}

// stripEmpty removes keys whose value is the type's zero so the
// payload sent to the LLM only contains fields that actually came in.
// Targets that don't deliver permission_mode / model / turn_id
// shouldn't surface those keys at all.
func stripEmpty(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if x == "" {
				continue
			}
		case json.RawMessage:
			if len(x) == 0 {
				continue
			}
		case nil:
			continue
		}
		out[k] = v
	}
	return out
}

// redactedUserMessage strips the high-volume / potentially sensitive
// pieces (raw tool_input bodies, content, content_updates) from the
// user message before it is logged. The full payload still goes to
// the LLM; only the local log file is sanitised.
func redactedUserMessage(user string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(user), &m); err != nil {
		return "{}"
	}
	for _, k := range []string{"tool_input", "tool_input_raw", "content", "content_updates", "permission_suggestions", "recent_transcript"} {
		if _, ok := m[k]; ok {
			m[k] = "[REDACTED]"
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(out)
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

const maxTruncateLen = 200

func buildMetricsEntry(
	start time.Time,
	elapsed time.Duration,
	targetName string,
	req Request,
	cfg config.Config,
	decision llm.Decision,
	hasDecision bool,
	kind string,
	llmReason string,
	usage *llm.Usage,
	err error,
) metrics.Entry {
	_ = targetName // metrics file is per-target on disk, no need to encode it inline
	entry := metrics.Entry{
		Timestamp:      start,
		SessionID:      req.SessionID,
		ToolName:       req.ToolName,
		PermissionMode: req.PermissionMode,
		Model:          cfg.Provider.Model,
		ElapsedMS:      elapsed.Milliseconds(),
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
		if kind == llm.FallthroughKindLLM {
			entry.FallthroughKind = kind
			entry.Forced = true
		}
	default:
		entry.Decision = "fallthrough"
		entry.FallthroughKind = kind
		entry.Reason = truncateStr(llmReason, maxTruncateLen)
	}

	if usage != nil {
		entry.InputTokens = usage.InputTokens
		entry.OutputTokens = usage.OutputTokens
	}

	entry.ToolInput = metrics.CapToolInput(metrics.ToolInputFields{
		Command:  req.MetricsToolCommand,
		FilePath: req.MetricsToolFilePath,
		Path:     req.MetricsToolPath,
		Pattern:  req.MetricsToolPattern,
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

func initLogger(logPath string, disabled bool, maxLogSize int64) (*slog.Logger, func()) {
	if disabled {
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1})), func() {}
	}
	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		slog.Warn("failed to create log directory", "error", err)
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {}
	}
	metrics.RotateIfNeeded(logPath, maxLogSize)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to open log file %s: %v\n", logPath, err)
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {}
	}
	w := &atomicWriter{f: f}
	return slog.New(slog.NewTextHandler(w, nil)), func() { _ = f.Close() }
}

type atomicWriter struct {
	f  *os.File
	mu sync.Mutex
}

func (w *atomicWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Write(p)
}
