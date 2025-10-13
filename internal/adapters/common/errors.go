package common

import (
	"errors"
	"fmt"
)

// ErrTransient and ErrPermanent are sentinel errors adapters will use when
// classifying provider failures.
var (
	ErrTransient = errors.New("transient error")
	ErrPermanent = errors.New("permanent error")
)

// WrapTransient annotates an error so callers can detect transient failures.
func WrapTransient(err error) error {
	if err == nil {
		return ErrTransient
	}
	return fmt.Errorf("%w: %v", ErrTransient, err)
}

// WrapPermanent annotates an error as permanent.
func WrapPermanent(err error) error {
	if err == nil {
		return ErrPermanent
	}
	return fmt.Errorf("%w: %v", ErrPermanent, err)
}
