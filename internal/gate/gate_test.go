package gate

import (
	"strings"
	"testing"

	"github.com/tak848/ccgate/internal/config"
)

func strPtr(s string) *string { return &s }

func TestDecideFromLLMResult(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		strategy            *string
		callResult          LLMCallResult
		wantHasDecision     bool
		wantBehavior        string
		wantFallthroughKind string
		wantMessageHas      []string
	}{
		"clear allow passes through": {
			callResult:      LLMCallResult{Output: PermissionLLMOutput{Behavior: BehaviorAllow, Reason: "obviously safe"}},
			wantHasDecision: true,
			wantBehavior:    BehaviorAllow,
		},
		"clear deny passes through with reason": {
			callResult:      LLMCallResult{Output: PermissionLLMOutput{Behavior: BehaviorDeny, Reason: "dangerous", DenyMessage: "Dangerous operation"}},
			wantHasDecision: true,
			wantBehavior:    BehaviorDeny,
			wantMessageHas:  []string{"Dangerous operation"},
		},
		"llm fallthrough with default ask preserves fallthrough": {
			callResult:          LLMCallResult{Output: PermissionLLMOutput{Behavior: BehaviorFallthrough, Reason: "unsure"}},
			wantHasDecision:     false,
			wantFallthroughKind: FallthroughKindLLM,
		},
		"llm fallthrough with strategy=deny is forced to deny with message": {
			strategy:            strPtr(config.FallthroughStrategyDeny),
			callResult:          LLMCallResult{Output: PermissionLLMOutput{Behavior: BehaviorFallthrough, Reason: "unsure"}},
			wantHasDecision:     true,
			wantBehavior:        BehaviorDeny,
			wantFallthroughKind: FallthroughKindLLM,
			wantMessageHas:      []string{"Auto-denied for safety", `LLM reason: "unsure"`},
		},
		"llm fallthrough with strategy=allow is forced to allow with message": {
			strategy:            strPtr(config.FallthroughStrategyAllow),
			callResult:          LLMCallResult{Output: PermissionLLMOutput{Behavior: BehaviorFallthrough, Reason: "unsure"}},
			wantHasDecision:     true,
			wantBehavior:        BehaviorAllow,
			wantFallthroughKind: FallthroughKindLLM,
			wantMessageHas:      []string{"Auto-approved", "proceed with care"},
		},
		"empty behavior is treated as fallthrough and forceable": {
			strategy:            strPtr(config.FallthroughStrategyDeny),
			callResult:          LLMCallResult{Output: PermissionLLMOutput{Behavior: "", Reason: "blank"}},
			wantHasDecision:     true,
			wantBehavior:        BehaviorDeny,
			wantFallthroughKind: FallthroughKindLLM,
			wantMessageHas:      []string{"Auto-denied for safety"},
		},
		"unusable api with default ask falls through with api_unusable": {
			callResult:          LLMCallResult{Unusable: true},
			wantHasDecision:     false,
			wantFallthroughKind: FallthroughKindAPIUnusable,
		},
		"unusable api with strategy=allow stays a fallthrough (not forced)": {
			strategy:            strPtr(config.FallthroughStrategyAllow),
			callResult:          LLMCallResult{Unusable: true},
			wantHasDecision:     false,
			wantFallthroughKind: FallthroughKindAPIUnusable,
		},
		"unusable api with strategy=deny stays a fallthrough (not forced)": {
			strategy:            strPtr(config.FallthroughStrategyDeny),
			callResult:          LLMCallResult{Unusable: true},
			wantHasDecision:     false,
			wantFallthroughKind: FallthroughKindAPIUnusable,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Default()
			cfg.FallthroughStrategy = tc.strategy

			got := decideFromLLMResult(cfg, tc.callResult)

			if got.HasDecision != tc.wantHasDecision {
				t.Fatalf("HasDecision = %v, want %v", got.HasDecision, tc.wantHasDecision)
			}
			if got.HasDecision && got.Decision.Behavior != tc.wantBehavior {
				t.Fatalf("Behavior = %q, want %q", got.Decision.Behavior, tc.wantBehavior)
			}
			if got.FallthroughKind != tc.wantFallthroughKind {
				t.Fatalf("FallthroughKind = %q, want %q", got.FallthroughKind, tc.wantFallthroughKind)
			}
			for _, sub := range tc.wantMessageHas {
				if !strings.Contains(got.Decision.Message, sub) {
					t.Errorf("Decision.Message %q missing substring %q", got.Decision.Message, sub)
				}
			}
		})
	}
}

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
		"reason containing quotes is escaped (no broken quoting)": {
			strategy:     strPtr(config.FallthroughStrategyDeny),
			llmReason:    `the user said "yes"` + "\nbut also no",
			wantOK:       true,
			wantBehavior: BehaviorDeny,
			wantMessageHas: []string{
				`LLM reason: "the user said \"yes\"\nbut also no".`,
				"Auto-denied for safety",
			},
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
