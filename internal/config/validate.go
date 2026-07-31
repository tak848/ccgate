package config

import (
	"errors"
	"fmt"
	"strings"
)

// Validate checks Config invariants. Returns an error describing all violations.
func (c Config) Validate() error {
	var errs []error
	if strings.TrimSpace(c.Provider.Name) == "" {
		errs = append(errs, fmt.Errorf("provider.name must not be empty"))
	}
	if strings.TrimSpace(c.Provider.Model) == "" {
		errs = append(errs, fmt.Errorf("provider.model must not be empty"))
	}
	if c.Provider.TimeoutMS != nil && *c.Provider.TimeoutMS < 0 {
		errs = append(errs, fmt.Errorf("provider.timeout_ms must not be negative, got %d", *c.Provider.TimeoutMS))
	}
	if err := validateAuth(c.Provider.Auth); err != nil {
		errs = append(errs, err)
	}
	// type=profile delegates to anthropic-sdk-go's profile loader, so
	// the abstraction only makes sense when the provider is anthropic.
	// Catch the obvious user error (`provider.name = "openai" + auth.type
	// = "profile"`) at config load time instead of at the first hook fire.
	if c.Provider.Auth != nil && c.Provider.Auth.Type == AuthTypeProfile {
		if strings.ToLower(strings.TrimSpace(c.Provider.Name)) != "anthropic" {
			errs = append(errs, fmt.Errorf(`provider.auth.type=%q is only supported when provider.name="anthropic"`, AuthTypeProfile))
		}
	}
	if err := validateReasoningEffort(c.Provider); err != nil {
		errs = append(errs, err)
	}
	if c.LogMaxSize != nil && *c.LogMaxSize < 0 {
		errs = append(errs, fmt.Errorf("log_max_size must not be negative, got %d", *c.LogMaxSize))
	}
	if c.MetricsMaxSize != nil && *c.MetricsMaxSize < 0 {
		errs = append(errs, fmt.Errorf("metrics_max_size must not be negative, got %d", *c.MetricsMaxSize))
	}
	if c.FallthroughStrategy != nil {
		switch *c.FallthroughStrategy {
		case FallthroughStrategyAsk, FallthroughStrategyAllow, FallthroughStrategyDeny:
		default:
			errs = append(errs, fmt.Errorf("fallthrough_strategy must be one of %q, %q, %q, got %q",
				FallthroughStrategyAsk, FallthroughStrategyAllow, FallthroughStrategyDeny, *c.FallthroughStrategy))
		}
	}
	return errors.Join(errs...)
}

// validateReasoningEffort rejects the one value that provably cannot
// work, rather than policing the whole vocabulary.
//
// Only the anthropic branch is checked. Anthropic has no "minimal"
// level anywhere — neither `thinking` nor `output_config.effort` can
// express it — so a config asking for it is broken no matter which
// model it names, and catching that at load time beats a 400 on the
// first hook fire.
//
// openai / gemini are deliberately not validated: which efforts a
// model accepts is model-specific (a non-reasoning model may reject
// every value, a new model may gain one), so a ccgate-side allowlist
// would either lag the provider or reject configs that work. Those
// values go out as-is and the provider decides.
func validateReasoningEffort(p ProviderConfig) error {
	if p.ReasoningEffort == nil {
		return nil
	}
	if strings.ToLower(strings.TrimSpace(p.Name)) != "anthropic" {
		return nil
	}
	switch *p.ReasoningEffort {
	case ReasoningEffortMinimal:
		return fmt.Errorf(`provider.reasoning_effort=%q has no anthropic equivalent; use %q to disable thinking or one of %q, %q, %q, %q, %q`,
			ReasoningEffortMinimal, ReasoningEffortNone,
			ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh, ReasoningEffortXHigh, ReasoningEffortMax)
	}
	return nil
}

// validateAuth enforces the discriminated-union shape of provider.auth.
//
// Rules per type:
//
//   - type=exec: command required (non-empty after trim);
//     refresh_margin_ms / timeout_ms / cache_key / shell are
//     optional. path is forbidden.
//   - type=file: path optional (runner falls back to
//     config.DefaultAuthPath for the target). When set it must be
//     absolute, `~/`-prefixed, or relative; bare `~` and `~/` are
//     rejected (they expand to the home dir itself, not a file).
//     refresh_margin_ms / timeout_ms are allowed (timeout bounds
//     the file read for stalled mounts). command / cache_key /
//     shell are forbidden.
//   - type unknown / empty: rejected.
//
// Auth omitted entirely (nil) means env-var fallback, which Validate
// always accepts here — the resolution path is exercised in runner.
func validateAuth(a *AuthConfig) error {
	if a == nil {
		return nil
	}
	switch a.Type {
	case AuthTypeExec:
		return validateAuthExec(a)
	case AuthTypeFile:
		return validateAuthFile(a)
	case AuthTypeProfile:
		return validateAuthProfile(a)
	case "":
		return fmt.Errorf("provider.auth.type must be set to %q, %q, or %q", AuthTypeExec, AuthTypeFile, AuthTypeProfile)
	default:
		return fmt.Errorf("provider.auth.type %q is not supported (allowed: %q, %q, %q)",
			a.Type, AuthTypeExec, AuthTypeFile, AuthTypeProfile)
	}
}

