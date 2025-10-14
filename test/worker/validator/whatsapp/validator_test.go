package whatsappvalidator_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/example/messaging-microservice/internal/config"
	"github.com/example/messaging-microservice/internal/models"
	wav "github.com/example/messaging-microservice/internal/worker/validator/whatsapp"
)

func TestValidatorSuccess(t *testing.T) {
	cfg := config.ValidationConfig{
		RecipientsMax:   10,
		WABodyMax:       4096,
		MetaMaxEntries:  5,
		MetaMaxKeyLen:   16,
		MetaMaxValueLen: 64,
	}
	validator := wav.New(cfg, zerolog.Nop())

	payload := models.WhatsAppRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelWhatsApp,
			CreatedAt: time.Now().UTC(),
			TraceID:   "trace",
			TenantID:  "tenant",
			Meta:      map[string]string{"scenario": "success"},
		},
		From: "+10000000000",
		To:   []string{"+10000000002"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello via whatsapp"},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	validated, err := validator.ParseAndValidate(context.Background(), models.ChannelWhatsApp, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if validated == nil || validated.Request == nil {
		t.Fatalf("expected validated message")
	}
}

func TestValidatorRejectsUnsupportedBody(t *testing.T) {
	validator := wav.New(config.ValidationConfig{}, zerolog.Nop())

	payload := models.WhatsAppRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelWhatsApp,
			CreatedAt: time.Now().UTC(),
		},
		From: "+10000000000",
		To:   []string{"+10000000002"},
		Body: models.MessageBody{Type: "unsupported", Content: "hello"},
	}
	raw, _ := json.Marshal(payload)

	if _, err := validator.ParseAndValidate(context.Background(), models.ChannelWhatsApp, raw); err == nil {
		t.Fatalf("expected unsupported body type error")
	}
}

func TestValidatorRejectsInvalidPhone(t *testing.T) {
	validator := wav.New(config.ValidationConfig{}, zerolog.Nop())

	payload := models.WhatsAppRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelWhatsApp,
			CreatedAt: time.Now().UTC(),
		},
		From: "invalid",
		To:   []string{"+10000000002"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello"},
	}
	raw, _ := json.Marshal(payload)

	if _, err := validator.ParseAndValidate(context.Background(), models.ChannelWhatsApp, raw); err == nil {
		t.Fatalf("expected invalid from error")
	}
}

func TestValidatorContextCancel(t *testing.T) {
	validator := wav.New(config.ValidationConfig{}, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := validator.ParseAndValidate(ctx, models.ChannelWhatsApp, []byte("{}")); err == nil {
		t.Fatalf("expected context error")
	}
}
