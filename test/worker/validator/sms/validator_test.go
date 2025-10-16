package smsvalidator_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/ajayykmr/messaging-service-go/internal/config"
	"github.com/ajayykmr/messaging-service-go/internal/models"
	smsvalidator "github.com/ajayykmr/messaging-service-go/internal/worker/validator/sms"
)

func TestValidatorSuccess(t *testing.T) {
	cfg := config.ValidationConfig{
		SMSRecipientsMax: 5,
		SMSBodyMax:       160,
		MetaMaxEntries:   5,
		MetaMaxKeyLen:    16,
		MetaMaxValueLen:  32,
	}
	validator := smsvalidator.New(cfg, zerolog.Nop())

	payload := models.SMSRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelSMS,
			CreatedAt: time.Now().UTC(),
			TraceID:   "trace",
			TenantID:  "tenant",
			Meta:      map[string]string{"scenario": "success"},
		},
		From: "+10000000000",
		To:   []string{"+10000000001"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello"},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	validated, err := validator.ParseAndValidate(context.Background(), models.ChannelSMS, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if validated == nil || validated.Request == nil {
		t.Fatalf("expected validated message")
	}
}

func TestValidatorRejectsInvalidChannel(t *testing.T) {
	validator := smsvalidator.New(config.ValidationConfig{}, zerolog.Nop())

	payload := models.SMSRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   "email",
			CreatedAt: time.Now().UTC(),
		},
		From: "+10000000000",
		To:   []string{"+10000000001"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello"},
	}
	raw, _ := json.Marshal(payload)

	if _, err := validator.ParseAndValidate(context.Background(), models.ChannelSMS, raw); err == nil {
		t.Fatalf("expected channel mismatch error")
	}
}

func TestValidatorRejectsInvalidFrom(t *testing.T) {
	validator := smsvalidator.New(config.ValidationConfig{}, zerolog.Nop())

	payload := models.SMSRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelSMS,
			CreatedAt: time.Now().UTC(),
		},
		From: "invalid",
		To:   []string{"+10000000001"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello"},
	}
	raw, _ := json.Marshal(payload)

	if _, err := validator.ParseAndValidate(context.Background(), models.ChannelSMS, raw); err == nil {
		t.Fatalf("expected invalid from error")
	}
}

func TestValidatorContextCancel(t *testing.T) {
	validator := smsvalidator.New(config.ValidationConfig{}, zerolog.Nop())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := validator.ParseAndValidate(ctx, models.ChannelSMS, []byte("{}")); err == nil {
		t.Fatalf("expected context error")
	}
}
