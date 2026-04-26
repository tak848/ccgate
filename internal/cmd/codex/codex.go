// Package codex implements the OpenAI Codex CLI PermissionRequest hook.
//
// Same shape as cmd/claude (Run / Init / Metrics) but configured for
// Codex's stdin schema and writing to a per-target log/metrics path.
// Codex hooks are upstream-experimental.
package codex

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
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
	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/llm/anthropic"
	"github.com/tak848/ccgate/internal/metrics"
)

//go:embed defaults.jsonnet
var defaultsJsonnet string

// Defaults exposes the embedded Codex defaults so cli/ can output them
// from `ccgate codex init`.
func Defaults() string { return defaultsJsonnet }

// LoadOptions builds the config.LoadOptions for the Codex hook.
// Project-local config is read from `{repo_root}/.codex/ccgate.local.jsonnet`
// only — root-level `ccgate.local.jsonnet` is target-ambiguous and not
// read for either target in v0.5.
func LoadOptions() config.LoadOptions {
	home, _ := os.UserHomeDir()
	sd := stateDir()
	return config.LoadOptions{
		GlobalConfigPath:          filepath.Join(home, ".codex", config.BaseConfigName),
		ProjectLocalRelativePaths: []string{filepath.Join(".codex", config.LocalConfigName)},
		EmbedDefaults:             defaultsJsonnet,
		DefaultLogPath:            filepath.Join(sd, "ccgate.log"),
		DefaultMetricsPath:        filepath.Join(sd, "metrics.jsonl"),
	}
}

// stateDir is the per-user state subdirectory for Codex log/metrics
// (i.e. $XDG_STATE_HOME/ccgate/codex/...).
func stateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "ccgate", "codex")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "ccgate", "codex")
	}
	return filepath.Join(".", "codex")
}

// Run reads a single PermissionRequest from stdin, decides allow / deny /
// fallthrough, and writes the response to stdout.
func Run(stdin io.Reader, stdout io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var input HookInput
	if err := json.NewDecoder(stdin).Decode(&input); err != nil {
		slog.Error("failed to decode stdin", "error", err)
		return 1
	}

	lr, err := config.Load(LoadOptions(), input.Cwd)
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
		"tool", input.ToolName,
		"model", input.Model,
		"turn_id", input.TurnID,
		"config_source", string(lr.Source),
	)

	start := time.Now()
	decision, hasDecision, fallthroughKind, llmReason, usage, runErr := runHook(ctx, cfg, input)
	elapsed := time.Since(start)

	if !cfg.IsMetricsDisabled() {
		entry := buildMetricsEntry(start, elapsed, input, cfg.Provider.Model, decision, hasDecision, fallthroughKind, llmReason, usage, runErr)
		metrics.Record(cfg.ResolveMetricsPath(), cfg.GetMetricsMaxSize(), entry)
	}

	if runErr != nil {
		slog.Error("decide failed", "error", runErr, "elapsed_ms", elapsed.Milliseconds())
		return 1
	}
	if !hasDecision {
		slog.Info("no decision (fallthrough)", "fallthrough_kind", fallthroughKind, "elapsed_ms", elapsed.Milliseconds())
		return 0
	}

	slog.Info("decision made",
		"behavior", decision.Behavior,
		"message", decision.Message,
		"elapsed_ms", elapsed.Milliseconds(),
	)

	resp := NewPermissionResponse(decision)
	if err := json.NewEncoder(stdout).Encode(resp); err != nil {
		slog.Error("failed to encode response to stdout", "error", err)
		return 1
	}
	return 0
}

// runHook is the LLM-call portion of Run, factored out so the metrics
// recording in Run handles every error/branch identically. Returns
// (decision, hasDecision, fallthroughKind, llmReason, usage, err).
func runHook(ctx context.Context, cfg config.Config, input HookInput) (llm.Decision, bool, string, string, *llm.Usage, error) {
	if pr := runPrefilter(input); pr.Skip {
		return llm.Decision{}, false, pr.FallthroughKind, pr.Reason, nil, nil
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

	p, err := buildPrompt(cfg, input)
	if err != nil {
		return llm.Decision{}, false, "", "", nil, fmt.Errorf("build prompt: %w", err)
	}

	slog.Info("anthropic request",
		"model", p.Model,
		"timeout_ms", p.TimeoutMS,
		"system_prompt", p.System,
		"user_message", p.User,
	)

	client := &anthropic.Client{APIKey: apiKey}
	res, err := client.Decide(ctx, p)
	if err != nil {
		if errors.Is(err, anthropic.ErrNoAPIKey) {
			return llm.Decision{}, false, llm.FallthroughKindNoAPIKey, "", res.Usage, nil
		}
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

func resolveAPIKey() (string, bool) {
	if key := strings.TrimSpace(os.Getenv("CCGATE_ANTHROPIC_API_KEY")); key != "" {
		return key, true
	}
	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		return key, true
	}
	return "", false
}

// InitOptions describes how `ccgate codex init` should output the
// embedded defaults.
type InitOptions struct {
	Output string
	Force  bool
}

// Init writes the embedded Codex defaults to stdout or opts.Output.
func Init(stdout io.Writer, stderr io.Writer, opts InitOptions) int {
	if opts.Output == "" {
		fmt.Fprint(stdout, defaultsJsonnet)
		return 0
	}
	if !opts.Force {
		if _, err := os.Stat(opts.Output); err == nil {
			fmt.Fprintf(stderr, "error: file already exists: %s (use -f to overwrite)\n", opts.Output)
			return 1
		}
	}
	dir := filepath.Dir(opts.Output)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "error: failed to create directory %s: %v\n", dir, err)
		return 1
	}
	if err := os.WriteFile(opts.Output, []byte(defaultsJsonnet), 0o644); err != nil {
		fmt.Fprintf(stderr, "error: failed to write file %s: %v\n", opts.Output, err)
		return 1
	}
	fmt.Fprintf(stderr, "wrote %s\n", opts.Output)
	return 0
}

// MetricsOptions controls `ccgate codex metrics`.
type MetricsOptions struct {
	Days       int
	AsJSON     bool
	DetailsTop int
}

// Metrics aggregates the Codex metrics file and prints the report
// to stdout.
func Metrics(stdout io.Writer, stderr io.Writer, cwd string, opts MetricsOptions) int {
	lr, err := config.Load(LoadOptions(), cwd)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	if err := metrics.PrintReport(stdout, []string{lr.Config.ResolveMetricsPath()}, metrics.ReportOptions{
		Days:       opts.Days,
		AsJSON:     opts.AsJSON,
		DetailsTop: opts.DetailsTop,
	}); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
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
