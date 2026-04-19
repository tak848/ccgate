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
