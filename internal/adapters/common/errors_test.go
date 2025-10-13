package common

import (
	"errors"
	"strings"
	"testing"
)

func TestWrapTransient(t *testing.T) {
	base := errors.New("temporary failure")
	wrapped := WrapTransient(base)

	if !errors.Is(wrapped, ErrTransient) {
		t.Fatalf("expected wrapped error to be transient: %v", wrapped)
	}

	if !strings.Contains(wrapped.Error(), base.Error()) {
		t.Fatalf("expected wrapped error message to include original message")
	}
}

func TestWrapPermanent(t *testing.T) {
	base := errors.New("invalid recipient")
	wrapped := WrapPermanent(base)

	if !errors.Is(wrapped, ErrPermanent) {
		t.Fatalf("expected wrapped error to be permanent: %v", wrapped)
	}

	if !strings.Contains(wrapped.Error(), base.Error()) {
		t.Fatalf("expected wrapped error message to include original message")
	}
}

func TestWrapNil(t *testing.T) {
	if !errors.Is(WrapTransient(nil), ErrTransient) {
		t.Fatalf("expected nil transient wrap to fall back to ErrTransient")
	}
	if !errors.Is(WrapPermanent(nil), ErrPermanent) {
		t.Fatalf("expected nil permanent wrap to fall back to ErrPermanent")
	}
}
