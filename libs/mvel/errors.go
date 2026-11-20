package mvel

import (
	"errors"
	"fmt"
)

var (
	// ErrSyntax identifies malformed expressions or decimal text.
	ErrSyntax = errors.New("mvel syntax error")
	// ErrUnsupported identifies syntax or identifiers outside the accepted grammar.
	ErrUnsupported = errors.New("mvel unsupported expression")
	// ErrLimit identifies an expression, binding, or result outside a fixed evaluator budget.
	ErrLimit = errors.New("mvel limit exceeded")
	// ErrBinding identifies a missing, extra, or wrongly typed binding.
	ErrBinding = errors.New("mvel invalid binding")
	// ErrResultKind identifies an expression compiled for the wrong result kind.
	ErrResultKind = errors.New("mvel result kind mismatch")
)

type evaluatorError struct {
	category error
	position int
	name     string
	reason   string
}

func (err *evaluatorError) Error() string {
	switch {
	case err.name != "":
		return fmt.Sprintf("%s %s: %s", err.category, err.name, err.reason)
	case err.position >= 0:
		return fmt.Sprintf("%s at byte %d: %s", err.category, err.position, err.reason)
	default:
		return fmt.Sprintf("%s: %s", err.category, err.reason)
	}
}

func (err *evaluatorError) Unwrap() error {
	return err.category
}

func sourceError(category error, position int, reason string) error {
	return &evaluatorError{category: category, position: position, reason: reason}
}

func bindingError(name, reason string) error {
	return &evaluatorError{category: ErrBinding, position: -1, name: name, reason: reason}
}

func decimalError(category error, reason string) error {
	return &evaluatorError{category: category, position: -1, reason: reason}
}
