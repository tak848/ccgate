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
	Metrics MetricsCmd       `cmd:"" help:"Show usage metrics summary."`
}

type MetricsCmd struct {
	Days int  `help:"Show last N days." default:"7"`
	JSON bool `help:"Output as JSON." name:"json"`
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
		if kctx.Command() == "metrics" {
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to get working directory: %v\n", err)
			}
			cfg, err := config.Load(cwd)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
				return 1
			}
			if err := metrics.PrintReport(os.Stdout, cfg.ResolveMetricsPath(), metrics.ReportOptions{
				Days:   cli.Metrics.Days,
				AsJSON: cli.Metrics.JSON,
			}); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return 1
			}
		}
		return 0
	}

	// No args: if tty, show usage; if pipe, run hook.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "ccgate %s\n\nClaude Code PermissionRequest hook.\nReads HookInput JSON from stdin, returns allow/deny/fallthrough to stdout.\n\nCommands:\n  ccgate metrics [--days N] [--json]\n\nFlags:\n  --version    Print version and exit\n  --help       Show help\n", version)
		return 0
	}

	return runHook()
}

func runHook() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var input hookctx.HookInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		slog.Error("failed to decode stdin", "error", err)
		return 1
	}

	cfg, err := config.Load(input.Cwd)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return 1
	}

	logger, cleanup := initLogger(cfg.ResolveLogPath(), cfg.IsLogDisabled(), cfg.GetLogMaxSize())
	defer cleanup()
	slog.SetDefault(logger)

	slog.Info("hook invoked",
		"tool", input.ToolName,
		"permission_mode", input.PermissionMode,
	)

	start := time.Now()
	result, err := gate.DecidePermission(ctx, cfg, input)
	elapsed := time.Since(start)

	// Record metrics (fire-and-forget).
	if !cfg.IsMetricsDisabled() {
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
		entry.DenyMessage = result.Decision.Message
		entry.Reason = truncateStr(result.LLMReason, maxTruncateLen)
	} else {
		entry.Decision = "fallthrough"
		entry.FallthroughKind = result.FallthroughKind
		entry.Reason = truncateStr(result.LLMReason, maxTruncateLen)
	}

	if result.Usage != nil {
		entry.InputTokens = result.Usage.InputTokens
		entry.OutputTokens = result.Usage.OutputTokens
	}

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
