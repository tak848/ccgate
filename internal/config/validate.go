package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
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
	if err := validateAPIKeyFile(c.Provider.APIKeyFile); err != nil {
		errs = append(errs, err)
	}
	// Refresh margin: must parse, non-negative. "0s" is a meaningful
	// "no early refresh" setting so we accept it; negative values
	// would silently disable the cache, so reject them up front.
	if v := strings.TrimSpace(c.Provider.APIKeyRefreshMargin); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("provider.api_key_refresh_margin %q: %w", v, err))
		case d < 0:
			errs = append(errs, fmt.Errorf("provider.api_key_refresh_margin must not be negative, got %s", d))
		}
	}
	// Command timeout: must parse and be strictly positive. "0s"
	// would cause every helper exec to time out instantly and wedge
	// the hot path, so reject it at config time.
	if v := strings.TrimSpace(c.Provider.APIKeyCommandTimeout); v != "" {
		d, err := time.ParseDuration(v)
		switch {
		case err != nil:
			errs = append(errs, fmt.Errorf("provider.api_key_command_timeout %q: %w", v, err))
		case d <= 0:
			errs = append(errs, fmt.Errorf("provider.api_key_command_timeout must be positive, got %s", d))
		}
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

// validateAPIKeyFile rejects relative paths. The config loader does
// not pass the config file's location into ProviderConfig, so a
// relative path here would have no well-defined base — surface that
// at validate time instead of guessing later.
func validateAPIKeyFile(path string) error {
	v := strings.TrimSpace(path)
	if v == "" {
		return nil
	}
	if strings.HasPrefix(v, "/") || strings.HasPrefix(v, "~/") || v == "~" {
		return nil
	}
	return fmt.Errorf("provider.api_key_file %q must be an absolute path or start with ~/", v)
}
