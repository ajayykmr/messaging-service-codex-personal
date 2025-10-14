package whatsapp

import (
	"context"
	"time"
)

// Payload encapsulates the WhatsApp message to be sent via a provider.
type Payload struct {
	MessageID string
	From      string
	To        []string
	BodyType  string
	Body      string
	Meta      map[string]string
}

// RawResponse captures the low-level provider response for a WhatsApp send.
type RawResponse struct {
	ID        string
	Code      int
	Status    string
	Body      string
	Timestamp time.Time
}

// Provider represents an outbound WhatsApp provider (e.g. Twilio or Meta API).
type Provider interface {
	Send(ctx context.Context, payload *Payload) (*RawResponse, error)
}
