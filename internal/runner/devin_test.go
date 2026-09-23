package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/llm"
)

// TestResolveCwdFallback pins the cwd substitution order for targets
// whose HookInput carries no cwd (Devin): the target-supplied env var
// wins, the process working directory is the last resort.
func TestResolveCwdFallback(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	t.Run("env var wins", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CCGATE_TEST_PROJECT_DIR", dir)
		if got := resolveCwdFallback("CCGATE_TEST_PROJECT_DIR"); got != dir {
			t.Fatalf("got %q, want %q", got, dir)
		}
	})

	t.Run("empty env var falls back to getwd", func(t *testing.T) {
		t.Setenv("CCGATE_TEST_PROJECT_DIR", "")
		if got := resolveCwdFallback("CCGATE_TEST_PROJECT_DIR"); got != wd {
			t.Fatalf("got %q, want process cwd %q", got, wd)
		}
	})

	t.Run("no env var configured falls back to getwd", func(t *testing.T) {
		if got := resolveCwdFallback(""); got != wd {
			t.Fatalf("got %q, want process cwd %q", got, wd)
		}
	})
}

// TestReferencedPathsDevinToolNames covers the snake_case tool names
// Devin emits on the wire. Same extraction rules as the Claude-side
// PascalCase names, different spellings.
func TestReferencedPathsDevinToolNames(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		toolName string
		input    HookToolInput
		wantLen  int
	}{
		"exec command paths": {
			toolName: "exec",
			input:    HookToolInput{Command: "cat /etc/hosts ./README.md"},
			wantLen:  2,
		},
		"edit file_path": {
			toolName: "edit",
			input:    HookToolInput{FilePath: "src/main.go"},
			wantLen:  1,
		},
		"glob path and pattern": {
			toolName: "glob",
			input:    HookToolInput{Path: "src", Pattern: "**/*.go"},
			wantLen:  2,
		},
		"apply_patch not modeled": {
			toolName: "apply_patch",
			input:    HookToolInput{FilePath: "src/main.go"},
			wantLen:  0,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := referencedPaths(HookInput{
				Cwd:       filepath.Join(string(filepath.Separator), "tmp", "repo"),
				ToolName:  tc.toolName,
				ToolInput: tc.input,
			})
			if len(got) != tc.wantLen {
				t.Fatalf("referencedPaths got %d paths %v, want %d", len(got), got, tc.wantLen)
			}
		})
	}
}

// TestDecideDevinInteractiveTools pins the user-interaction fallthrough
// for Devin's snake_case tool names: they must never be auto-decided,
// and the guard fires before any credential / provider work.
func TestDecideDevinInteractiveTools(t *testing.T) {
	t.Parallel()

	for _, toolName := range []string{"exit_plan_mode", "ask_user_question"} {
		t.Run(toolName, func(t *testing.T) {
			t.Parallel()
			_, hasDecision, kind, _, _, _, err := decide(
				context.Background(),
				config.Default(),
				HookInput{ToolName: toolName},
				runtimeOptions{},
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if hasDecision {
				t.Fatal("interactive tool produced a decision")
			}
			if kind != llm.FallthroughKindUserInteraction {
				t.Fatalf("kind = %q, want %q", kind, llm.FallthroughKindUserInteraction)
			}
		})
	}
}
