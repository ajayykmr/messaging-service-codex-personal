package models

import "time"

// Status event constants.
const (
	StatusEventQueued      = "queued"
	StatusEventAttempt     = "attempt"
	StatusEventSent        = "sent"
	StatusEventRejected    = "rejected"
	StatusEventRateLimited = "rate_limited"
	StatusEventFailed      = "failed"
	StatusEventDLQ         = "dlq"
)

// ProviderResponse captures normalized adapter responses.
type ProviderResponse struct {
	Status  string            `json:"status"`
	Code    *int              `json:"code,omitempty"`
	Message string            `json:"message,omitempty"`
	Raw     string            `json:"raw,omitempty"`
	Meta    map[string]string `json:"meta,omitempty"`
}

// StatusEvent represents lifecycle events emitted for outbound messages.
type StatusEvent struct {
	MessageID        string            `json:"message_id"`
	Channel          string            `json:"channel"`
	EventType        string            `json:"event_type"`
	Attempt          int               `json:"attempt,omitempty"`
	ProviderResponse *ProviderResponse `json:"provider_response,omitempty"`
	Error            string            `json:"error,omitempty"`
	TraceID          string            `json:"trace_id,omitempty"`
	Timestamp        time.Time         `json:"timestamp"`
}