func validateAuthExec(a *AuthConfig) error {
	var errs []error
	if strings.TrimSpace(a.Command) == "" {
		errs = append(errs, fmt.Errorf("provider.auth.command must not be empty when type=%q", AuthTypeExec))
	}
	if a.Path != nil {
		errs = append(errs, fmt.Errorf("provider.auth.path is only allowed when type=%q", AuthTypeFile))
	}
	if a.Profile != "" {
		errs = append(errs, fmt.Errorf("provider.auth.profile is only allowed when type=%q", AuthTypeProfile))
	}
	switch a.Shell {
	case "", AuthShellBash, AuthShellPowerShell:
	default:
		errs = append(errs, fmt.Errorf("provider.auth.shell must be %q or %q, got %q",
			AuthShellBash, AuthShellPowerShell, a.Shell))
	}
	if a.RefreshMarginMS != nil && *a.RefreshMarginMS < 0 {
		errs = append(errs, fmt.Errorf("provider.auth.refresh_margin_ms must not be negative, got %d", *a.RefreshMarginMS))
	}
	if a.TimeoutMS != nil && *a.TimeoutMS <= 0 {
		errs = append(errs, fmt.Errorf("provider.auth.timeout_ms must be positive, got %d", *a.TimeoutMS))
	}
	// cache_key: any string accepted; the value is used as-is.
	return errors.Join(errs...)
}

func validateAuthFile(a *AuthConfig) error {
	var errs []error
	// Path is optional: nil = "omit, use the default per target".
	// An explicit empty string is rejected so a config that
	// produced "" via std.native('env') etc. surfaces as a config
	// error instead of silently sharing the default with omitted
	// configs.
	if a.Path != nil {
		if *a.Path == "" {
			errs = append(errs, fmt.Errorf("provider.auth.path must not be an empty string; omit the field to use the default"))
		} else if err := validateAuthPath(*a.Path); err != nil {
			errs = append(errs, err)
		}
	}
	if a.Command != "" {
		errs = append(errs, fmt.Errorf("provider.auth.command is only allowed when type=%q", AuthTypeExec))
	}
	if a.Shell != "" {
		errs = append(errs, fmt.Errorf("provider.auth.shell is only allowed when type=%q", AuthTypeExec))
	}
	if a.Profile != "" {
		errs = append(errs, fmt.Errorf("provider.auth.profile is only allowed when type=%q", AuthTypeProfile))
	}
	if a.TimeoutMS != nil && *a.TimeoutMS <= 0 {
		errs = append(errs, fmt.Errorf("provider.auth.timeout_ms must be positive, got %d", *a.TimeoutMS))
	}
	if a.CacheKey != "" {
		errs = append(errs, fmt.Errorf("provider.auth.cache_key is only allowed when type=%q", AuthTypeExec))
	}
	if a.RefreshMarginMS != nil && *a.RefreshMarginMS < 0 {
		errs = append(errs, fmt.Errorf("provider.auth.refresh_margin_ms must not be negative, got %d", *a.RefreshMarginMS))
	}
	return errors.Join(errs...)
}

// validateAuthPath rejects only the obviously broken cases (empty
// string, bare home-directory). Absolute paths, `~/`-prefixed
// paths, and relative paths are all accepted; relative paths
// resolve from the hook's working directory (the project root for
// Claude Code / Codex CLI), matching how those tools resolve
// `command` paths in their own hook configs.
func validateAuthPath(path string) error {
	v := strings.TrimSpace(path)
	if v == "~" || v == "~/" {
		return fmt.Errorf("provider.auth.path must point at a file, got bare %q", v)
	}
	return nil
}

// validateAuthProfile enforces the rules for type=profile (Anthropic
// only). The SDK's profile loader runs at hook fire time and
// surfaces real semantic failures (missing config, invalid name,
// missing credentials) as ErrProfileUnavailable -> fallthrough, so
// ccgate-side validation only catches the obvious config-shape
// mistakes:
//
//   - profile optional; whitespace-only is rejected (almost certainly
//     a templating mistake). Character-set rules are delegated to
//     anthropicconfig.LoadProfile's own validateProfileName so the
//     two implementations cannot drift.
//   - command / path / shell / refresh_margin_ms / timeout_ms /
//     cache_key are all forbidden — the profile path delegates the
//     credential lifecycle to the SDK, so none of them have anything
//     to bound or salt on the ccgate side.
func validateAuthProfile(a *AuthConfig) error {
	var errs []error
	if a.Profile != "" && strings.TrimSpace(a.Profile) == "" {
		errs = append(errs, fmt.Errorf("provider.auth.profile must not be whitespace only"))
	}
	if a.Command != "" {
		errs = append(errs, fmt.Errorf("provider.auth.command is only allowed when type=%q", AuthTypeExec))
	}
	if a.Path != nil {
		errs = append(errs, fmt.Errorf("provider.auth.path is only allowed when type=%q", AuthTypeFile))
	}
	if a.Shell != "" {
		errs = append(errs, fmt.Errorf("provider.auth.shell is only allowed when type=%q", AuthTypeExec))
	}
	if a.RefreshMarginMS != nil {
		errs = append(errs, fmt.Errorf("provider.auth.refresh_margin_ms is not supported when type=%q (SDK manages refresh internally)", AuthTypeProfile))
	}
	if a.TimeoutMS != nil {
		errs = append(errs, fmt.Errorf("provider.auth.timeout_ms is not supported when type=%q (SDK owns the credential lifecycle)", AuthTypeProfile))
	}
	if a.CacheKey != "" {
		errs = append(errs, fmt.Errorf("provider.auth.cache_key is only allowed when type=%q", AuthTypeExec))
	}
	return errors.Join(errs...)
}
