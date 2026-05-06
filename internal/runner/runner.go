// Package runner is the entire ccgate PermissionRequest hook
// orchestration. There is no per-target adapter layer: stdin/stdout
// shapes are shared across Claude Code and Codex CLI (both deliver
// session_id / transcript_path / cwd / hook_event_name / tool_name /
// tool_input on stdin and the same hookSpecificOutput.decision shape
// on stdout). Per-target differences are handled here directly:
//
//   - Claude-only fields: permission_mode, permission_suggestions
//   - Codex-only fields: model, turn_id
//
// cmd/<target>/ packages stay tiny -- they only hand a config.LoadOptions
// (where to read the per-user config / write the per-target log+metrics)
// and call Run.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	openaisdk "github.com/openai/openai-go"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/gitutil"
	"github.com/tak848/ccgate/internal/keystore"
	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/llm/anthropic"
	"github.com/tak848/ccgate/internal/llm/gemini"
	"github.com/tak848/ccgate/internal/llm/openai"
	"github.com/tak848/ccgate/internal/metrics"
	"github.com/tak848/ccgate/internal/prompt"
)

// PermissionModePlan is the Claude Code permission_mode value that
// puts ccgate into plan-mode evaluation.
const PermissionModePlan = "plan"

// HookInput is the on-the-wire JSON Claude Code and Codex CLI both
// deliver to the PermissionRequest hook. Fields that only one of the
// two surfaces stay omitempty so the user payload sent to the LLM
// only contains what actually arrived.
type HookInput struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	ToolName       string `json:"tool_name"`

	ToolInput    HookToolInput   `json:"-"`
	ToolInputRaw json.RawMessage `json:"-"`

	// Claude-only
	PermissionMode        string            `json:"permission_mode,omitempty"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions,omitempty"`

	// Codex-only
	Model  string `json:"model,omitempty"`
	TurnID string `json:"turn_id,omitempty"`
}

// HookToolInput is the canonical parsed view of tool_input shared by
// all targets. Fields are emitted unconditionally (no omitempty) so
// the LLM sees the full schema and can address fields by name even
// when the upstream tool left them empty -- the JSON shape itself
// documents what ccgate models. ToolInputRaw carries the verbatim
// upstream bytes alongside this view for tool shapes ccgate has not
// canonicalized yet.
type HookToolInput struct {
	Command        string              `json:"command"`
	Description    string              `json:"description"`
	FilePath       string              `json:"file_path"`
	Path           string              `json:"path"`
	Pattern        string              `json:"pattern"`
	Content        string              `json:"content"`
	ContentUpdates []HookContentUpdate `json:"content_updates"`
}

// HookContentUpdate is the per-hunk Edit / apply_patch payload.
type HookContentUpdate struct {
	OldString string `json:"old_str"`
	NewString string `json:"new_str"`
}

