// Package runner is the entire ccgate PermissionRequest hook
// orchestration. There is no per-target adapter layer: stdin/stdout
// shapes are shared across Claude Code and Codex CLI (both deliver
// session_id / transcript_path / cwd / hook_event_name / tool_name /
// tool_input on stdin and the same hookSpecificOutput.decision shape
// on stdout). Per-target differences are handled here directly:
//
//   - Claude-only fields: permission_mode, permission_suggestions
//   - Codex-only fields: model, turn_id
//
// cmd/<target>/ packages stay tiny -- they only hand a config.LoadOptions
// (where to read the per-user config / write the per-target log+metrics)
// and call Run.
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

// PermissionModePlan is the Claude Code permission_mode value that
// puts ccgate into plan-mode evaluation.
const PermissionModePlan = "plan"

// HookInput is the on-the-wire JSON Claude Code and Codex CLI both
// deliver to the PermissionRequest hook. Fields that only one of the
// two surfaces stay omitempty so the user payload sent to the LLM
// only contains what actually arrived.
type HookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	ToolName       string `json:"tool_name"`

	ToolInput    HookToolInput   `json:"-"`
	ToolInputRaw json.RawMessage `json:"-"`

	// Claude-only
	PermissionMode        string            `json:"permission_mode,omitempty"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions,omitempty"`

	// Codex-only
	Model  string `json:"model,omitempty"`
	TurnID string `json:"turn_id,omitempty"`
}

// HookToolInput is the canonical parsed view of tool_input shared by
// all targets. Fields are emitted unconditionally (no omitempty) so
// the LLM sees the full schema and can address fields by name even
// when the upstream tool left them empty -- the JSON shape itself
// documents what ccgate models. ToolInputRaw carries the verbatim
// upstream bytes alongside this view for tool shapes ccgate has not
// canonicalized yet.
type HookToolInput struct {
	Command        string              `json:"command"`
	Description    string              `json:"description"`
	FilePath       string              `json:"file_path"`
	Path           string              `json:"path"`
	Pattern        string              `json:"pattern"`
	Content        string              `json:"content"`
	ContentUpdates []HookContentUpdate `json:"content_updates"`
}

// HookContentUpdate is the per-hunk Edit / apply_patch payload.
type HookContentUpdate struct {
	OldString string `json:"old_str"`
	NewString string `json:"new_str"`
}

// UnmarshalJSON keeps the raw tool_input bytes around so the LLM sees
// every field the upstream hook delivered (including tool-specific
// shapes ccgate doesn't yet model) while also surfacing the parsed
// fields metrics relies on.
func (h *HookInput) UnmarshalJSON(data []byte) error {
	type alias HookInput
	var raw struct {
		alias
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*h = HookInput(raw.alias)
	h.ToolInputRaw = raw.ToolInput
	if len(raw.ToolInput) > 0 {
		// Best-effort parse: failure is fine for tool shapes ccgate
		// does not yet model. Raw bytes are forwarded to the LLM untouched.
		_ = json.Unmarshal(raw.ToolInput, &h.ToolInput)
	}
	return nil
}

// HookOutput is the JSON response shape Claude Code and Codex both expect.
type HookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName string             `json:"hookEventName"`
	Decision      decisionPayloadOut `json:"decision"`
}

type decisionPayloadOut struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

func newHookOutput(eventName string, d llm.Decision) HookOutput {
	if eventName == "" {
		eventName = "PermissionRequest"
	}
	return HookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName: eventName,
			Decision: decisionPayloadOut{
				Behavior: d.Behavior,
				Message:  d.Message,
			},
		},
	}
}

// Option customises the runner with target-specific extra inputs
// the LLM payload + system prompt should carry. Pass them via Run's
// variadic.
type Option func(*runtimeOptions)

type runtimeOptions struct {
	targetName            string
	promptSection         string
	hasRecentTranscript   bool
	loadStaticPermissions func(cwd string) any
	loadRecentTranscript  func(transcriptPath string) any
}

// WithTargetName labels the host tool in the system prompt header
// (e.g. "Claude Code", "Codex CLI"). The default header text falls
// back to a generic phrasing if unset.
func WithTargetName(name string) Option {
	return func(o *runtimeOptions) { o.targetName = name }
}

