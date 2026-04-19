package gate

import (
	"strings"
	"testing"

	"github.com/tak848/ccgate/internal/config"
)

func strPtr(s string) *string { return &s }

func TestApplyForcedStrategy(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		strategy        *string
		llmReason       string
		wantOK          bool
		wantBehavior    string
		wantMessageHas  []string
		wantMessageMiss []string
	}{
		"unset defaults to ask (no force)": {
			strategy: nil,
			wantOK:   false,
		},
		"explicit ask preserves fallthrough": {
			strategy: strPtr(config.FallthroughStrategyAsk),
			wantOK:   false,
		},
		"allow forces allow with reason": {
			strategy:       strPtr(config.FallthroughStrategyAllow),
			llmReason:      "tool seems read-only but unsure",
			wantOK:         true,
			wantBehavior:   BehaviorAllow,
			wantMessageHas: []string{"LLM-based permission hook returned fallthrough", `LLM reason: "tool seems read-only but unsure"`, "Auto-approved", "unattended automation", "proceed with care"},
		},
		"allow without reason omits LLM reason suffix": {
			strategy:        strPtr(config.FallthroughStrategyAllow),
			llmReason:       "   ",
			wantOK:          true,
			wantBehavior:    BehaviorAllow,
			wantMessageHas:  []string{"LLM-based permission hook returned fallthrough.", "Auto-approved", "proceed with care"},
			wantMessageMiss: []string{"LLM reason:"},
		},
		"deny forces deny with reason": {
			strategy:       strPtr(config.FallthroughStrategyDeny),
			llmReason:      "could be destructive",
			wantOK:         true,
			wantBehavior:   BehaviorDeny,
			wantMessageHas: []string{"LLM-based permission hook returned fallthrough", `LLM reason: "could be destructive"`, "Auto-denied for safety", "do not ask the user", "do not attempt to bypass"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			cfg.FallthroughStrategy = tc.strategy

			d, ok := applyForcedStrategy(cfg, tc.llmReason)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if d.Behavior != tc.wantBehavior {
				t.Fatalf("behavior = %q, want %q", d.Behavior, tc.wantBehavior)
			}
			for _, sub := range tc.wantMessageHas {
				if !strings.Contains(d.Message, sub) {
					t.Errorf("message %q missing substring %q", d.Message, sub)
				}
			}
			for _, sub := range tc.wantMessageMiss {
				if strings.Contains(d.Message, sub) {
					t.Errorf("message %q should not contain %q", d.Message, sub)
				}
			}
		})
	}
}
