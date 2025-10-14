package email

import (
	"context"
	"time"
)

// Payload is the canonical representation of an outbound email passed to the
// provider. Adapters are expected to normalize their inputs to this structure.
type Payload struct {
	MessageID string
	From      string
	To        []string
	CC        []string
	BCC       []string
	Subject   string
	BodyType  string
	Body      string
	Headers   map[string]string
}

// RawResponse mirrors the low level provider response that adapters inspect to
// derive higher level ProviderResponse values.
type RawResponse struct {
	ID        string
	Code      int
	Body      string
	Timestamp time.Time
}

// Provider is the contract exposed by the email provider implementation.
type Provider interface {
	Send(ctx context.Context, payload *Payload) (*RawResponse, error)
}
