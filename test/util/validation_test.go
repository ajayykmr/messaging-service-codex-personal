package util_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ajayykmr/messaging-service-go/internal/util"
)

func TestParseUUIDv4(t *testing.T) {
	_, err := util.ParseUUIDv4("b0c9c2b0-1f3a-4d2d-9e3f-123456789abc")
	if err != nil {
		t.Fatalf("expected success parsing valid uuid: %v", err)
	}

	if _, err := util.ParseUUIDv4(""); !errors.Is(err, util.ErrInvalidUUID) {
		t.Fatalf("expected ErrInvalidUUID for empty string, got %v", err)
	}

	if _, err := util.ParseUUIDv4("6fa459ea-ee8a-11d2-90f6-000000000000"); !errors.Is(err, util.ErrInvalidUUID) {
		t.Fatalf("expected ErrInvalidUUID for non v4 uuid, got %v", err)
	}
}

func TestParseRFC3339(t *testing.T) {
	ts, err := util.ParseRFC3339("2025-10-11T10:00:00Z")
	if err != nil {
		t.Fatalf("expected success parsing timestamp: %v", err)
	}

	if got := ts.Format(time.RFC3339); got != "2025-10-11T10:00:00Z" {
		t.Fatalf("unexpected timestamp round trip: %s", got)
	}

	if _, err := util.ParseRFC3339("not-a-time"); !errors.Is(err, util.ErrInvalidTimestamp) {
		t.Fatalf("expected ErrInvalidTimestamp, got %v", err)
	}
}

func TestNormalizeEmail(t *testing.T) {
	addr, err := util.NormalizeEmail("User@example.com")
	if err != nil {
		t.Fatalf("expected valid email: %v", err)
	}
	if addr != "user@example.com" {
		t.Fatalf("expected lowercased email, got %q", addr)
	}

	_, err = util.NormalizeEmail("User <user@example.com>")
	if !errors.Is(err, util.ErrInvalidEmail) {
		t.Fatalf("expected ErrInvalidEmail for display name, got %v", err)
	}
}

func TestNormalizeEmails(t *testing.T) {
	emails, err := util.NormalizeEmails([]string{"user@example.com", "Other@Example.com"}, 1, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(emails) != 2 {
		t.Fatalf("expected 2 emails, got %d", len(emails))
	}
	if emails[1] != "other@example.com" {
		t.Fatalf("expected normalized email, got %q", emails[1])
	}

	if _, err := util.NormalizeEmails([]string{}, 1, 2); err == nil {
		t.Fatalf("expected error when below minimum length")
	}
}

func TestNormalizeE164(t *testing.T) {
	num, err := util.NormalizeE164("+14155552671")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if num != "+14155552671" {
		t.Fatalf("unexpected normalization result: %q", num)
	}

	if _, err := util.NormalizeE164("4155552671"); !errors.Is(err, util.ErrInvalidPhone) {
		t.Fatalf("expected ErrInvalidPhone, got %v", err)
	}
}

func TestNormalizeE164List(t *testing.T) {
	phones, err := util.NormalizeE164List([]string{"+14155552671", "+441234567890"}, 1, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(phones) != 2 {
		t.Fatalf("expected 2 phone numbers, got %d", len(phones))
	}

	if _, err := util.NormalizeE164List([]string{}, 1, 2); err == nil {
		t.Fatalf("expected error when below min entries")
	}
}

func TestValidateMetadata(t *testing.T) {
	meta, err := util.ValidateMetadata(map[string]string{
		" Trace ":  " value ",
		"tenantID": "abc",
	}, 5, 10, 10)
	if err != nil {
		t.Fatalf("unexpected error validating metadata: %v", err)
	}
	if meta["Trace"] != "value" {
		t.Fatalf("expected trimmed metadata, got %q", meta["Trace"])
	}

	_, err = util.ValidateMetadata(map[string]string{"": "invalid"}, 5, 10, 10)
	if err == nil || !strings.Contains(err.Error(), "cannot be empty") {
		t.Fatalf("expected metadata key empty error, got %v", err)
	}

	_, err = util.ValidateMetadata(map[string]string{"toolong": "value"}, 5, 3, 10)
	if err == nil {
		t.Fatalf("expected error for exceeding key length")
	}
}

func TestEnsureMaxBytes(t *testing.T) {
	if err := util.EnsureMaxBytes("body", []byte("12345"), 10); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if err := util.EnsureMaxBytes("body", []byte("1234567890"), 5); err == nil {
		t.Fatalf("expected error when bytes exceed max")
	}
}

func TestEnsureMaxRunesAndEnsureMinRunes(t *testing.T) {
	if err := util.EnsureMaxRunes("subject", "hello", 10); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if err := util.EnsureMaxRunes("subject", "hello world", 5); err == nil {
		t.Fatalf("expected error for exceeding rune length")
	}

	if err := util.EnsureMinRunes("subject", "hello", 3); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if err := util.EnsureMinRunes("subject", "hi", 3); err == nil {
		t.Fatalf("expected min rune error")
	}
}

func TestValidateHTTPURL(t *testing.T) {
	url, err := util.ValidateHTTPURL("https://example.com/path")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != "https://example.com/path" {
		t.Fatalf("unexpected normalized url %q", url)
	}

	if _, err := util.ValidateHTTPURL("ftp://example.com"); !errors.Is(err, util.ErrInvalidURL) {
		t.Fatalf("expected ErrInvalidURL for unsupported scheme, got %v", err)
	}
}
