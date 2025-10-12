package models

import "time"

// Common channels supported by the messaging service.
const (
	ChannelEmail    = "email"
	ChannelSMS      = "sms"
	ChannelWhatsApp = "whatsapp"
)

// BodyType enumerates valid message body encodings.
const (
	BodyTypeHTML     = "html"
	BodyTypeText     = "text"
	BodyTypeTemplate = "template"
	BodyTypeMedia    = "media"
)

// Envelope provides the shared metadata across request payloads.
type Envelope struct {
	MessageID string            `json:"message_id"`
	Channel   string            `json:"channel"`
	TenantID  string            `json:"tenant_id,omitempty"`
	TraceID   string            `json:"trace_id,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// MessageBody defines the payload content for outbound messages.
type MessageBody struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	MediaType string `json:"media_type,omitempty"`
}

// EmailRequest is the canonical representation of an email send request.
type EmailRequest struct {
	Envelope
	From    string      `json:"from"`
	To      []string    `json:"to"`
	CC      []string    `json:"cc,omitempty"`
	BCC     []string    `json:"bcc,omitempty"`
	Subject string      `json:"subject"`
	Body    MessageBody `json:"body"`
}

// SMSRequest captures SMS request metadata and body.
type SMSRequest struct {
	Envelope
	From string      `json:"from"`
	To   []string    `json:"to"`
	Body MessageBody `json:"body"`
}

// WhatsAppRequest represents a WhatsApp delivery request.
type WhatsAppRequest struct {
	Envelope
	From string      `json:"from"`
	To   []string    `json:"to"`
	Body MessageBody `json:"body"`
}
