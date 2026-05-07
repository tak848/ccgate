// Package anthropic implements llm.Provider against the Anthropic
// Messages API. The client is target-agnostic: callers (cmd/claude,
// cmd/codex) build their own Prompt and feed it through Decide.
package anthropic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	anthropicconfig "github.com/anthropics/anthropic-sdk-go/config"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/gofrs/flock"
	"github.com/invopop/jsonschema"

	"github.com/tak848/ccgate/internal/llm"
)

const (
	maxTokens  = 4096
	maxRetries = 5

	// autoLoginLockWait bounds how long bootstrapWithAnt waits for the
	// per-profile flock before falling through. Short on purpose:
	// blocking the hook longer makes Claude Code feel hung, and a
	// concurrent fire that already started ant will publish credentials
	// the next retry can read.
	autoLoginLockWait = 3 * time.Second

	// autoLoginKillCapMargin is added to the user-supplied auth.timeout_ms
	// before ccgate hard-kills the ant subprocess. ant should exit on
	// its own via --timeout; this is a backstop, not the primary cap.
	autoLoginKillCapMargin = 30 * time.Second
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
//     stat error, custom credentials_path that auto_login cannot
//     bootstrap).
//   - ant auto-login subprocess failures (lock contention, ant binary
//     missing, timeout, non-zero exit, post-login credential still
//     missing).
//
// Excluded: SDK request-time errors (refresh-token failures, on-disk
// shape / permission errors). Those live behind the SDK's
// internal/auth package and cannot be type-asserted from outside —
// they keep the existing exit-1 path until upstream surfaces a
// stable typed error.
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
	// the resulting *Config into option.WithConfig. ANTHROPIC_API_KEY
	// / ANTHROPIC_AUTH_TOKEN env vars are suppressed via
	// option.WithoutEnvironmentDefaults so a leftover env never
	// shadows the declared profile.
	UseProfile bool

	// AutoLogin opts in (Beta) to spawning `ant auth login --profile <Profile>`
	// when preflight detects the credentials file is missing. Requires
	// a non-empty Profile (validated upstream and re-checked here as
	// defense in depth).
	AutoLogin bool

	// AutoLoginTimeout is the value passed to ant via --timeout. The
	// ccgate context kill cap is this value + autoLoginKillCapMargin.
	// Zero or negative falls back to the SDK-default behavior of ant
	// (5 min); production callers always supply a positive value via
	// runner.newProviderClient.
	AutoLoginTimeout time.Duration
}

