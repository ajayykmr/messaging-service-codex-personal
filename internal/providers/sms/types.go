package sms

import (
	"context"
	"time"
)

// Payload encapsulates the data required to send an SMS message via a provider.
type Payload struct {
	MessageID string
	From      string
	To        []string
	Body      string
	Meta      map[string]string
}

// RawResponse describes the low-level provider response returned after an SMS
// has been processed.
type RawResponse struct {
	ID        string
	Code      int
	Status    string
	Body      string
	Timestamp time.Time
}

// Provider represents an outbound SMS provider (e.g. Twilio).
type Provider interface {
	Send(ctx context.Context, payload *Payload) (*RawResponse, error)
}
