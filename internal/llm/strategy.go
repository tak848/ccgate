// Package llm holds shared primitives for LLM-driven permission decisions.
//
// FallthroughKind* values are stored verbatim in metrics entries.
// Only FallthroughKindLLM is promotable via fallthrough_strategy
// (allow|deny). The other kinds indicate runtime-mode or configuration
// conditions and must always defer to the upstream tool's prompt.
//
// FallthroughKindAPIUnusable means the API truncated/refused the response
// or returned no parseable text. It is intentionally NOT subject to
// fallthrough_strategy because the LLM never actually expressed an
// uncertain decision — auto-allowing on a refused/truncated response
// would silently weaken security.
//
// FallthroughKindCredentialUnavailable covers credential-resolution
// failure on the auth.type=exec / auth.type=file path (the helper /
// file itself, the cache layer behind them, and provider 401/403
// responses that suggest a stale or invalid key). It is distinct
// from FallthroughKindNoAPIKey, which is reserved for "the user
// never set any key at all". Like the other runtime-mode kinds, it
// is not affected by fallthrough_strategy: helper failure is not
// LLM uncertainty.
package llm

import (
	"strconv"
	"strings"
)

const (
	FallthroughKindUserInteraction       = "user_interaction"
	FallthroughKindBypass                = "bypass"
	FallthroughKindDontAsk               = "dontask"
	FallthroughKindUnknownProvider       = "unknown_provider"
	FallthroughKindNoAPIKey              = "no_apikey"
	FallthroughKindCredentialUnavailable = "credential_unavailable" //nolint:gosec // metrics classifier value, not a credential
	FallthroughKindLLM                   = "llm"
	FallthroughKindAPIUnusable           = "api_unusable"
)

// FallthroughStrategy values control what ccgate does when the LLM
// returns "fallthrough" (or an empty/unexpected behavior). Only the
// LLM kind is affected — runtime-mode fallthroughs (bypass, dontAsk,
// no_apikey, etc.) always defer to the upstream tool's prompt
// regardless of this setting.
const (
	FallthroughStrategyAsk   = "ask"
	FallthroughStrategyAllow = "allow"
	FallthroughStrategyDeny  = "deny"
)

// ApplyStrategy converts an LLM-uncertainty fallthrough into a forced
// allow/deny based on `strategy`. Returns ok=false when the strategy
// is "ask" (or unrecognized), preserving the original fallthrough
// behavior.
//
// On the message field: target hooks (Claude Code, Codex CLI) only
// deliver decision.message to the AI when behavior is "deny"; the
// allow-side message is silently ignored. We still populate it so
// that (a) it shows up in our own logs / metrics for auditing and
// (b) it works as a forward-compatible hint if upstreams ever start
// delivering allow-side messages.
func ApplyStrategy(strategy, llmReason string) (Decision, bool) {
	switch strategy {
	case FallthroughStrategyAllow:
		return Decision{
			Behavior: BehaviorAllow,
			Message:  buildForcedMessage(BehaviorAllow, llmReason),
		}, true
	case FallthroughStrategyDeny:
		return Decision{
			Behavior: BehaviorDeny,
			Message:  buildForcedMessage(BehaviorDeny, llmReason),
		}, true
	default:
		return Decision{}, false
	}
}

// buildForcedMessage explains to the upstream AI that the hook
// auto-decided what would normally have prompted the user. The
// wording covers: who decided (an LLM-based permission hook), what
// the hook actually returned (fallthrough), why that became a fixed
// decision (to keep unattended automation running), and — for deny —
// that the AI must not ask the user or work around the restriction.
func buildForcedMessage(behavior, llmReason string) string {
	reason := strings.TrimSpace(llmReason)
	var head string
	if reason == "" {
		head = "LLM-based permission hook returned fallthrough."
	} else {
		// strconv.Quote escapes embedded quotes/newlines so the
		// message stays unambiguous regardless of what the LLM
		// emitted.
		head = "LLM-based permission hook returned fallthrough; LLM reason: " + strconv.Quote(reason) + "."
	}

	switch behavior {
	case BehaviorAllow:
		return head + " Auto-approved to keep unattended automation running — proceed with care."
	case BehaviorDeny:
		return head + " Auto-denied for safety to keep unattended automation running — do not ask the user, and do not attempt to bypass this decision via alternative commands or workarounds."
	}
	return head
}
