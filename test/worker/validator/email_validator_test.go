package validator_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/example/messaging-microservice/internal/config"
	"github.com/example/messaging-microservice/internal/models"
	emailvalidator "github.com/example/messaging-microservice/internal/worker/validator/email"
)

func TestValidatorParseAndValidateSuccess(t *testing.T) {
	cfg := config.ValidationConfig{
		RecipientsMax:   10,
		SubjectMaxLen:   255,
		BodyMaxBytes:    10000,
		MetaMaxEntries:  5,
		MetaMaxKeyLen:   32,
		MetaMaxValueLen: 64,
	}

	validator := emailvalidator.New(cfg, zerolog.Nop())

	payload := models.EmailRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelEmail,
			CreatedAt: time.Date(2025, 10, 11, 10, 0, 0, 0, time.UTC),
			TraceID:   "trace-1",
			TenantID:  "tenant-1",
			Meta:      map[string]string{"source": "test"},
		},
		From:    "noreply@example.com",
		To:      []string{"user@example.com"},
		CC:      []string{"carbon@example.com"},
		BCC:     []string{"blind@example.com"},
		Subject: "Sample",
		Body: models.MessageBody{
			Type:    models.BodyTypeHTML,
			Content: "<p>Hello</p>",
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	validated, err := validator.ParseAndValidate(context.Background(), models.ChannelEmail, raw)
	if err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if validated == nil {
		t.Fatalf("expected validated message")
	}

	if validated.MessageID != payload.MessageID {
		t.Fatalf("expected message id %s, got %s", payload.MessageID, validated.MessageID)
	}
	if _, ok := validated.Request.(*models.EmailRequest); !ok {
		t.Fatalf("expected request to be *models.EmailRequest")
	}
}

func TestValidatorRejectsEmptyPayload(t *testing.T) {
	validator := emailvalidator.New(config.ValidationConfig{}, zerolog.Nop())
	if _, err := validator.ParseAndValidate(context.Background(), models.ChannelEmail, nil); err == nil {
		t.Fatalf("expected error for empty payload")
	}
}

func TestValidatorRejectsBadJSON(t *testing.T) {
	validator := emailvalidator.New(config.ValidationConfig{}, zerolog.Nop())
	payload := []byte(`{"invalid":true`)
	if _, err := validator.ParseAndValidate(context.Background(), models.ChannelEmail, payload); err == nil {
		t.Fatalf("expected error for malformed json")
	}
}

func TestValidatorRejectsInvalidChannel(t *testing.T) {
	cfg := config.ValidationConfig{}
	validator := emailvalidator.New(cfg, zerolog.Nop())

	payload := models.EmailRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   "sms",
			CreatedAt: time.Now().UTC(),
		},
		From: "noreply@example.com",
		To:   []string{"user@example.com"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hi"},
	}
	raw, _ := json.Marshal(payload)

	if _, err := validator.ParseAndValidate(context.Background(), models.ChannelEmail, raw); err == nil {
		t.Fatalf("expected error for channel mismatch")
	}
}

func TestValidatorRejectsInvalidEmailAddresses(t *testing.T) {
	cfg := config.ValidationConfig{
		RecipientsMax: 2,
	}
	validator := emailvalidator.New(cfg, zerolog.Nop())

	payload := models.EmailRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelEmail,
			CreatedAt: time.Now().UTC(),
		},
		From: "bad-from",
		To:   []string{"user@example.com"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hi"},
	}
	raw, _ := json.Marshal(payload)

	if _, err := validator.ParseAndValidate(context.Background(), models.ChannelEmail, raw); err == nil {
		t.Fatalf("expected error for invalid from address")
	}
}

func TestValidatorRejectsTooLargeBody(t *testing.T) {
	cfg := config.ValidationConfig{
		BodyMaxBytes: 4,
	}
	validator := emailvalidator.New(cfg, zerolog.Nop())

	payload := models.EmailRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelEmail,
			CreatedAt: time.Now().UTC(),
		},
		From: "noreply@example.com",
		To:   []string{"user@example.com"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello"},
	}
	raw, _ := json.Marshal(payload)

	if _, err := validator.ParseAndValidate(context.Background(), models.ChannelEmail, raw); err == nil {
		t.Fatalf("expected error for oversize body")
	}
}

func TestValidatorRespectsContextCancellation(t *testing.T) {
	validator := emailvalidator.New(config.ValidationConfig{}, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := validator.ParseAndValidate(ctx, models.ChannelEmail, []byte(`{"channel":"email"}`))
	if err == nil {
		t.Fatalf("expected context error")
	}
}
