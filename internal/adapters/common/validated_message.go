package common

import "time"

// ValidatedMessage captures the canonical representation of a request after it
// has passed validation. Adapters receive this structure when sending to an
// upstream provider, and the worker engine uses it to enrich status/DLQ events.
type ValidatedMessage struct {
	Channel      string
	MessageID    string
	TraceID      string
	TenantID     string
	CreatedAt    time.Time
	Metadata     map[string]any
	Request      any
	RawPayload   []byte
	Key          []byte
	KafkaHeaders map[string][]byte
}
