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
	if c.Provider.TimeoutMS <= 0 {
		errs = append(errs, fmt.Errorf("provider.timeout_ms must be positive, got %d", c.Provider.TimeoutMS))
	}
	return errors.Join(errs...)
}