// WithPromptSection injects target-specific guidance about which
// payload fields the target delivers and how the LLM should
// interpret them. Inserted between the decision rules and the
// allow/deny lists. Targets that have nothing target-specific to
// say (Codex today) simply do not pass this option.
func WithPromptSection(section string) Option {
	return func(o *runtimeOptions) { o.promptSection = section }
}

// WithHasRecentTranscript declares that the user payload carries a
// `recent_transcript` field. The decision-rule wording adjusts so
// the LLM is not told to consult a field that isn't there. Targets
// that have no equivalent (Codex today) leave this false.
func WithHasRecentTranscript(has bool) Option {
	return func(o *runtimeOptions) { o.hasRecentTranscript = has }
}

// WithStaticPermissions injects a target-specific reader for the
// host tool's static allow/deny patterns (e.g. Claude Code's
// `~/.claude/settings.json` permissions). Returning nil omits the
// payload entry. Targets that have no equivalent (Codex today --
// its `~/.codex/config.toml` prefix_rules ingestion is a separate
// piece of work) simply do not pass this option.
func WithStaticPermissions(fn func(cwd string) any) Option {
	return func(o *runtimeOptions) { o.loadStaticPermissions = fn }
}

// WithRecentTranscript injects a target-specific transcript reader.
// Returning nil omits the payload entry. Targets whose transcript
// format ccgate does not yet model (Codex today) do not pass this
// option.
func WithRecentTranscript(fn func(transcriptPath string) any) Option {
	return func(o *runtimeOptions) { o.loadRecentTranscript = fn }
}

// Run is the entry point. cmd/<target>/ packages call it with a
// per-target config.LoadOptions plus any target-specific Options
// (settings reader, transcript reader, ...). The rest of the flow
// is identical.
func Run(stdin io.Reader, stdout io.Writer, opts config.LoadOptions, runOpts ...Option) int {
	var ro runtimeOptions
	for _, fn := range runOpts {
		fn(&ro)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var input HookInput
	if err := json.NewDecoder(stdin).Decode(&input); err != nil {
		slog.Error("failed to decode stdin", "error", err)
		return 1
	}

	lr, err := config.Load(opts, input.Cwd)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return 1
	}
	cfg := lr.Config

	logger, cleanup := initLogger(cfg.ResolveLogPath(), cfg.IsLogDisabled(), cfg.GetLogMaxSize())
	defer cleanup()
	slog.SetDefault(logger)

	slog.Info("hook invoked",
		"tool", input.ToolName,
		"permission_mode", input.PermissionMode,
		"model", input.Model,
		"turn_id", input.TurnID,
		"config_source", string(lr.Source),
	)

	start := time.Now()
	decision, hasDecision, kind, reason, usage, runErr := decide(ctx, cfg, input, ro)
	elapsed := time.Since(start)

	if !cfg.IsMetricsDisabled() {
		entry := buildMetricsEntry(start, elapsed, input, cfg, decision, hasDecision, kind, reason, usage, runErr)
		metrics.Record(cfg.ResolveMetricsPath(), cfg.GetMetricsMaxSize(), entry)
	}

	if runErr != nil {
		slog.Error("decide failed", "error", runErr, "tool", input.ToolName, "elapsed_ms", elapsed.Milliseconds())
		return 1
	}
	if !hasDecision {
		slog.Info("no decision (fallthrough)", "kind", kind, "tool", input.ToolName, "elapsed_ms", elapsed.Milliseconds())
		return 0
	}

	slog.Info("decision made", "behavior", decision.Behavior, "message", decision.Message, "tool", input.ToolName, "elapsed_ms", elapsed.Milliseconds())
	if err := json.NewEncoder(stdout).Encode(newHookOutput(input.HookEventName, decision)); err != nil {
		slog.Error("failed to encode response to stdout", "error", err)
		return 1
	}
	return 0
}

