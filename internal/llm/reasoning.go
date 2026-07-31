package llm

// ReasoningEffort* are the accepted `provider.reasoning_effort`
// values. ccgate is a classifier — it decides whether a single tool
// call is allowed — so the default is ReasoningEffortNone: no
// reasoning, lowest latency, and no reasoning tokens eating into the
// provider clients' output-token cap.
//
// The value is a single knob across providers, but each provider
// spells it differently and the clients own their own mapping:
//
//   - openai / gemini send it verbatim as `reasoning_effort`.
//   - anthropic has no "none" effort level. `output_config.effort` is
//     only low|medium|high|xhigh|max, and the parameter that actually
//     stops Claude from thinking is `thinking`. So the anthropic
//     client maps None to `thinking: {type: "disabled"}` alone, and
//     every other level to `thinking: {type: "adaptive"}` plus
//     `output_config.effort`.
//
// ReasoningEffortOff is the opt-out: nothing is sent at all, for
// models that reject the parameter outright.
//
// ReasoningEffortMinimal has no Anthropic counterpart; config
// validation rejects it when provider.name is "anthropic".
const (
	ReasoningEffortOff     = ""
	ReasoningEffortNone    = "none"
	ReasoningEffortMinimal = "minimal"
	ReasoningEffortLow     = "low"
	ReasoningEffortMedium  = "medium"
	ReasoningEffortHigh    = "high"
	ReasoningEffortXHigh   = "xhigh"
	ReasoningEffortMax     = "max"
)
