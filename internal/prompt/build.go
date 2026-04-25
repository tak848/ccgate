// Package prompt builds the system prompt fed into the LLM. Targets
// (Claude Code, Codex CLI, ...) supply their own allow/deny/environment
// guidance and a target-specific section explaining how to read the
// per-target context fields; the common decision-rule scaffolding lives
// here so both targets stay in lock-step.
package prompt

import (
	"strings"

	"github.com/tak848/ccgate/internal/llm"
)

// Args describes one Build invocation. Caller supplies all
// target-specific text (system prompt header, section explaining how
// to read recent_transcript / settings_permissions / etc., and the
// already-marshalled user payload).
type Args struct {
	// TargetName goes into the prompt header, e.g. "Claude Code" or
	// "Codex CLI". Used purely for human-readable phrasing.
	TargetName string

	// PlanMode switches the decision-rule section to plan-mode rules.
	// Only Claude Code surfaces a plan permission_mode today; Codex
	// hooks should always pass false.
	PlanMode bool

	// TargetSection is target-specific guidance about which fields
	// the user payload carries and how to interpret them. It is
	// inserted between the decision rules and the allow/deny lists.
	// Newline-terminated text; the caller is responsible for wording
	// and trailing blank line.
	TargetSection string

	Allow       []string
	Deny        []string
	Environment []string

	// UserPayload is the JSON (or other text) the LLM should classify.
	// Caller marshals target-specific structs into this string.
	UserPayload string
}

// Build assembles the system + user messages.
func Build(args Args) llm.Prompt {
	var sys strings.Builder

	target := args.TargetName
	if target == "" {
		target = "AI coding tools"
	}
	sys.WriteString("You are ccgate, a PermissionRequest hook classifier for ")
	sys.WriteString(target)
	sys.WriteString(".\n")
	sys.WriteString("Return one of: allow, deny, fallthrough.\n")
	sys.WriteString("Decide quickly. Do not deliberate or reconsider.\n\n")

	if args.PlanMode {
		writePlanModeRules(&sys)
	} else {
		writeNormalModeRules(&sys)
	}

	sys.WriteString("Always provide a brief reason for your decision.\n")
	sys.WriteString("When deny, provide a concise deny_message. If the deny rule includes a deny_message hint, adapt it to the specific situation.\n")

	if args.TargetSection != "" {
		sys.WriteString(args.TargetSection)
		if !strings.HasSuffix(args.TargetSection, "\n") {
			sys.WriteString("\n")
		}
	}
	sys.WriteString("\n")

	if len(args.Allow) > 0 {
		sys.WriteString("Allow guidance:\n- ")
		sys.WriteString(strings.Join(args.Allow, "\n- "))
		sys.WriteString("\n\n")
	}
	if len(args.Deny) > 0 {
		sys.WriteString("Deny guidance (mandatory):\n- ")
		sys.WriteString(strings.Join(args.Deny, "\n- "))
		sys.WriteString("\n\n")
	}
	if len(args.Environment) > 0 {
		sys.WriteString("Environment:\n- ")
		sys.WriteString(strings.Join(args.Environment, "\n- "))
	}

	return llm.Prompt{
		System: strings.TrimSpace(sys.String()),
		User:   args.UserPayload,
	}
}

func writePlanModeRules(b *strings.Builder) {
	b.WriteString("Decision rules (plan mode):\n")
	b.WriteString("Deny guidance below still applies: if a deny guidance rule matches, return deny (or fallthrough when recent_transcript shows the user explicitly requested the exact operation). Deny guidance can block read-only operations too.\n")
	b.WriteString("Otherwise classify:\n")
	b.WriteString("- allow: The operation is (a) side-effect-free (purely read-only / query), OR (b) an edit to the active plan file that Claude Code's plan-mode workflow designated. For compound commands (`|`, `&&`, `||`, `;`, `|&`, `&`, newline), every subcommand MUST independently satisfy (a) or (b). Allow guidance does NOT override (a)/(b) in plan mode, and absence from allow guidance is NOT a reason to fallthrough.\n")
	b.WriteString("- deny: The operation has any side effect on project / production / shared state (writes, package install, build, deploy, git commit/push, piping into a shell, etc.).\n")
	b.WriteString("- fallthrough: Side-effect status is genuinely ambiguous.\n\n")
}

func writeNormalModeRules(b *strings.Builder) {
	b.WriteString("Decision rules:\n")
	b.WriteString("- deny: When a deny guidance rule matches. EXCEPT: if recent_transcript shows the user explicitly requested the exact operation, use fallthrough instead of deny to let the user confirm.\n")
	b.WriteString("- allow: When the operation matches allow guidance and no deny rule matches.\n")
	b.WriteString("- fallthrough: When genuinely uncertain, OR when a deny rule matches but the user explicitly requested the operation.\n")
	b.WriteString("Deny rules always take priority over allow rules. Explicit user requests can only escalate deny to fallthrough, never to allow.\n\n")
}
