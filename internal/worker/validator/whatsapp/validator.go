package whatsappvalidator

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "reflect"
    "strings"

    "github.com/rs/zerolog"

    "github.com/example/messaging-microservice/internal/config"
    "github.com/example/messaging-microservice/internal/models"
    "github.com/example/messaging-microservice/internal/util"
    "github.com/example/messaging-microservice/internal/worker"
)

// Validator implements worker.Validator for WhatsApp payloads.
type Validator struct {
	logger zerolog.Logger
	cfg    config.ValidationConfig
}

// New constructs a Validator.
func New(cfg config.ValidationConfig, logger zerolog.Logger) *Validator {
	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}
	return &Validator{logger: logger, cfg: cfg}
}

// ParseAndValidate parses the payload and returns a validated message.
func (v *Validator) ParseAndValidate(ctx context.Context, channel string, payload []byte) (*worker.ValidatedMessage, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(payload) == 0 {
		return nil, errors.New("whatsapp validator: payload is empty")
	}

	var req models.WhatsAppRequest
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return nil, fmt.Errorf("whatsapp validator: decode: %w", err)
	}

	if err := v.applyDefaultsAndValidate(channel, &req); err != nil {
		return nil, err
	}

	validated := &worker.ValidatedMessage{
		Channel:   req.Channel,
		MessageID: req.MessageID,
		TraceID:   req.TraceID,
		TenantID:  req.TenantID,
		CreatedAt: req.CreatedAt,
		Metadata:  toAnyMap(req.Meta),
		Request:   &req,
		RawPayload: func() []byte {
			buf := make([]byte, len(payload))
			copy(buf, payload)
			return buf
		}(),
	}

	return validated, nil
}

func (v *Validator) applyDefaultsAndValidate(channel string, req *models.WhatsAppRequest) error {
	req.Channel = strings.TrimSpace(strings.ToLower(req.Channel))
	if req.Channel == "" {
		req.Channel = channel
	}
	if channel != "" && req.Channel != strings.ToLower(channel) {
		return fmt.Errorf("whatsapp validator: channel mismatch: expected %s, got %s", channel, req.Channel)
	}

	if _, err := util.ParseUUIDv4(req.MessageID); err != nil {
		return fmt.Errorf("whatsapp validator: message_id: %w", err)
	}
	req.MessageID = strings.TrimSpace(req.MessageID)
	req.TraceID = strings.TrimSpace(req.TraceID)
	req.TenantID = strings.TrimSpace(req.TenantID)

	if req.CreatedAt.IsZero() {
		return errors.New("whatsapp validator: created_at is required")
	}
	req.CreatedAt = req.CreatedAt.UTC()

	from, err := util.NormalizeE164(req.From)
	if err != nil {
		return fmt.Errorf("whatsapp validator: from: %w", err)
	}
	req.From = from

    toNormalized, err := util.NormalizeE164List(req.To, 1, v.cfg.RecipientsMax)
	if err != nil {
		return fmt.Errorf("whatsapp validator: to: %w", err)
	}
	req.To = toNormalized

	req.Body.Type = strings.ToLower(strings.TrimSpace(req.Body.Type))
	if req.Body.Type == "" {
		req.Body.Type = models.BodyTypeText
	}
	switch req.Body.Type {
	case models.BodyTypeText, models.BodyTypeTemplate, models.BodyTypeMedia:
	default:
		return fmt.Errorf("whatsapp validator: unsupported body type %q", req.Body.Type)
	}

    if v.cfg.WABodyMax > 0 && len(req.Body.Content) > v.cfg.WABodyMax {
        return fmt.Errorf("whatsapp validator: body exceeds max bytes %d", v.cfg.WABodyMax)
    }

	meta, err := util.ValidateMetadata(req.Meta, v.cfg.MetaMaxEntries, v.cfg.MetaMaxKeyLen, v.cfg.MetaMaxValueLen)
	if err != nil {
		return fmt.Errorf("whatsapp validator: metadata: %w", err)
	}
	req.Meta = meta

	return nil
}

func toAnyMap(meta map[string]string) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	return out
}
