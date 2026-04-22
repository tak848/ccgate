package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"golang.org/x/term"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/gate"
	"github.com/tak848/ccgate/internal/hookctx"
	"github.com/tak848/ccgate/internal/metrics"
)

var version = "dev"

func init() {
	if version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
}

type CLI struct {
	Version kong.VersionFlag `help:"Print version and exit."`
	Init    InitCmd          `cmd:"" help:"Output default configuration."`
	Metrics MetricsCmd       `cmd:"" help:"Show usage metrics summary."`
}

type InitCmd struct {
	Project bool   `help:"Output project-local configuration template." short:"p"`
	Output  string `help:"Write to file instead of stdout." short:"o" type:"path"`
	Force   bool   `help:"Overwrite existing file." short:"f"`
}

type MetricsCmd struct {
	Days    int  `help:"Show last N days." default:"7"`
	JSON    bool `help:"Output as JSON." name:"json"`
	Details int  `help:"Show top-N fallthrough/deny commands per section. Use 0 to hide both sections." default:"10"`
}

func main() { os.Exit(_main()) }

func _main() int {
	// If args given, parse with kong (subcommands, --version, --help).
	if len(os.Args) > 1 {
		var cli CLI
		kctx := kong.Parse(&cli,
			kong.Name("ccgate"),
			kong.Description("Claude Code PermissionRequest hook.\nReads HookInput JSON from stdin, returns allow/deny/fallthrough to stdout."),
			kong.Vars{"version": version},
		)
		switch kctx.Command() {
		case "init":
			return runInit(cli.Init)
		case "metrics":
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to get working directory: %v\n", err)
			}
			lr, err := config.Load(cwd)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
				return 1
			}
			if err := metrics.PrintReport(os.Stdout, lr.Config.ResolveMetricsPath(), metrics.ReportOptions{
				Days:       cli.Metrics.Days,
				AsJSON:     cli.Metrics.JSON,
				DetailsTop: cli.Metrics.Details,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
		}
		return 0
	}

	// No args: if tty, show usage; if pipe, run hook.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "ccgate %s\n\nClaude Code PermissionRequest hook.\nReads HookInput JSON from stdin, returns allow/deny/fallthrough to stdout.\n\nCommands:\n  ccgate init [-p] [-o FILE] [-f]                        Output default configuration\n  ccgate metrics [--days N] [--json] [--details N]       Show usage metrics summary\n                                                         (also prints top fallthrough/deny commands)\n\nFlags:\n  --version    Print version and exit\n  --help       Show help\n", version)
		return 0
	}

	return runHook()
}

func runInit(cmd InitCmd) int {
	content := config.DefaultsJsonnet
	if cmd.Project {
		content = config.DefaultsProjectJsonnet
	}

	if cmd.Output == "" {
		fmt.Print(content)
		return 0
	}

	if !cmd.Force {
		if _, err := os.Stat(cmd.Output); err == nil {
			fmt.Fprintf(os.Stderr, "error: file already exists: %s (use -f to overwrite)\n", cmd.Output)
			return 1
		}
	}

	dir := filepath.Dir(cmd.Output)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create directory %s: %v\n", dir, err)
		return 1
	}

	if err := os.WriteFile(cmd.Output, []byte(content), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to write file %s: %v\n", cmd.Output, err)
		return 1
	}

	fmt.Fprintf(os.Stderr, "wrote %s\n", cmd.Output)
	return 0
}

func runHook() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var input hookctx.HookInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		slog.Error("failed to decode stdin", "error", err)
		return 1
	}

	lr, err := config.Load(input.Cwd)
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
		"permission_mode", input.PermissionMode,
		"config_source", string(lr.Source),
	)

	start := time.Now()
	result, err := gate.DecidePermission(ctx, cfg, input)
	elapsed := time.Since(start)

	// Record metrics (fire-and-forget).
	if !cfg.IsMetricsDisabled() && shouldRecordMetrics(result, err) {
		entry := buildMetricsEntry(start, elapsed, input, cfg, result, err)
		metrics.Record(cfg.ResolveMetricsPath(), cfg.GetMetricsMaxSize(), entry)
	}

	if err != nil {
		slog.Error("DecidePermission failed",
			"error", err,
			"tool", input.ToolName,
			"elapsed_ms", elapsed.Milliseconds(),
		)
		return 1
	}
	if !result.HasDecision {
		slog.Info("DecidePermission: no decision (fallthrough)",
			"tool", input.ToolName,
			"fallthrough_kind", result.FallthroughKind,
			"elapsed_ms", elapsed.Milliseconds(),
		)
		return 0
	}

	slog.Info("DecidePermission: decision made",
		"behavior", result.Decision.Behavior,
		"message", result.Decision.Message,
		"tool", input.ToolName,
		"elapsed_ms", elapsed.Milliseconds(),
	)

	resp := gate.NewPermissionResponse(result.Decision)
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		slog.Error("failed to encode response to stdout", "error", err)
		return 1
	}
	return 0
}

// shouldRecordMetrics decides whether a DecisionResult is worth persisting to
// the metrics log. user_interaction fallthrough (ExitPlanMode / AskUserQuestion)
// is a hard-coded tool-name skip where ccgate never actually evaluates anything,
// so recording it would only inflate the Fall column and drag down
// automation_rate. Every other outcome — including other fallthrough kinds that
// depend on the user's config / environment (bypass, dontAsk, no_apikey,
// non_anthropic), API-unusable responses, LLM fallthroughs, and explicit
// allow/deny — carries useful signal and is recorded. Errors are also always
// recorded regardless of FallthroughKind so that a future code path that
// ever pairs an error with user_interaction cannot silently disappear.
func shouldRecordMetrics(result gate.DecisionResult, err error) bool {
	if err != nil {
		return true
	}
	return result.FallthroughKind != gate.FallthroughKindUserInteraction
}

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
		if result.FallthroughKind == gate.FallthroughKindLLM {
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

const maxTruncateLen = 200

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func initLogger(logPath string, disabled bool, maxLogSize int64) (*slog.Logger, func()) {
	if disabled {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1})), func() {}
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
	return slog.New(slog.NewTextHandler(w, nil)), func() { f.Close() }
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
