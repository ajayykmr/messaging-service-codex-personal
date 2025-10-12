package models

import "time"

// Failure types for DLQ records.
const (
	FailureTypePermanent  = "permanent"
	FailureTypeTransient  = "transient"
	FailureTypeValidation = "validation"
	FailureTypeUnknown    = "unknown"
)

// DLQRecord mirrors the failure payload described in the design document.
type DLQRecord struct {
	MessageID       string            `json:"message_id"`
	Channel         string            `json:"channel"`
	OriginalMessage any               `json:"original_message"`
	Attempts        int               `json:"attempts"`
	FailureType     string            `json:"failure_type"`
	LastError       string            `json:"last_error,omitempty"`
	FirstFailedAt   time.Time         `json:"first_failed_at"`
	LastAttemptAt   time.Time         `json:"last_attempt_at"`
	TraceID         string            `json:"trace_id,omitempty"`
	Meta            map[string]string `json:"meta,omitempty"`
}