// UnmarshalJSON keeps the raw tool_input bytes around so the LLM sees
// every field the upstream hook delivered (including tool-specific
// shapes ccgate doesn't yet model) while also surfacing the parsed
// fields metrics relies on.
func (h *HookInput) UnmarshalJSON(data []byte) error {
	type alias HookInput
	var raw struct {
		alias
		ToolInput json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*h = HookInput(raw.alias)
	h.ToolInputRaw = raw.ToolInput
	if len(raw.ToolInput) > 0 {
		// Best-effort parse: failure is fine for tool shapes ccgate
		// does not yet model. Raw bytes are forwarded to the LLM untouched.
		_ = json.Unmarshal(raw.ToolInput, &h.ToolInput)
	}
	return nil
}

// HookOutput is the JSON response shape Claude Code and Codex both expect.
type HookOutput struct {
	HookSpecificOutput hookSpecificOutput `json:"hookSpecificOutput"`
}

type hookSpecificOutput struct {
	HookEventName string             `json:"hookEventName"`
	Decision      decisionPayloadOut `json:"decision"`
}

type decisionPayloadOut struct {
	Behavior string `json:"behavior"`
	Message  string `json:"message,omitempty"`
}

func newHookOutput(eventName string, d llm.Decision) HookOutput {
	if eventName == "" {
		eventName = "PermissionRequest"
	}
	return HookOutput{
		HookSpecificOutput: hookSpecificOutput{
			HookEventName: eventName,
			Decision: decisionPayloadOut{
				Behavior: d.Behavior,
				Message:  d.Message,
			},
		},
	}
}

// Option customises the runner with target-specific extra inputs
// the LLM payload + system prompt should carry. Pass them via Run's
// variadic.
type Option func(*runtimeOptions)

type runtimeOptions struct {
	targetName            string
	cacheTarget           string
	promptSection         string
	hasRecentTranscript   bool
	loadStaticPermissions func(cwd string) any
	loadRecentTranscript  func(transcriptPath string) any
	// providerFactory is a test-only injection point for swapping
	// the live LLM client. Production code leaves it nil and falls
	// back to newProviderClient. Wired through runtimeOptions
	// instead of a package-level var so parallel tests can each pass
	// their own fake without racing.
	providerFactory func(providerName, apiKey, baseURL string) llm.Provider
}

// WithTargetName labels the host tool in the system prompt header
// (e.g. "Claude Code", "Codex CLI"). The default header text falls
// back to a generic phrasing if unset.
func WithTargetName(name string) Option {
	return func(o *runtimeOptions) { o.targetName = name }
}

// WithCacheTarget sets the per-target subdirectory name used for the
// keystore cache layout ("claude" / "codex" / ...). It must match the
// subdir cmd/<target>/ already uses for log_path / metrics_path so a
// user looking at one place sees them all together. An empty string
// is fed straight to keystore.Options.TargetName, which means the
// cache file lands one level up under $XDG_CACHE_HOME/ccgate/ and is
// shared across targets — fine for Run callers that never configure
// provider.auth (env-var path only), since nothing is written there.
func WithCacheTarget(name string) Option {
	return func(o *runtimeOptions) { o.cacheTarget = name }
}

// WithPromptSection injects target-specific guidance about which
// payload fields the target delivers and how the LLM should
// interpret them. Inserted between the decision rules and the
// allow/deny lists. Targets that have nothing target-specific to
// say (Codex today) simply do not pass this option.
func WithPromptSection(section string) Option {
	return func(o *runtimeOptions) { o.promptSection = section }
}

// WithHasRecentTranscript declares that the user payload carries a
// `recent_transcript` field. The decision-rule wording adjusts so
// the LLM is not told to consult a field that isn't there. Targets
// that have no equivalent (Codex today) leave this false.
func WithHasRecentTranscript(has bool) Option {
	return func(o *runtimeOptions) { o.hasRecentTranscript = has }
}

// WithStaticPermissions injects a target-specific reader for the
// host tool's static allow/deny patterns (e.g. Claude Code's
// `~/.claude/settings.json` permissions). Returning nil omits the
// payload entry. Targets that have no equivalent (Codex today --
// its `~/.codex/config.toml` prefix_rules ingestion is a separate
// piece of work) simply do not pass this option.
func WithStaticPermissions(fn func(cwd string) any) Option {
	return func(o *runtimeOptions) { o.loadStaticPermissions = fn }
}

// WithRecentTranscript injects a target-specific transcript reader.
// Returning nil omits the payload entry. Targets whose transcript
// format ccgate does not yet model (Codex today) do not pass this
// option.
func WithRecentTranscript(fn func(transcriptPath string) any) Option {
	return func(o *runtimeOptions) { o.loadRecentTranscript = fn }
}

// WithProviderFactory replaces the LLM-client constructor used by
// decide(). Test-only: production callers leave it unset and the
// runner uses newProviderClient. The signature mirrors
// newProviderClient so a fake can implement llm.Provider with no
// awareness of the underlying SDK shape.
func WithProviderFactory(fn func(providerName, apiKey, baseURL string) llm.Provider) Option {
	return func(o *runtimeOptions) { o.providerFactory = fn }
}

// Run is the entry point. cmd/<target>/ packages call it with a
// per-target config.LoadOptions plus any target-specific Options
// (settings reader, transcript reader, ...). The rest of the flow
// is identical.
func Run(stdin io.Reader, stdout io.Writer, opts config.LoadOptions, runOpts ...Option) int {
	var ro runtimeOptions
	for _, fn := range runOpts {
		fn(&ro)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var input HookInput
	if err := json.NewDecoder(stdin).Decode(&input); err != nil {
		slog.Error("failed to decode stdin", "error", err)
		return 1
	}

	lr, err := config.Load(opts, input.Cwd)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return 1
	}
	cfg := lr.Config

	logger, cleanup := initLogger(cfg.ResolveLogPath(), cfg.IsLogDisabled(), cfg.GetLogMaxSize())
	defer cleanup()
	slog.SetDefault(logger)

	slog.Info("hook invoked",
		"tool", input.ToolName,
		"permission_mode", input.PermissionMode,
		"model", input.Model,
		"turn_id", input.TurnID,
		"config_source", string(lr.Source),
	)

	start := time.Now()
	decision, hasDecision, kind, reason, credSource, usage, runErr := decide(ctx, cfg, input, ro)
	elapsed := time.Since(start)

	if !cfg.IsMetricsDisabled() {
		entry := buildMetricsEntry(start, elapsed, input, cfg, decision, hasDecision, kind, reason, credSource, usage, runErr)
		metrics.Record(cfg.ResolveMetricsPath(), cfg.GetMetricsMaxSize(), entry)
	}

	if runErr != nil {
		slog.Error("decide failed", "error", runErr, "tool", input.ToolName, "elapsed_ms", elapsed.Milliseconds())
		return 1
	}
	if !hasDecision {
		slog.Info("no decision (fallthrough)", "kind", kind, "tool", input.ToolName, "elapsed_ms", elapsed.Milliseconds())
		return 0
	}

	slog.Info("decision made", "behavior", decision.Behavior, "message", decision.Message, "tool", input.ToolName, "elapsed_ms", elapsed.Milliseconds())
	if err := json.NewEncoder(stdout).Encode(newHookOutput(input.HookEventName, decision)); err != nil {
		slog.Error("failed to encode response to stdout", "error", err)
		return 1
	}
	return 0
}

// decide returns: the resolved Decision, whether the hook should
// emit an explicit allow/deny payload (false means "fall through to
// the upstream prompt"), the fallthrough kind classifier for
// metrics, the reason string, the credential source label
// ("exec"/"file"/"cache"/"lock") when kind is
// credential_unavailable, the LLM token usage if any, and the run
// error (only set for unrecoverable failures that should make the
// hook exit 1).
func decide(ctx context.Context, cfg config.Config, in HookInput, ro runtimeOptions) (llm.Decision, bool, string, string, string, *llm.Usage, error) {
	// Tools that require user interaction must never be auto-decided.
	switch in.ToolName {
	case "ExitPlanMode", "AskUserQuestion":
		slog.Info("user interaction tool: falling through", "tool", in.ToolName)
		return llm.Decision{}, false, llm.FallthroughKindUserInteraction, "", "", nil, nil
	}

	// Some permission modes hand the prompt back to the upstream tool
	// regardless of the LLM's opinion.
	switch in.PermissionMode {
	case PermissionModePlan:
		// fall through to LLM
	case "bypassPermissions":
		slog.Info("bypass mode: falling through", "tool", in.ToolName)
		return llm.Decision{}, false, llm.FallthroughKindBypass, "", "", nil, nil
	case "dontAsk":
		slog.Info("dontAsk mode: falling through", "tool", in.ToolName)
		return llm.Decision{}, false, llm.FallthroughKindDontAsk, "", "", nil, nil
	}

	providerName := strings.ToLower(cfg.Provider.Name)
	// Trim whitespace so a templating mistake like `base_url: '   '`
	// is treated as missing rather than passed through to the SDK,
	// which would surface as a hard config error and exit 1 instead of
	// quietly using the provider's default endpoint.
	baseURL := strings.TrimSpace(cfg.Provider.BaseURL)

	apiKey, kind, reason, source, err := resolveAPIKey(ctx, cfg.Provider, providerName, ro.cacheTarget)
	if err != nil {
		// resolveAPIKey already logged the helper / file failure.
		// Intentionally fall through (never propagate this error to
		// run-level exit) so the upstream tool prompts the user
		// instead of the hook crashing — same UX as the existing
		// "no API key set" path. The kind/reason/source we captured
		// already carry the diagnostic into metrics + log.
		return llm.Decision{}, false, kind, reason, source, nil, nil //nolint:nilerr // intentional: credential failures never exit 1
	}
	if apiKey == "" {
		// No key configured at all (kind already classified).
		return llm.Decision{}, false, kind, reason, source, nil, nil
	}

	p, err := buildPrompt(cfg, in, ro)
	if err != nil {
		return llm.Decision{}, false, "", "", "", nil, fmt.Errorf("build prompt: %w", err)
	}

	slog.Info("llm request",
		"provider", cfg.Provider.Name,
		"model", p.Model,
		"timeout_ms", p.TimeoutMS,
		"system_prompt", p.System,
		"user_message", redactedUserMessage(p.User),
	)

	clientFactory := ro.providerFactory
	if clientFactory == nil {
		clientFactory = newProviderClient
	}
	client := clientFactory(providerName, apiKey, baseURL)
	res, err := client.Decide(ctx, p)
	if err != nil {
		// 401 / 403 against a key that came from a helper / file
		// path is the canonical "the credential we just used is
		// stale or wrong" signal: invalidate the keystore cache
		// (auth.type=exec only — file mode does not cache) and
		// fall through (no exit 1) so the upstream tool's prompt
		// still reaches the user.
		//
		// env-var keys are deliberately NOT routed here. ccgate
		// cannot rotate env vars, so silently turning a
		// revoked / wrong env-var key into a fallthrough would
		// hide a real user-side configuration error. Surface it
		// as a normal API error and let the existing exit-1 path
		// run.
		//
		// We do not split 401 vs 403 by error code: ccgate's
		// supported provider paths (anthropic-sdk-go, openai-go,
		// gemini via openai-compat) report credential rejection on
		// 401 in practice, and we do not have any provider-specific
		// 403 classifier for the codes that AWS-style proxies emit
		// (Bedrock support is a separate piece of work, see #62).
		if status, ok := providerAuthStatus(err); ok && cfg.Provider.Auth != nil {
			if cfg.Provider.Auth.Type == config.AuthTypeExec {
				invalidateAuthCache(cfg.Provider, ro.cacheTarget, providerName, baseURL)
			}
			// Deliberately do NOT log the raw err string. Both
			// anthropic-sdk-go and openai-go embed the response
			// body inside Error.Error(); a custom proxy or broker
			// might echo a credential / token / request signature
			// in its 401/403 body. Log only the secret-free
			// triage fields.
			slog.Warn("provider rejected credential, falling through",
				"kind", llm.FallthroughKindCredentialUnavailable,
				"reason", string(keystore.ReasonProviderAuth),
				"source", source,
				"status", status,
				"provider", cfg.Provider.Name,
			)
			return llm.Decision{}, false, llm.FallthroughKindCredentialUnavailable, string(keystore.ReasonProviderAuth), source, res.Usage, nil
		}
		// Non-auth API errors (rate limit / 5xx / network) and
		// env-var-path 401/403 keep the existing exit-1 path.
		// Strip the raw response body so a chatty proxy cannot
		// leak credentials into ccgate.log.
		return llm.Decision{}, false, "", "", "", res.Usage, redactProviderError(cfg.Provider.Name, err)
	}
	if res.Unusable {
		return llm.Decision{}, false, llm.FallthroughKindAPIUnusable, "", "", res.Usage, nil
	}

	switch res.Output.Behavior {
	case llm.BehaviorAllow:
		return llm.Decision{Behavior: llm.BehaviorAllow}, true, "", res.Output.Reason, "", res.Usage, nil
	case llm.BehaviorDeny:
		msg := strings.TrimSpace(res.Output.DenyMessage)
		if msg == "" {
			msg = llm.DefaultDenyMessage
		}
		return llm.Decision{Behavior: llm.BehaviorDeny, Message: msg}, true, "", res.Output.Reason, "", res.Usage, nil
	case llm.BehaviorFallthrough, "":
		if d, ok := llm.ApplyStrategy(cfg.GetFallthroughStrategy(), res.Output.Reason); ok {
			return d, true, llm.FallthroughKindLLM, res.Output.Reason, "", res.Usage, nil
		}
		return llm.Decision{}, false, llm.FallthroughKindLLM, res.Output.Reason, "", res.Usage, nil
	default:
		slog.Warn("unexpected LLM behavior", "behavior", res.Output.Behavior)
		if d, ok := llm.ApplyStrategy(cfg.GetFallthroughStrategy(), res.Output.Reason); ok {
			return d, true, llm.FallthroughKindLLM, res.Output.Reason, "", res.Usage, nil
		}
		return llm.Decision{}, false, llm.FallthroughKindLLM, res.Output.Reason, "", res.Usage, nil
	}
}

// redactProviderError strips the SDK error of its raw response
// body before the runner logs it / writes it to metrics. Both
// anthropic-sdk-go and openai-go build Error.Error() by embedding
// the entire response body, which a misbehaving proxy / gateway can
// populate with Authorization headers, request signatures, or echo
// of the submitted credential. ccgate.log is `0644` and the
// metrics JSONL is consumed by the user's other tooling, so we
// can't risk that surface carrying secrets through.
//
// Known SDK error types collapse to "<provider> <statusCode>".
// Anything else (transport / context / parse) keeps its original
// shape because we built those messages ourselves and they don't
// echo provider response bodies.
func redactProviderError(providerName string, err error) error {
	if err == nil {
		return nil
	}
	var anth *anthropicsdk.Error
	if errors.As(err, &anth) {
		return fmt.Errorf("%s API error (status %d)", providerName, anth.StatusCode)
	}
	var oai *openaisdk.Error
	if errors.As(err, &oai) {
		return fmt.Errorf("%s API error (status %d)", providerName, oai.StatusCode)
	}
	return err
}

// providerAuthStatus type-checks the error against the
// anthropic-sdk-go and openai-go SDK error envelopes (gemini
// delegates to openai internally) and reports whether it is a
// credential-rejection response (HTTP 401 or 403). Other 4xx/5xx
// responses keep the existing exit-1 behaviour because they signal
// user-recoverable issues we cannot auto-resolve from the keystore.
//
// The status code is returned alongside the boolean so callers can
// log it without dragging the raw error message (which the SDKs
// build by concatenating the response body — proxies and brokers
// sometimes echo credentials there) into ccgate.log.
//
// We deliberately do not parse the SDK error's body for a code-name
// classifier (e.g. AWS `ExpiredTokenException`). ccgate's supported
// provider paths (anthropic-sdk-go, openai-go, gemini via
// openai-compat) all surface credential rejection as 401, and the
// only other 401/403 source we anticipate today is the mostly
// theoretical Bedrock-via-OpenAI-compat case tracked under #62. A
// blanket "401 or 403 ⇒ credential rejected" is the same rule that
// shipped before the auth.* refactor and matches the conservative
// "rotate sooner rather than later" failure mode.
func providerAuthStatus(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var anth *anthropicsdk.Error
	if errors.As(err, &anth) {
		if anth.StatusCode == http.StatusUnauthorized || anth.StatusCode == http.StatusForbidden {
			return anth.StatusCode, true
		}
		return anth.StatusCode, false
	}
	var oai *openaisdk.Error
	if errors.As(err, &oai) {
		if oai.StatusCode == http.StatusUnauthorized || oai.StatusCode == http.StatusForbidden {
			return oai.StatusCode, true
		}
		return oai.StatusCode, false
	}
	return 0, false
}

// invalidateAuthCache asks keystore to forget the cached credential
// so the next hook fire forces a fresh helper exec. Only callable
// when auth.type=exec actually produced a cache file; the file
// branch and the env-var path never wrote one.
//
// We rebuild the same Options keystore.Resolve was given on the
// preceding fire so CacheFingerprint hashes to the same path.
func invalidateAuthCache(p config.ProviderConfig, target, providerName, baseURL string) {
	if p.Auth == nil || p.Auth.Type != config.AuthTypeExec {
		return
	}
	shell := p.Auth.Shell
	if shell == "" {
		shell = config.AuthShellBash
	}
	opts := keystore.Options{
		Shell:        shell,
		Command:      p.Auth.Command,
		ProviderName: providerName,
		BaseURL:      baseURL,
		TargetName:   target,
		CacheKey:     p.Auth.CacheKey,
	}
	if err := keystore.Invalidate(opts); err != nil {
		// Use a unique log-only attribute so triage can tell this
		// apart from "the cache write step on a fresh helper run
		// failed" (cache_write). The hook still falls through
		// successfully — the next fire just won't get the benefit
		// of having the bad credential pre-removed.
		slog.Warn("keystore: cache invalidate failed",
			"event", "cache_invalidate_failed",
			"source", string(keystore.SourceCache),
			"error", err,
		)
	}
}

// PromptInput is the user-message JSON the LLM sees. It is the
// shared payload shape across targets: fields that one of the two
// targets does not deliver carry omitempty so they disappear from
// the JSON instead of showing up as empty values. The prompt's
// TargetSection (set via WithPromptSection) tells the LLM which
// fields are / are not present.
type PromptInput struct {
	ToolName              string            `json:"tool_name"`
	ToolInput             HookToolInput     `json:"tool_input"`
	ToolInputRaw          json.RawMessage   `json:"tool_input_raw,omitempty"`
	PermissionMode        string            `json:"permission_mode,omitempty"`
	PermissionSuggestions []json.RawMessage `json:"permission_suggestions,omitempty"`
	Context               Context           `json:"context"`
	SettingsPermissions   any               `json:"settings_permissions,omitempty"`
	RecentTranscript      any               `json:"recent_transcript,omitempty"`
	Model                 string            `json:"model,omitempty"`
	TurnID                string            `json:"turn_id,omitempty"`
}

// Context bundles the working-directory + git information ccgate
// surfaces to the LLM together with the tool-derived
// referenced_paths. Wraps gitutil.Context with the path-extraction
// output so both live in one nested object the LLM can navigate by
// name.
type Context struct {
	gitutil.Context
	ReferencedPaths []string `json:"referenced_paths,omitempty"`
}

func buildPrompt(cfg config.Config, in HookInput, ro runtimeOptions) (llm.Prompt, error) {
	pi := PromptInput{
		ToolName:              in.ToolName,
		ToolInput:             in.ToolInput,
		ToolInputRaw:          in.ToolInputRaw,
		PermissionMode:        in.PermissionMode,
		PermissionSuggestions: in.PermissionSuggestions,
		Model:                 in.Model,
		TurnID:                in.TurnID,
		Context: Context{
			Context:         gitutil.BuildContext(in.Cwd),
			ReferencedPaths: referencedPaths(in),
		},
	}
	if ro.loadStaticPermissions != nil {
		pi.SettingsPermissions = ro.loadStaticPermissions(in.Cwd)
	}
	if ro.loadRecentTranscript != nil && in.TranscriptPath != "" {
		pi.RecentTranscript = ro.loadRecentTranscript(in.TranscriptPath)
	}
	user, err := json.MarshalIndent(pi, "", "  ")
	if err != nil {
		return llm.Prompt{}, fmt.Errorf("marshal prompt input: %w", err)
	}
	p := prompt.Build(prompt.Args{
		TargetName:          ro.targetName,
		PlanMode:            in.PermissionMode == PermissionModePlan,
		HasRecentTranscript: ro.hasRecentTranscript,
		TargetSection:       ro.promptSection,
		Allow:               cfg.Allow,
		Deny:                cfg.Deny,
		Environment:         cfg.Environment,
		UserPayload:         string(user),
	})
	p.Model = cfg.Provider.Model
	p.TimeoutMS = cfg.GetTimeoutMS()
	return p, nil
}

func redactedUserMessage(user string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(user), &m); err != nil {
		return "{}"
	}
	// tool_input_raw carries the verbatim upstream payload alongside
	// the parsed view -- redact both so file bodies (Edit/Write
	// content_updates) and Bash arguments (tokens, secrets) never
	// reach ccgate.log even when the parsed view alone would have.
	for _, k := range []string{"tool_input", "tool_input_raw", "permission_suggestions", "recent_transcript"} {
		if _, ok := m[k]; ok {
			m[k] = "[REDACTED]"
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(out)
}

// resolveAPIKey decides where the provider API key comes from on
// this hook fire and returns the credential plus the metrics-friendly
// classifiers we need to log and aggregate failures.
//
// The order is fixed:
//
//  1. provider.auth (auth.type=exec / auth.type=file) via
//     internal/keystore. Configured-but-failing helpers fall through
//     with credential_unavailable rather than silently dropping back
//     to env vars (silent fallback would mask helper bugs).
//  2. CCGATE_<PROVIDER>_API_KEY then <PROVIDER>_API_KEY for the
//     known providers; anything else returns unknown_provider.
//  3. Nothing set: no_apikey for the known providers.
//
// Unknown providers short-circuit BEFORE the auth branch runs.
// Otherwise a typo like provider.name = "opena1" with an
// auth.command that returns an OpenAI-shaped key would still be
// handed to newProviderClient, which falls back to the Anthropic
// SDK by default — i.e. ccgate would send the wrong provider's
// credential to Anthropic. We refuse to resolve anything when the
// provider name is unrecognised.
//
// Returns (key, fallthroughKind, reason, source, err). On success
// `key` is non-empty and `fallthroughKind` is empty; on
// fallthrough-class outcomes `key` is empty and the caller emits
// the upstream prompt without exiting 1. `err` is only set when
// keystore.Resolve produced one (used for log enrichment, not for
// hook exit).
func resolveAPIKey(ctx context.Context, p config.ProviderConfig, providerName, target string) (string, string, string, string, error) {
	if !isKnownProvider(providerName) {
		slog.Info("unknown provider, falling through", "provider", providerName)
		return "", llm.FallthroughKindUnknownProvider, "", "", nil
	}

	if p.Auth != nil {
		opts := keystore.Options{
			ProviderName:   providerName,
			BaseURL:        strings.TrimSpace(p.BaseURL),
			TargetName:     target,
			RefreshMargin:  p.Auth.GetRefreshMargin(),
			CommandTimeout: p.Auth.GetTimeout(),
		}
		switch p.Auth.Type {
		case config.AuthTypeExec:
			opts.Command = p.Auth.Command
			opts.CacheKey = p.Auth.CacheKey
			opts.Shell = p.Auth.Shell
			if opts.Shell == "" {
				opts.Shell = config.AuthShellBash
			}
		case config.AuthTypeFile:
			if p.Auth.Path != nil {
				opts.Path = *p.Auth.Path
			} else {
				opts.Path = config.DefaultAuthPath(target)
			}
		}
		res, err := keystore.Resolve(ctx, opts)
		if err != nil {
			slog.Warn("keystore: api key resolution failed",
				"kind", llm.FallthroughKindCredentialUnavailable,
				"reason", string(res.Reason),
				"source", string(res.Source),
				"error", err,
			)
			return "", llm.FallthroughKindCredentialUnavailable, string(res.Reason), string(res.Source), err
		}
		return res.Key, "", "", string(res.Source), nil
	}

	var primary, fallback string
	switch providerName {
	case "openai":
		primary, fallback = "CCGATE_OPENAI_API_KEY", "OPENAI_API_KEY"
	case "gemini":
		primary, fallback = "CCGATE_GEMINI_API_KEY", "GEMINI_API_KEY"
	default: // anthropic
		primary, fallback = "CCGATE_ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY"
	}
	if key := strings.TrimSpace(os.Getenv(primary)); key != "" {
		return key, "", "", "", nil
	}
	if key := strings.TrimSpace(os.Getenv(fallback)); key != "" {
		return key, "", "", "", nil
	}
	slog.Warn("no API key found", "provider", providerName)
	return "", llm.FallthroughKindNoAPIKey, "", "", nil
}

// isKnownProvider gates every other code path on this file. Keep
// this list in sync with newProviderClient's switch — the two
// together define the universe of providers ccgate will route a
// real key to.
func isKnownProvider(name string) bool {
	switch name {
	case "anthropic", "openai", "gemini":
		return true
	}
	return false
}

func newProviderClient(providerName, apiKey, baseURL string) llm.Provider {
	switch providerName {
	case "openai":
		return &openai.Client{APIKey: apiKey, BaseURL: baseURL}
	case "gemini":
		return &gemini.Client{APIKey: apiKey, BaseURL: baseURL}
	default: // anthropic
		return &anthropic.Client{APIKey: apiKey, BaseURL: baseURL}
	}
}

const maxTruncateLen = 200

func buildMetricsEntry(
	start time.Time,
	elapsed time.Duration,
	in HookInput,
	cfg config.Config,
	decision llm.Decision,
	hasDecision bool,
	kind string,
	llmReason string,
	credSource string,
	usage *llm.Usage,
	err error,
) metrics.Entry {
	entry := metrics.Entry{
		Timestamp:      start,
		SessionID:      in.SessionID,
		ToolName:       in.ToolName,
		PermissionMode: in.PermissionMode,
		Model:          cfg.Provider.Model,
		ElapsedMS:      elapsed.Milliseconds(),
	}

	switch {
	case err != nil:
		entry.Decision = "error"
		entry.Error = truncateStr(err.Error(), maxTruncateLen)
	case hasDecision:
		entry.Decision = decision.Behavior
		if decision.Behavior == llm.BehaviorDeny {
			entry.DenyMessage = decision.Message
		}
		entry.Reason = truncateStr(llmReason, maxTruncateLen)
		if kind == llm.FallthroughKindLLM {
			entry.FallthroughKind = kind
			entry.Forced = true
		}
	default:
		entry.Decision = "fallthrough"
		entry.FallthroughKind = kind
		entry.Reason = truncateStr(llmReason, maxTruncateLen)
		if kind == llm.FallthroughKindCredentialUnavailable {
			entry.CredentialSource = credSource
		}
	}

	if usage != nil {
		entry.InputTokens = usage.InputTokens
		entry.OutputTokens = usage.OutputTokens
	}

	entry.ToolInput = metrics.CapToolInput(metrics.ToolInputFields{
		Command:  in.ToolInput.Command,
		FilePath: in.ToolInput.FilePath,
		Path:     in.ToolInput.Path,
		Pattern:  in.ToolInput.Pattern,
	})
	return entry
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func initLogger(logPath string, disabled bool, maxLogSize int64) (*slog.Logger, func()) {
	if disabled {
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1})), func() {}
	}
	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		slog.Warn("failed to create log directory", "error", err)
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {}
	}
	metrics.RotateIfNeeded(logPath, maxLogSize)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to open log file %s: %v\n", logPath, err)
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {}
	}
	w := &atomicWriter{f: f}
	return slog.New(slog.NewTextHandler(w, nil)), func() { _ = f.Close() }
}

type atomicWriter struct {
	f  *os.File
	mu sync.Mutex
}

func (w *atomicWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Write(p)
}
