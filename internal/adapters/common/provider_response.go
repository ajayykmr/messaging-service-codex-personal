package common

import "unicode/utf8"

// DefaultRawBodyLimit defines the maximum number of characters retained from a
// provider response body when attaching it to a ProviderResponse.
const DefaultRawBodyLimit = 1024

// ProviderResponse captures normalized provider information exchanged between
// adapters and the worker engine.
type ProviderResponse struct {
	Status  string            `json:"status"`
	Code    *int              `json:"code,omitempty"`
	Message string            `json:"message,omitempty"`
	Raw     string            `json:"raw,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// TruncateRaw trims the supplied string to the specified rune limit. If limit
// is zero or negative it returns an empty string.
func TruncateRaw(raw string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(raw) <= limit {
		return raw
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw
	}
	return string(runes[:limit])
}
