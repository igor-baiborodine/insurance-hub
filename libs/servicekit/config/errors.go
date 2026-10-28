package config

import (
	"errors"
	"fmt"
)

// Invalid creates a configuration error from trusted schema names and fixed reasons.
// Callers must never pass environment values, credentials or raw parser errors.
func Invalid(name, reason string) error {
	return &validationError{setting: name, reason: reason}
}

type validationError struct {
	setting string
	reason  string
}

func (err *validationError) Error() string {
	return fmt.Sprintf("invalid %s: %s", err.setting, err.reason)
}

// ErrorDetails extracts safe fields without rendering wrappers or unknown errors.
func ErrorDetails(err error) (setting, reason string) {
	if validation, ok := errors.AsType[*validationError](err); ok {
		return validation.setting, validation.reason
	}
	return "unknown", "could not load configuration"
}
