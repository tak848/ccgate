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
package llm

const (
	FallthroughKindUserInteraction = "user_interaction"
	FallthroughKindBypass          = "bypass"
	FallthroughKindDontAsk         = "dontask"
	FallthroughKindNonAnthropic    = "non_anthropic"
	FallthroughKindNoAPIKey        = "no_apikey"
	FallthroughKindLLM             = "llm"
	FallthroughKindAPIUnusable     = "api_unusable"
)
