package models

import "time"

// BaseRequest captures attributes that are shared across all message
// requests regardless of the channel.
type BaseRequest struct {
	MessageID string            `json:"message_id"`
	Channel   string            `json:"channel"`
	TenantID  string            `json:"tenant_id,omitempty"`
	TraceID   string            `json:"trace_id,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// MessageBody encapsulates content metadata for the different channels.
type MessageBody struct {
	Type      string `json:"type"`
	Content   string `json:"content"`
	MediaType string `json:"media_type,omitempty"`
}

// EmailRequest models the payload expected for email messages.
type EmailRequest struct {
	BaseRequest
	From    string      `json:"from"`
	To      []string    `json:"to"`
	CC      []string    `json:"cc,omitempty"`
	BCC     []string    `json:"bcc,omitempty"`
	Subject string      `json:"subject"`
	Body    MessageBody `json:"body"`
}

// SMSRequest models the payload expected for SMS messages.
type SMSRequest struct {
	BaseRequest
	From string      `json:"from"`
	To   []string    `json:"to"`
	Body MessageBody `json:"body"`
}

// WhatsAppRequest models the payload expected for WhatsApp messages.
type WhatsAppRequest struct {
	BaseRequest
	From string      `json:"from"`
	To   []string    `json:"to"`
	Body MessageBody `json:"body"`
}

// MessageRequest exposes the metadata that is common to all request types.
type MessageRequest interface {
	GetMessageID() string
	GetChannel() string
	GetTraceID() string
	GetCreatedAt() time.Time
	GetMeta() map[string]string
}

// GetMessageID returns the UUID of the message request.
func (b BaseRequest) GetMessageID() string { return b.MessageID }

// GetChannel returns the message channel (email, sms, whatsapp).
func (b BaseRequest) GetChannel() string { return b.Channel }

// GetTraceID returns the trace identifier attached to the request if any.
func (b BaseRequest) GetTraceID() string { return b.TraceID }

// GetCreatedAt returns the timestamp the request was created.
func (b BaseRequest) GetCreatedAt() time.Time { return b.CreatedAt }

// GetMeta returns the arbitrary metadata attached to the message.
func (b BaseRequest) GetMeta() map[string]string { return b.Meta }