// Decide sends a single classification request and parses the
// structured response into llm.Result.
//
// The flow has two distinct stages:
//
//  1. Credential resolution. For profile mode this is LoadProfile /
//     LoadConfig + (optionally) auto-login bootstrap. We deliberately
//     do NOT apply p.TimeoutMS here — the user-supplied
//     provider.timeout_ms (default 20 s) is a per-request cap meant
//     for the LLM API call, and clamping a 6-minute browser OAuth
//     bootstrap to it would never let auto_login finish.
//  2. LLM API call. p.TimeoutMS applies here via apiCtx.
//
// The split also means cancellation (Ctrl-C / SIGTERM) propagates
// through ctx all the way to the ant subprocess: runner builds ctx
// with signal.NotifyContext and passes it unchanged.
func (c *Client) Decide(ctx context.Context, p llm.Prompt) (llm.Result, error) {
	if !c.UseProfile && c.APIKey == "" {
		return llm.Result{}, ErrNoAPIKey
	}

	opts := []option.RequestOption{option.WithMaxRetries(maxRetries)}
	if c.UseProfile {
		profileOpts, err := c.resolveProfileOptions(ctx)
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

	apiCtx := ctx
	if p.TimeoutMS > 0 {
		var cancel context.CancelFunc
		apiCtx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	schema, err := outputSchema()
	if err != nil {
		return llm.Result{}, fmt.Errorf("generate output schema: %w", err)
	}

	message, err := client.Messages.New(apiCtx, anthropicsdk.MessageNewParams{
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
// (LoadProfile / LoadConfig + preflight + optional ant bootstrap)
// and returns the SDK options that wire the resolved config into
// NewClient. Any failure surfaces as ErrProfileUnavailable so
// runner.decide() can route the fallthrough.
func (c *Client) resolveProfileOptions(ctx context.Context) ([]option.RequestOption, error) {
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

	if err := c.preflightCredentials(ctx, cfg); err != nil {
		return nil, err
	}

	slog.Info("credential source selected",
		"source", "profile",
		"profile_name_set", c.Profile != "",
	)
	return []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
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
// On missing credentials the function may, under AutoLogin, hand off
// to bootstrapWithAnt and re-check. Any failure is wrapped in
// ErrProfileUnavailable.
func (c *Client) preflightCredentials(ctx context.Context, cfg *anthropicconfig.Config) error {
	if cfg.AuthenticationInfo == nil ||
		cfg.AuthenticationInfo.Type != anthropicconfig.AuthenticationTypeUserOAuth ||
		cfg.AuthenticationInfo.CredentialsPath == "" {
		return nil
	}
	credentialsPath := cfg.AuthenticationInfo.CredentialsPath
	if _, statErr := os.Stat(credentialsPath); statErr != nil {
		if !errors.Is(statErr, fs.ErrNotExist) {
			slog.Warn("anthropic profile credentials stat failed",
				"kind", llm.FallthroughKindCredentialUnavailable,
				"reason", "profile_load",
				"source", "profile",
				"error_class", "credentials_stat_failed",
			)
			return fmt.Errorf("%w: credentials stat failed", ErrProfileUnavailable)
		}
		if !c.AutoLogin {
			slog.Warn("anthropic profile credentials missing",
				"kind", llm.FallthroughKindCredentialUnavailable,
				"reason", "profile_load",
				"source", "profile",
				"error_class", "credentials_missing",
			)
			return fmt.Errorf("%w: credentials missing (preflight)", ErrProfileUnavailable)
		}
		// Defense in depth: validate rejects auto_login=true with an
		// empty name, but a Client constructed directly (tests, future
		// callers) might still hit this branch.
		if c.Profile == "" {
			slog.Warn("auto_login requires non-empty profile name",
				"kind", llm.FallthroughKindCredentialUnavailable,
				"reason", "profile_load",
				"source", "profile",
				"error_class", "auto_login_requires_profile",
			)
			return fmt.Errorf("%w: auto_login requires non-empty profile name", ErrProfileUnavailable)
		}
		// ant always writes to the SDK default credentials path. If
		// the profile config aimed elsewhere, bootstrapping would
		// publish credentials the SDK middleware never reads —
		// deterministic infinite-fail loop. Fail fast so the user can
		// either drop the custom path or disable auto_login.
		defaultPath := anthropicconfig.ProfileCredentialsPath(anthropicconfig.DefaultDir(), c.Profile)
		if filepath.Clean(credentialsPath) != filepath.Clean(defaultPath) {
			slog.Warn("auto_login unsupported with custom credentials_path",
				"kind", llm.FallthroughKindCredentialUnavailable,
				"reason", "profile_load",
				"source", "profile",
				"error_class", "credentials_path_auto_login_unsupported",
			)
			return fmt.Errorf("%w: auto_login requires SDK default credentials_path", ErrProfileUnavailable)
		}
		if loginErr := bootstrapWithAnt(ctx, c.Profile, credentialsPath, c.AutoLoginTimeout); loginErr != nil {
			return fmt.Errorf("%w: ant auto-login", ErrProfileUnavailable)
		}
		// Re-check post-login. ant exits 0 only after writing the
		// credentials file synchronously, so a missing file here is
		// the rare ant-side bug case rather than a normal failure.
		if _, statErr := os.Stat(credentialsPath); statErr != nil {
			slog.Warn("anthropic profile credentials still missing after ant auto-login",
				"kind", llm.FallthroughKindCredentialUnavailable,
				"reason", "profile_load",
				"source", "profile",
				"error_class", "credentials_missing_after_login",
			)
			return fmt.Errorf("%w: credentials missing after ant auto-login", ErrProfileUnavailable)
		}
		slog.Info("ant auto-login succeeded", "source", "profile", "profile_name_set", true)
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

// bootstrapWithAnt spawns `ant auth login --profile <profile>` to
// produce credentials when preflight has detected the file is
// missing. It runs under a short per-profile flock to keep two
// concurrent hook fires from racing the same browser callback (one
// of them would just retry the auth dance for nothing).
//
// Notes:
//
//   - Lock scope is global per profile (target-agnostic): both
//     Claude Code and Codex CLI hooks share one ant invocation.
//     Lock contention is treated as a fallthrough rather than a
//     long block — the next hook fire reads the published
//     credentials.
//   - ccgate intentionally does NOT save / restore active_config
//     here. ant overwrites it as a side effect of `--profile`, and
//     mitigating that race in ccgate would be racy in its own
//     right. Upstream PR anthropics/anthropic-cli#45 adds
//     `--no-activate` for a real fix; once it lands we will pass
//     the flag and drop the docs warning.
//   - stdout / stderr are discarded. ant prints token-bearing
//     diagnostics to both, and ccgate.log is mode 0644.
func bootstrapWithAnt(ctx context.Context, profile, credentialsPath string, antTimeout time.Duration) error {
	if profile == "" {
		return errors.New("auto-login requires non-empty profile name")
	}
	dir := anthropicconfig.DefaultDir()
	lockPath, err := autoLoginLockPath(dir, profile)
	if err != nil {
		slog.Warn("auto_login lock path setup failed",
			"kind", llm.FallthroughKindCredentialUnavailable,
			"reason", "profile_load",
			"source", "profile",
			"error_class", "ant_lock_unavailable",
		)
		return fmt.Errorf("auto_login lock path: %w", err)
	}
	fl := flock.New(lockPath)
	lockCtx, lockCancel := context.WithTimeout(ctx, autoLoginLockWait)
	defer lockCancel()
	locked, lockErr := fl.TryLockContext(lockCtx, 100*time.Millisecond)
	if lockErr != nil || !locked {
		slog.Warn("auto_login lock acquisition failed",
			"kind", llm.FallthroughKindCredentialUnavailable,
			"reason", "profile_load",
			"source", "profile",
			"error_class", "ant_lock_unavailable",
		)
		return errors.New("acquire auto_login lock")
	}
	defer func() { _ = fl.Unlock() }()

	// Re-check inside the lock: if a sibling fire just published
	// credentials we can skip the bootstrap entirely.
	if _, statErr := os.Stat(credentialsPath); statErr == nil {
		return nil
	}

	cmdTimeout := antTimeout + autoLoginKillCapMargin
	if antTimeout <= 0 {
		// Defensive: a zero AutoLoginTimeout would collapse the kill
		// cap to autoLoginKillCapMargin. Production callers always
		// pass a positive value; this branch is for hand-built Clients.
		cmdTimeout = autoLoginKillCapMargin
	}
	cmdCtx, cmdCancel := context.WithTimeout(ctx, cmdTimeout)
	defer cmdCancel()
	args := []string{"auth", "login", "--profile", profile}
	if antTimeout > 0 {
		args = append(args, "--timeout", antTimeout.String())
	}
	cmd := exec.CommandContext(cmdCtx, "ant", args...)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmdErr := cmd.Run(); cmdErr != nil {
		return classifyAntError(cmdCtx, cmdErr)
	}
	return nil
}

// classifyAntError translates an exec.Cmd.Run() error from the ant
// subprocess into a sanitized sentinel + structured slog warning.
// The raw error is intentionally not %w-wrapped because exec.Error
// includes the looked-up PATH and exec.ExitError can include
// stdout/stderr — neither belongs in ccgate.log.
func classifyAntError(ctx context.Context, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		slog.Warn("ant binary not found on PATH",
			"kind", llm.FallthroughKindCredentialUnavailable,
			"reason", "profile_load",
			"source", "profile",
			"error_class", "ant_not_found",
		)
		return errors.New("ant auto-login: not found")
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		slog.Warn("ant lookup failed",
			"kind", llm.FallthroughKindCredentialUnavailable,
			"reason", "profile_load",
			"source", "profile",
			"error_class", "ant_lookup_failed",
		)
		return errors.New("ant auto-login: lookup failed")
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		slog.Warn("ant subprocess timed out",
			"kind", llm.FallthroughKindCredentialUnavailable,
			"reason", "profile_load",
			"source", "profile",
			"error_class", "ant_timeout",
		)
		return errors.New("ant auto-login: timeout")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		slog.Warn("ant subprocess exited non-zero",
			"kind", llm.FallthroughKindCredentialUnavailable,
			"reason", "profile_load",
			"source", "profile",
			"error_class", "ant_failed",
			"exit_code", exitErr.ExitCode(),
		)
		return errors.New("ant auto-login: failed")
	}
	slog.Warn("ant subprocess failed (unclassified)",
		"kind", llm.FallthroughKindCredentialUnavailable,
		"reason", "profile_load",
		"source", "profile",
		"error_class", "unknown",
	)
	return errors.New("ant auto-login: unknown")
}

// autoLoginLockPath returns a profile-scoped lock file path under
// ccgate's state directory. The hash key combines the resolved
// config dir (so different ANTHROPIC_CONFIG_DIR values do not share
// a lock) and the profile name (so two profiles can bootstrap in
// parallel). Per-profile granularity is the right scope: the only
// race the lock prevents is two callers asking ant to publish the
// same credentials file at once. It does NOT serialize active_config
// writes across profiles — ccgate does not touch active_config.
func autoLoginLockPath(configDir, profile string) (string, error) {
	dir := stateBaseDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create state dir: %w", err)
	}
	sum := sha256.Sum256([]byte(configDir + "\x00" + profile))
	name := "auto_login." + hex.EncodeToString(sum[:8]) + ".lock"
	return filepath.Join(dir, name), nil
}

// stateBaseDir mirrors internal/config.stateDir() (which is
// unexported). Anthropic-only state lives directly under the ccgate
// root, sibling to the per-target subdirectories — auto_login locks
// do not belong to a single target.
func stateBaseDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "ccgate")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "ccgate")
	}
	return "."
}

func outputSchema() (map[string]any, error) {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	schema := reflector.Reflect(llm.Output{})
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
