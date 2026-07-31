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
// These constants name the values ccgate knows about; they are not an
// allowlist. Nothing validates the configured string. provider.name
// only picks which protocol to speak, and base_url can point that
// protocol at anything, so the endpoint on the other end is the only
// authority on which levels mean something -- a proxy fronting a
// dozen models may accept levels no first-party API defines. Only Off
// and None carry ccgate semantics; every other value is forwarded
// verbatim for the provider to accept or reject.
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
