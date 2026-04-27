// Package prompt builds the system prompt fed into the LLM. The
// scaffolding is identical across targets (Claude Code, Codex CLI,
// ...); per-target differences live in the jsonnet config the
// caller supplies (allow / deny / environment) and in the user
// payload caller marshals into the message.
package prompt

import (
	"strings"

	"github.com/tak848/ccgate/internal/llm"
)

// Args describes one Build invocation.
type Args struct {
	// PlanMode switches the decision-rule section to plan-mode
	// rules. Only Claude Code surfaces a plan permission_mode today;
	// Codex hooks should always pass false.
	PlanMode bool

	Allow       []string
	Deny        []string
	Environment []string

	// UserPayload is the JSON the LLM should classify. Caller
	// marshals target-specific structs into this string.
	UserPayload string
}

// Build assembles the system + user messages.
func Build(args Args) llm.Prompt {
	var sys strings.Builder

	sys.WriteString("You are ccgate, a PermissionRequest hook classifier for AI coding tools.\n")
	sys.WriteString("Return one of: allow, deny, fallthrough.\n")
	sys.WriteString("Decide quickly. Do not deliberate or reconsider.\n\n")

	if args.PlanMode {
		writePlanModeRules(&sys)
	} else {
		writeNormalModeRules(&sys)
	}

	sys.WriteString("Always provide a brief reason for your decision.\n")
	sys.WriteString("When deny, provide a concise deny_message. If a deny rule includes a deny_message hint, adapt it to the specific situation.\n")
	sys.WriteString("The user message is a JSON document describing the request. Inspect tool_name, tool_input, cwd, and any other fields present (settings_permissions / recent_transcript / context, etc.) -- use what is there, do not invent fields you don't see.\n\n")

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
	b.WriteString("- allow: The operation is (a) side-effect-free (purely read-only / query), OR (b) an edit to the active plan file the host tool's plan-mode workflow designated. For compound shell commands (`|`, `&&`, `||`, `;`, `|&`, `&`, newline), every subcommand MUST independently satisfy (a) or (b). Allow guidance does NOT override (a)/(b) in plan mode, and absence from allow guidance is NOT a reason to fallthrough.\n")
	b.WriteString("- deny: The operation has any side effect on project / production / shared state (writes, package install, build, deploy, git commit/push, piping into a shell, etc.).\n")
	b.WriteString("- fallthrough: Side-effect status is genuinely ambiguous.\n\n")
}

func writeNormalModeRules(b *strings.Builder) {
	b.WriteString("Decision rules:\n")
	b.WriteString("- deny: When a deny guidance rule matches. EXCEPT: if recent_transcript (when present) shows the user explicitly requested the exact operation, use fallthrough instead of deny to let the user confirm.\n")
	b.WriteString("- allow: When the operation matches allow guidance and no deny rule matches.\n")
	b.WriteString("- fallthrough: When genuinely uncertain, OR when a deny rule matches but the user explicitly requested the operation.\n")
	b.WriteString("Deny rules always take priority over allow rules. Explicit user requests can only escalate deny to fallthrough, never to allow.\n\n")
}
