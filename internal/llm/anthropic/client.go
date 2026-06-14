// Package anthropic implements llm.Provider against the Anthropic
// Messages API. The client is target-agnostic: callers (cmd/claude,
// cmd/codex) build their own Prompt and feed it through Decide.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicconfig "github.com/anthropics/anthropic-sdk-go/config"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/tak848/ccgate/internal/llm"
)

const (
	maxTokens  = 4096
	maxRetries = 5
)

// ErrNoAPIKey is returned by Decide when neither
// CCGATE_ANTHROPIC_API_KEY nor ANTHROPIC_API_KEY is set, and no
// alternative credential source (auth.type=profile) was declared.
// Callers should treat this as a fallthrough (not a hard error) so
// the hook degrades gracefully when the user has not configured a
// credential.
var ErrNoAPIKey = errors.New("anthropic: no credential available (CCGATE_ANTHROPIC_API_KEY / ANTHROPIC_API_KEY / auth.type=profile)")

// ErrProfileUnavailable signals that a declared `auth.type=profile`
// could not produce a usable credential before the SDK's
// request-time middleware would have been called. runner.decide()
// converts this into a kind=credential_unavailable, reason=profile_load
// fallthrough so the upstream tool prompts the user instead of the
// hook crashing.
//
// Wrap range (intentionally narrow):
//
//   - LoadConfig / LoadProfile failures (config file missing / parse
//     errors / SDK validateProfileName rejecting a typo'd name).
//   - user_oauth credentials file preflight failures (file missing,
//     stat error).
//
// Excluded: SDK request-time errors (refresh-token failures, on-disk
// shape / permission errors). Those live behind the SDK's
// internal/auth package and cannot be type-asserted from outside —
// they keep the existing exit-1 path until upstream surfaces a
// stable typed error. Issue #67 (`ccgate doctor` + best-effort
// notifications) is the planned user-facing surface for those.
var ErrProfileUnavailable = errors.New("anthropic: profile credentials unavailable")

// Client is a stateless wrapper around the Anthropic SDK that
// implements llm.Provider. Either APIKey or UseProfile must be set;
// BaseURL lets tests point the client at a httptest.Server.
type Client struct {
	APIKey  string
	BaseURL string

	// Profile is the named Anthropic profile to load when UseProfile
	// is true. Empty Profile + UseProfile=true delegates resolution
	// to anthropicconfig.LoadConfig (env / active_config / "default").
	Profile string

	// UseProfile selects the profile-delegation path: ccgate calls
	// anthropicconfig.LoadProfile / LoadConfig directly and feeds
	// the resulting *Config into option.WithConfig. (Env-default
	// suppression via option.WithoutEnvironmentDefaults is applied
	// unconditionally by Decide for every mode, not just this one.)
	UseProfile bool
}

// Decide sends a single classification request and parses the
// structured response into llm.Result.
func (c *Client) Decide(ctx context.Context, p llm.Prompt) (llm.Result, error) {
	if !c.UseProfile && c.APIKey == "" {
		return llm.Result{}, ErrNoAPIKey
	}

	if p.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	// WithoutEnvironmentDefaults is unconditional: ccgate always resolves
	// its own credential (exec/file via keystore, profile via the SDK
	// loader, or a *_API_KEY env var it reads itself), so the SDK must
	// contribute nothing from the ambient environment. Without it,
	// anthropic.NewClient prepends DefaultClientOptions, which autoloads a
	// stray ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN / ANTHROPIC_BASE_URL or
	// an on-disk fallback profile (active_config / "default"). In the
	// WithAPIKey branch a leftover default profile then gets shadowed by our
	// explicit key, which makes the SDK emit a misleading "ANTHROPIC_API_KEY
	// is set ... takes precedence over the profile" warning even though the
	// env var is empty/unset. Suppressing env defaults keeps every mode
	// deterministic: only ccgate's resolved credential and configured
	// base_url reach the request.
	opts := []option.RequestOption{
		option.WithMaxRetries(maxRetries),
		option.WithoutEnvironmentDefaults(),
	}
	if c.UseProfile {
		profileOpts, err := c.resolveProfileOptions()
		if err != nil {
			return llm.Result{}, err
		}
		opts = append(opts, profileOpts...)
	} else {
		opts = append(opts, option.WithAPIKey(c.APIKey))
	}
	if c.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(c.BaseURL))
	}
	client := anthropicsdk.NewClient(opts...)

	schema, err := outputSchema()
	if err != nil {
		return llm.Result{}, fmt.Errorf("generate output schema: %w", err)
	}

	message, err := client.Messages.New(ctx, anthropicsdk.MessageNewParams{
		Model:     anthropicsdk.Model(p.Model),
		MaxTokens: maxTokens,
		System:    []anthropicsdk.TextBlockParam{{Text: p.System}},
		Messages: []anthropicsdk.MessageParam{
			anthropicsdk.NewUserMessage(anthropicsdk.NewTextBlock(p.User)),
		},
		OutputConfig: anthropicsdk.OutputConfigParam{
			Format: anthropicsdk.JSONOutputFormatParam{Schema: schema},
		},
		Temperature: anthropicsdk.Float(0),
	})
	if err != nil {
		// SDK request-time OAuth refresh / credential resolution
		// failures live in internal/auth and cannot be type-asserted.
		// They keep the existing exit-1 path; runner.redactProviderError
		// strips any chatty proxy body before logging.
		return llm.Result{}, fmt.Errorf("anthropic API: %w", err)
	}

	usage := &llm.Usage{
		InputTokens:  message.Usage.InputTokens,
		OutputTokens: message.Usage.OutputTokens,
	}

	if message.StopReason == anthropicsdk.StopReasonMaxTokens || message.StopReason == anthropicsdk.StopReasonRefusal {
		slog.Warn("anthropic response truncated or refused", "stop_reason", message.StopReason)
		return llm.Result{Usage: usage, Unusable: true}, nil
	}

	text := extractMessageText(message)
	slog.Info("anthropic response", "raw", text)
	if text == "" {
		slog.Warn("anthropic response had no text content")
		return llm.Result{Usage: usage, Unusable: true}, nil
	}

	var output llm.Output
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		return llm.Result{Usage: usage}, fmt.Errorf("parse LLM response: %w", err)
	}
	if output.Behavior == llm.BehaviorDeny && strings.TrimSpace(output.DenyMessage) == "" {
		output.DenyMessage = llm.DefaultDenyMessage
	}

	return llm.Result{Output: output, Usage: usage}, nil
}

