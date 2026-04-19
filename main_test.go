package main

import (
	"errors"
	"testing"
	"time"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/gate"
	"github.com/tak848/ccgate/internal/hookctx"
)

func TestBuildMetricsEntry(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 4, 19, 12, 0, 0, 0, time.UTC)
	elapsed := 42 * time.Millisecond
	input := hookctx.HookInput{
		SessionID:      "sess-1",
		ToolName:       "Bash",
		PermissionMode: "default",
	}
	cfg := config.Default()
	cfg.Provider.Model = "test-model"

	cases := map[string]struct {
		result              gate.DecisionResult
		err                 error
		wantDecision        string
		wantDenyMessage     string
		wantFallthroughKind string
		wantForced          bool
		wantError           string
	}{
		"clear allow": {
			result: gate.DecisionResult{
				HasDecision: true,
				Decision:    gate.PermissionDecision{Behavior: gate.BehaviorAllow},
			},
			wantDecision: gate.BehaviorAllow,
		},
		"clear deny carries deny message": {
			result: gate.DecisionResult{
				HasDecision: true,
				Decision:    gate.PermissionDecision{Behavior: gate.BehaviorDeny, Message: "no"},
			},
			wantDecision:    gate.BehaviorDeny,
			wantDenyMessage: "no",
		},
		"forced deny: ft_kind=llm + forced=true + deny_msg set": {
			result: gate.DecisionResult{
				HasDecision:     true,
				Decision:        gate.PermissionDecision{Behavior: gate.BehaviorDeny, Message: "Auto-denied for safety ..."},
				FallthroughKind: gate.FallthroughKindLLM,
			},
			wantDecision:        gate.BehaviorDeny,
			wantDenyMessage:     "Auto-denied for safety ...",
			wantFallthroughKind: gate.FallthroughKindLLM,
			wantForced:          true,
		},
		"forced allow: ft_kind=llm + forced=true + deny_msg empty": {
			result: gate.DecisionResult{
				HasDecision:     true,
				Decision:        gate.PermissionDecision{Behavior: gate.BehaviorAllow, Message: "Auto-approved ..."},
				FallthroughKind: gate.FallthroughKindLLM,
			},
			wantDecision:        gate.BehaviorAllow,
			wantDenyMessage:     "",
			wantFallthroughKind: gate.FallthroughKindLLM,
			wantForced:          true,
		},
		"natural llm fallthrough": {
			result: gate.DecisionResult{
				HasDecision:     false,
				FallthroughKind: gate.FallthroughKindLLM,
			},
			wantDecision:        "fallthrough",
			wantFallthroughKind: gate.FallthroughKindLLM,
		},
		"api unusable fallthrough": {
			result: gate.DecisionResult{
				HasDecision:     false,
				FallthroughKind: gate.FallthroughKindAPIUnusable,
			},
			wantDecision:        "fallthrough",
			wantFallthroughKind: gate.FallthroughKindAPIUnusable,
		},
		"error": {
			err:          errors.New("boom"),
			wantDecision: "error",
			wantError:    "boom",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			entry := buildMetricsEntry(start, elapsed, input, cfg, tc.result, tc.err)

			if entry.Decision != tc.wantDecision {
				t.Errorf("Decision = %q, want %q", entry.Decision, tc.wantDecision)
			}
			if entry.DenyMessage != tc.wantDenyMessage {
				t.Errorf("DenyMessage = %q, want %q", entry.DenyMessage, tc.wantDenyMessage)
			}
			if entry.FallthroughKind != tc.wantFallthroughKind {
				t.Errorf("FallthroughKind = %q, want %q", entry.FallthroughKind, tc.wantFallthroughKind)
			}
			if entry.Forced != tc.wantForced {
				t.Errorf("Forced = %v, want %v", entry.Forced, tc.wantForced)
			}
			if entry.Error != tc.wantError {
				t.Errorf("Error = %q, want %q", entry.Error, tc.wantError)
			}
		})
	}
}
