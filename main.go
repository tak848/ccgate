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
}

func main() { os.Exit(_main()) }

func _main() int {
	// Always parse flags first (--version, --help work regardless of tty).
	var cli CLI
	kong.Parse(&cli,
		kong.Name("ccgate"),
		kong.Description("Claude Code PermissionRequest hook.\nReads HookInput JSON from stdin, returns allow/deny/fallthrough to stdout."),
		kong.Vars{"version": version},
	)

	// When invoked directly from a terminal with no flags, show usage instead of blocking on stdin.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintf(os.Stderr, "ccgate %s\n\nClaude Code PermissionRequest hook.\nReads HookInput JSON from stdin, returns allow/deny/fallthrough to stdout.\n\nUsage: echo '<HookInput JSON>' | ccgate\n\nFlags:\n  --version    Print version and exit\n  --help       Show help\n", version)
		return 0
	}

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

	logger, cleanup := initLogger(cfg.ResolveLogPath(), cfg.LogDisabled, cfg.LogMaxSize)
	defer cleanup()
	slog.SetDefault(logger)

	slog.Info("hook invoked",
		"tool", input.ToolName,
		"permission_mode", input.PermissionMode,
	)

	start := time.Now()
	decision, ok, err := gate.DecidePermission(ctx, cfg, input)
	elapsed := time.Since(start)

	if err != nil {
		slog.Error("DecidePermission failed",
			"error", err,
			"tool", input.ToolName,
			"elapsed_ms", elapsed.Milliseconds(),
		)
		return 1
	}
	if !ok {
		slog.Info("DecidePermission: no decision (fallthrough)",
			"tool", input.ToolName,
			"elapsed_ms", elapsed.Milliseconds(),
		)
		return 0
	}

	slog.Info("DecidePermission: decision made",
		"behavior", decision.Behavior,
		"message", decision.Message,
		"tool", input.ToolName,
		"elapsed_ms", elapsed.Milliseconds(),
	)

	resp := gate.NewPermissionResponse(decision)
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		slog.Error("failed to encode response to stdout", "error", err)
		return 1
	}
	return 0
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

	rotateLog(logPath, maxLogSize)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
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

func rotateLog(path string, maxSize int64) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxSize {
		return
	}
	prev := path + ".1"
	if err := os.Remove(prev); err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to remove old log", "path", prev, "error", err)
	}
	if err := os.Rename(path, prev); err != nil {
		slog.Warn("failed to rotate log", "path", path, "error", err)
	}
}
