package common

import "errors"

// ErrTransient and ErrPermanent are sentinel errors adapters will use when
// classifying provider failures.
var (
ErrTransient = errors.New("transient error")
ErrPermanent = errors.New("permanent error")
)
