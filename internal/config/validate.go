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
	case "":
		return fmt.Errorf("provider.auth.type must be set to %q or %q", AuthTypeExec, AuthTypeFile)
	default:
		return fmt.Errorf("provider.auth.type %q is not supported (allowed: %q, %q)",
			a.Type, AuthTypeExec, AuthTypeFile)
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