func decide(ctx context.Context, cfg config.Config, in HookInput, ro runtimeOptions) (llm.Decision, bool, string, string, *llm.Usage, error) {
	// Tools that require user interaction must never be auto-decided.
	switch in.ToolName {
	case "ExitPlanMode", "AskUserQuestion":
		slog.Info("user interaction tool: falling through", "tool", in.ToolName)
		return llm.Decision{}, false, llm.FallthroughKindUserInteraction, "", nil, nil
	}

	// Some permission modes hand the prompt back to the upstream tool
	// regardless of the LLM's opinion.
	switch in.PermissionMode {
	case PermissionModePlan:
		// fall through to LLM
	case "bypassPermissions":
		slog.Info("bypass mode: falling through", "tool", in.ToolName)
		return llm.Decision{}, false, llm.FallthroughKindBypass, "", nil, nil
	case "dontAsk":
		slog.Info("dontAsk mode: falling through", "tool", in.ToolName)
		return llm.Decision{}, false, llm.FallthroughKindDontAsk, "", nil, nil
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

	p, err := buildPrompt(cfg, in, ro)
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

// PromptInput is the user-message JSON the LLM sees. It is the
// shared payload shape across targets: fields that one of the two
// targets does not deliver carry omitempty so they disappear from
// the JSON instead of showing up as empty values. The prompt's
// TargetSection (set via WithPromptSection) tells the LLM which
// fields are / are not present.
type PromptInput struct {
	ToolName              string            `json:"tool_name"`
	ToolInput             HookToolInput     `json:"tool_input"`
	ToolInputRaw          json.RawMessage   `json:"tool_input_raw,omitempty"`
	PermissionMode        string            `json:"permission_mode,omitempty"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions,omitempty"`
	Context               Context           `json:"context"`
	SettingsPermissions   any               `json:"settings_permissions,omitempty"`
	RecentTranscript      any               `json:"recent_transcript,omitempty"`
	Model                 string            `json:"model,omitempty"`
	TurnID                string            `json:"turn_id,omitempty"`
}

// Context bundles the working-directory + git information ccgate
// surfaces to the LLM together with the tool-derived
// referenced_paths. Wraps gitutil.Context with the path-extraction
// output so both live in one nested object the LLM can navigate by
// name.
type Context struct {
	gitutil.Context
	ReferencedPaths []string `json:"referenced_paths,omitempty"`
}

func buildPrompt(cfg config.Config, in HookInput, ro runtimeOptions) (llm.Prompt, error) {
	pi := PromptInput{
		ToolName:              in.ToolName,
		ToolInput:             in.ToolInput,
		ToolInputRaw:          in.ToolInputRaw,
		PermissionMode:        in.PermissionMode,
		PermissionSuggestions: in.PermissionSuggestions,
		Model:                 in.Model,
		TurnID:                in.TurnID,
		Context: Context{
			Context:         gitutil.BuildContext(in.Cwd),
			ReferencedPaths: referencedPaths(in),
		},
	}
	if ro.loadStaticPermissions != nil {
		pi.SettingsPermissions = ro.loadStaticPermissions(in.Cwd)
	}
	if ro.loadRecentTranscript != nil && in.TranscriptPath != "" {
		pi.RecentTranscript = ro.loadRecentTranscript(in.TranscriptPath)
	}
	user, err := json.MarshalIndent(pi, "", "  ")
	if err != nil {
		return llm.Prompt{}, fmt.Errorf("marshal prompt input: %w", err)
	}
	p := prompt.Build(prompt.Args{
		TargetName:          ro.targetName,
		PlanMode:            in.PermissionMode == PermissionModePlan,
		HasRecentTranscript: ro.hasRecentTranscript,
		TargetSection:       ro.promptSection,
		Allow:               cfg.Allow,
		Deny:                cfg.Deny,
		Environment:         cfg.Environment,
		UserPayload:         string(user),
	})
	p.Model = cfg.Provider.Model
	p.TimeoutMS = cfg.GetTimeoutMS()
	return p, nil
}

func redactedUserMessage(user string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(user), &m); err != nil {
		return "{}"
	}
	// tool_input_raw carries the verbatim upstream payload alongside
	// the parsed view -- redact both so file bodies (Edit/Write
	// content_updates) and Bash arguments (tokens, secrets) never
	// reach ccgate.log even when the parsed view alone would have.
	for _, k := range []string{"tool_input", "tool_input_raw", "permission_suggestions", "recent_transcript"} {
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
	in HookInput,
	cfg config.Config,
	decision llm.Decision,
	hasDecision bool,
	kind string,
	llmReason string,
	usage *llm.Usage,
	err error,
) metrics.Entry {
	entry := metrics.Entry{
		Timestamp:      start,
		SessionID:      in.SessionID,
		ToolName:       in.ToolName,
		PermissionMode: in.PermissionMode,
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
		Command:  in.ToolInput.Command,
		FilePath: in.ToolInput.FilePath,
		Path:     in.ToolInput.Path,
		Pattern:  in.ToolInput.Pattern,
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
