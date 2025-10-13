package common_test

import (
	"testing"

	common "github.com/example/messaging-microservice/internal/adapters/common"
)

func TestTruncateRaw(t *testing.T) {
	raw := "こんにちは世界" // 7 runes

	if got := common.TruncateRaw(raw, 10); got != raw {
		t.Fatalf("expected raw string unchanged when under limit, got %q", got)
	}

	if got := common.TruncateRaw(raw, 3); got != "こんに" {
		t.Fatalf("expected rune-safe truncation, got %q", got)
	}

	if got := common.TruncateRaw(raw, 0); got != "" {
		t.Fatalf("expected empty string for non-positive limit, got %q", got)
	}
}