// resolveProfileOptions runs the profile-mode credential pipeline
// (LoadProfile / LoadConfig + preflight) and returns the SDK options
// that wire the resolved config into NewClient. Any failure surfaces
// as ErrProfileUnavailable so runner.decide() can route the
// fallthrough.
func (c *Client) resolveProfileOptions() ([]option.RequestOption, error) {
	cfg, err := loadProfileConfig(c.Profile)
	if err != nil {
		// Don't %w-wrap the SDK error: its message can include the
		// resolved config path. Log the secret-free error_class via
		// slog and return a sanitized sentinel.
		slog.Warn("anthropic profile load failed",
			"kind", llm.FallthroughKindCredentialUnavailable,
			"reason", "profile_load",
			"source", "profile",
			"error_class", classifyProfileLoadError(err),
			"profile_name_set", c.Profile != "",
		)
		return nil, fmt.Errorf("%w: load profile", ErrProfileUnavailable)
	}

	if err := preflightCredentials(cfg); err != nil {
		return nil, err
	}

	slog.Info("credential source selected",
		"source", "profile",
		"profile_name_set", c.Profile != "",
	)
	// Only the profile wiring lives here; env-default suppression
	// (option.WithoutEnvironmentDefaults) is applied unconditionally by
	// Decide for every mode, so it is not repeated here.
	return []option.RequestOption{
		option.WithConfig(cfg),
	}, nil
}

// loadProfileConfig calls the SDK's profile loader. Empty profile
// goes through LoadConfig (env / active_config / "default"), a named
// profile bypasses the resolution chain via LoadProfile.
func loadProfileConfig(profile string) (*anthropicconfig.Config, error) {
	if profile != "" {
		return anthropicconfig.LoadProfile(anthropicconfig.DefaultDir(), profile)
	}
	return anthropicconfig.LoadConfig()
}

// preflightCredentials runs the user_oauth-only credentials-file
// existence check before letting the SDK middleware try to read it.
// oidc_federation profiles skip the check because cache misses are
// expected (the SDK does a fresh token exchange).
//
// Stat errors are split: ErrNotExist becomes credentials_missing
// (recovery: `ant auth login --profile <name>`); anything else
// (permission denied, ENOTDIR, ...) becomes credentials_stat_failed
// because re-running ant would not fix it.
func preflightCredentials(cfg *anthropicconfig.Config) error {
	if cfg.AuthenticationInfo == nil ||
		cfg.AuthenticationInfo.Type != anthropicconfig.AuthenticationTypeUserOAuth ||
		cfg.AuthenticationInfo.CredentialsPath == "" {
		return nil
	}
	if _, statErr := os.Stat(cfg.AuthenticationInfo.CredentialsPath); statErr != nil {
		if !errors.Is(statErr, fs.ErrNotExist) {
			slog.Warn("anthropic profile credentials stat failed",
				"kind", llm.FallthroughKindCredentialUnavailable,
				"reason", "profile_load",
				"source", "profile",
				"error_class", "credentials_stat_failed",
			)
			return fmt.Errorf("%w: credentials stat failed", ErrProfileUnavailable)
		}
		slog.Warn("anthropic profile credentials missing",
			"kind", llm.FallthroughKindCredentialUnavailable,
			"reason", "profile_load",
			"source", "profile",
			"error_class", "credentials_missing",
		)
		return fmt.Errorf("%w: credentials missing (preflight)", ErrProfileUnavailable)
	}
	return nil
}

// classifyProfileLoadError buckets a LoadProfile / LoadConfig error
// into one of four secret-free labels. The SDK does not export
// dedicated error sentinels for the semantic cases (auth block
// missing, name validation, etc.), so anything that isn't an
// obvious not-exist / JSON syntax error lands in
// "profile_config_invalid" as a catch-all bucket. Recovery guidance
// in docs/api-key-helper.md keys off these labels.
func classifyProfileLoadError(err error) string {
	if errors.Is(err, fs.ErrNotExist) {
		return "profile_config_missing"
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return "profile_config_parse"
	}
	if err != nil {
		return "profile_config_invalid"
	}
	return "unknown"
}

func outputSchema() (map[string]any, error) {
	schema, err := jsonschema.For[llm.Output](nil)
	if err != nil {
		return nil, fmt.Errorf("build schema: %w", err)
	}
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	return out, nil
}

func extractMessageText(message *anthropicsdk.Message) string {
	if message == nil {
		return ""
	}
	var text strings.Builder
	for _, block := range message.Content {
		switch variant := block.AsAny().(type) {
		case anthropicsdk.TextBlock:
			text.WriteString(variant.Text)
		}
	}
	return strings.TrimSpace(text.String())
}
