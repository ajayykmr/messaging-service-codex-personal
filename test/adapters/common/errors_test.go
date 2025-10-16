package common_test

import (
	"errors"
	"strings"
	"testing"

	common "github.com/ajayykmr/messaging-service-go/internal/adapters/common"
)

func TestWrapTransient(t *testing.T) {
	base := errors.New("temporary failure")
	wrapped := common.WrapTransient(base)

	if !errors.Is(wrapped, common.ErrTransient) {
		t.Fatalf("expected wrapped error to be transient: %v", wrapped)
	}

	if !strings.Contains(wrapped.Error(), base.Error()) {
		t.Fatalf("expected wrapped error message to include original message")
	}
}

func TestWrapPermanent(t *testing.T) {
	base := errors.New("invalid recipient")
	wrapped := common.WrapPermanent(base)

	if !errors.Is(wrapped, common.ErrPermanent) {
		t.Fatalf("expected wrapped error to be permanent: %v", wrapped)
	}

	if !strings.Contains(wrapped.Error(), base.Error()) {
		t.Fatalf("expected wrapped error message to include original message")
	}
}

func TestWrapNil(t *testing.T) {
	if !errors.Is(common.WrapTransient(nil), common.ErrTransient) {
		t.Fatalf("expected nil transient wrap to fall back to ErrTransient")
	}
	if !errors.Is(common.WrapPermanent(nil), common.ErrPermanent) {
		t.Fatalf("expected nil permanent wrap to fall back to ErrPermanent")
	}
}
