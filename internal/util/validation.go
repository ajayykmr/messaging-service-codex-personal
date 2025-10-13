package util

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

var (
	// ErrInvalidUUID is returned when a value is not a UUID v4.
	ErrInvalidUUID = errors.New("invalid uuid v4")
	// ErrInvalidTimestamp indicates the value could not be parsed as RFC3339.
	ErrInvalidTimestamp = errors.New("invalid rfc3339 timestamp")
	// ErrInvalidEmail is returned when an email address cannot be parsed.
	ErrInvalidEmail = errors.New("invalid email address")
	// ErrInvalidPhone is returned when a phone number is not E.164 compliant.
	ErrInvalidPhone = errors.New("invalid e164 phone number")
	// ErrInvalidURL indicates that a URL failed validation.
	ErrInvalidURL = errors.New("invalid url")
	// ErrInvalidTemplateID indicates a template identifier is malformed.
	ErrInvalidTemplateID = errors.New("invalid template id")
)

var (
	e164Pattern       = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)
	templateIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)
)

// ParseUUIDv4 parses and validates a UUID string, ensuring it is version 4.
func ParseUUIDv4(value string) (uuid.UUID, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return uuid.UUID{}, fmt.Errorf("%w: value is empty", ErrInvalidUUID)
	}

	u, err := uuid.Parse(trimmed)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("%w: %v", ErrInvalidUUID, err)
	}

	if u.Version() != 4 {
		return uuid.UUID{}, fmt.Errorf("%w: expected version 4", ErrInvalidUUID)
	}

	return u, nil
}

// ParseRFC3339 parses a timestamp string using RFC3339Nano for maximum fidelity.
func ParseRFC3339(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("%w: value is empty", ErrInvalidTimestamp)
	}

	ts, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrInvalidTimestamp, err)
	}

	return ts, nil
}

// NormalizeEmail validates and normalizes an email address. The returned value
// is lowercased and stripped of surrounding whitespace.
func NormalizeEmail(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%w: value is empty", ErrInvalidEmail)
	}

	addr, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidEmail, err)
	}

	// Disallow display names to keep payloads deterministic.
	if addr.Name != "" || addr.Address == "" {
		return "", fmt.Errorf("%w: must not include display name", ErrInvalidEmail)
	}

	if addr.Address != trimmed {
		return "", fmt.Errorf("%w: unexpected formatting", ErrInvalidEmail)
	}

	return strings.ToLower(addr.Address), nil
}

// NormalizeEmails validates each email and returns the normalized slice.
func NormalizeEmails(values []string, min, max int) ([]string, error) {
	count := len(values)
	if min > 0 && count < min {
		return nil, fmt.Errorf("expected at least %d email(s); got %d", min, count)
	}
	if max > 0 && count > max {
		return nil, fmt.Errorf("expected at most %d email(s); got %d", max, count)
	}

	if count == 0 {
		return nil, nil
	}

	result := make([]string, 0, count)
	for idx, value := range values {
		normalized, err := NormalizeEmail(value)
		if err != nil {
			return nil, fmt.Errorf("email[%d]: %w", idx, err)
		}
		result = append(result, normalized)
	}

	return result, nil
}

// NormalizeE164 validates a phone number using the E.164 format and returns the
// normalized representation.
func NormalizeE164(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%w: value is empty", ErrInvalidPhone)
	}

	if !e164Pattern.MatchString(trimmed) {
		return "", fmt.Errorf("%w: %q", ErrInvalidPhone, trimmed)
	}

	return trimmed, nil
}

// NormalizeE164List validates each phone number in the slice.
func NormalizeE164List(values []string, min, max int) ([]string, error) {
	count := len(values)
	if min > 0 && count < min {
		return nil, fmt.Errorf("expected at least %d phone number(s); got %d", min, count)
	}
	if max > 0 && count > max {
		return nil, fmt.Errorf("expected at most %d phone number(s); got %d", max, count)
	}

	if count == 0 {
		return nil, nil
	}

	result := make([]string, 0, count)
	for idx, value := range values {
		normalized, err := NormalizeE164(value)
		if err != nil {
			return nil, fmt.Errorf("phone[%d]: %w", idx, err)
		}
		result = append(result, normalized)
	}
	return result, nil
}

// ValidateMetadata enforces constraints on metadata maps and returns a copy
// containing trimmed keys and values.
func ValidateMetadata(meta map[string]string, maxEntries, maxKeyLen, maxValueLen int) (map[string]string, error) {
	if len(meta) == 0 {
		return nil, nil
	}

	if maxEntries > 0 && len(meta) > maxEntries {
		return nil, fmt.Errorf("metadata entries exceeded: got %d, max %d", len(meta), maxEntries)
	}

	out := make(map[string]string, len(meta))
	for rawKey, rawValue := range meta {
		key := strings.TrimSpace(rawKey)
		value := strings.TrimSpace(rawValue)

		if key == "" {
			return nil, errors.New("metadata key cannot be empty")
		}

		if maxKeyLen > 0 && utf8.RuneCountInString(key) > maxKeyLen {
			return nil, fmt.Errorf("metadata key %q exceeds max length %d", key, maxKeyLen)
		}

		if maxValueLen > 0 && utf8.RuneCountInString(value) > maxValueLen {
			return nil, fmt.Errorf("metadata value for %q exceeds max length %d", key, maxValueLen)
		}

		out[key] = value
	}

	return out, nil
}

// EnsureMaxBytes checks that a byte slice does not exceed the specified size.
func EnsureMaxBytes(field string, b []byte, max int) error {
	if max <= 0 {
		return nil
	}
	if len(b) > max {
		return fmt.Errorf("%s exceeds maximum size of %d bytes", field, max)
	}
	return nil
}

// EnsureMaxRunes ensures a string is not longer than the provided rune count.
func EnsureMaxRunes(field, value string, max int) error {
	if max <= 0 {
		return nil
	}
	length := utf8.RuneCountInString(value)
	if length > max {
		return fmt.Errorf("%s exceeds maximum length of %d characters", field, max)
	}
	return nil
}

// EnsureMinRunes ensures a string meets a minimum rune length requirement.
func EnsureMinRunes(field, value string, min int) error {
	if min <= 0 {
		return nil
	}

	length := utf8.RuneCountInString(value)
	if length < min {
		return fmt.Errorf("%s must be at least %d characters", field, min)
	}
	return nil
}

// ValidateHTTPURL ensures the provided string is a valid HTTP or HTTPS URL.
func ValidateHTTPURL(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%w: value is empty", ErrInvalidURL)
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: unsupported scheme %q", ErrInvalidURL, u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: host is required", ErrInvalidURL)
	}

	return trimmed, nil
}

// ValidateTemplateID enforces a conservative pattern recommended for template
// identifiers.
func ValidateTemplateID(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%w: value is empty", ErrInvalidTemplateID)
	}
	if !templateIDPattern.MatchString(trimmed) {
		return "", fmt.Errorf("%w: %q", ErrInvalidTemplateID, trimmed)
	}
	return trimmed, nil
}
