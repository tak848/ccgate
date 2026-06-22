package llm

import "context"

// Behavior values returned by the LLM (and emitted by Decide).
const (
	BehaviorAllow       = "allow"
	BehaviorDeny        = "deny"
	BehaviorFallthrough = "fallthrough"
)

// DefaultDenyMessage is used when the LLM signals deny but emits no
// concrete deny_message. Targets may override per request before
// rendering the final response.
const DefaultDenyMessage = "Automatically denied as potentially dangerous."

// Prompt is what targets feed into Provider.Decide.
type Prompt struct {
	System    string
	User      string
	Model     string
	TimeoutMS int
}

// Output is the structured response from the LLM.
//
// Field order is deliberate and load-bearing: providers generate the
// JSON keys in schema property order (OpenAI follows schema order;
// Anthropic follows it for required fields, which all of these are).
// Reason MUST come first so the model writes its justification before
// committing to a behavior -- emitting behavior first makes weak models
// (e.g. haiku, kimi) lock in a decision before reasoning, producing
// reason/behavior contradictions. See internal/llm/schema.go for how
// the order survives the trip to each SDK.
type Output struct {
	Reason      string `json:"reason"       jsonschema_description:"Brief reason for the decision. Always provide this regardless of behavior. Write this BEFORE deciding behavior."`
	Behavior    string `json:"behavior"     jsonschema:"enum=allow,enum=deny,enum=fallthrough" jsonschema_description:"One of allow, deny, fallthrough. Must be consistent with reason."`
	DenyMessage string `json:"deny_message" jsonschema_description:"When behavior is deny, a concise explanation of why. Must not be empty when denying."`
}

// Usage holds token usage from a single LLM call.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

// Result wraps Output + Usage with an Unusable flag set when the API
// truncated/refused the response or returned no parseable text. Targets
// must not treat Unusable=true as a real LLM "fallthrough" — the LLM
// never actually expressed uncertainty, so fallthrough_strategy must
// not promote it to allow/deny.
type Result struct {
	Output   Output
	Usage    *Usage
	Unusable bool
}

// Decision is the final allow/deny resolved from an LLM Output (after
// fallthrough_strategy is applied) and rendered into a target-specific
// response shape by the caller.
type Decision struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

// Provider abstracts a single LLM call. Implementations live under
// internal/llm/<provider>/ (e.g. anthropic). Tests use a fake provider
// to avoid real network calls.
type Provider interface {
	Decide(ctx context.Context, p Prompt) (Result, error)
}
