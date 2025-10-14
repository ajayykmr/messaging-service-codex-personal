package sms_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	common "github.com/example/messaging-microservice/internal/adapters/common"
	smsadapter "github.com/example/messaging-microservice/internal/adapters/sms"
	"github.com/example/messaging-microservice/internal/models"
	smsprovider "github.com/example/messaging-microservice/internal/providers/sms"
)

func TestAdapterSendSuccess(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop())
	adapter, err := smsadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := buildValidatedMessage(nil)

	resp, err := adapter.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected send error: %v", err)
	}
	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %s", resp.Status)
	}
	if resp.Code == nil || *resp.Code != 200 {
		t.Fatalf("expected provider code 200, got %+v", resp.Code)
	}
	if resp.Meta == nil || resp.Meta["provider_id"] == "" {
		t.Fatalf("expected provider id in meta, got %+v", resp.Meta)
	}
}

func TestAdapterSendPermanentFailure(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithScenario(smsprovider.ScenarioPermanent))
	adapter, err := smsadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := buildValidatedMessage(nil)

	resp, err := adapter.Send(context.Background(), msg)
	if err == nil {
		t.Fatalf("expected error for permanent failure")
	}
	if !errors.Is(err, common.ErrPermanent) {
		t.Fatalf("expected permanent error, got %v", err)
	}
	if resp.Status != "rejected" {
		t.Fatalf("expected rejected status, got %s", resp.Status)
	}
}

func TestAdapterSendTransientFailure(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithScenario(smsprovider.ScenarioTransient))
	adapter, err := smsadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := buildValidatedMessage(nil)

	resp, err := adapter.Send(context.Background(), msg)
	if err == nil {
		t.Fatalf("expected error for transient failure")
	}
	if !errors.Is(err, common.ErrTransient) {
		t.Fatalf("expected transient error, got %v", err)
	}
	if resp.Status != "rate_limited" {
		t.Fatalf("expected rate_limited status, got %s", resp.Status)
	}
}

func TestAdapterRejectsInvalidRequestType(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop())
	adapter, err := smsadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := &common.ValidatedMessage{Request: "not-an-sms"}
	if _, err := adapter.Send(context.Background(), msg); !errors.Is(err, common.ErrPermanent) {
		t.Fatalf("expected permanent error for invalid request type, got %v", err)
	}
}

func buildValidatedMessage(meta map[string]any) *common.ValidatedMessage {
	if meta == nil {
		meta = map[string]any{}
	}
	req := &models.SMSRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelSMS,
			TraceID:   "trace-id",
			TenantID:  "tenant-123",
			CreatedAt: time.Now().UTC(),
		},
		From: "+10000000000",
		To:   []string{"+10000000001"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello"},
	}

	return &common.ValidatedMessage{
		Channel:      models.ChannelSMS,
		MessageID:    req.MessageID,
		TraceID:      req.TraceID,
		TenantID:     req.TenantID,
		CreatedAt:    req.CreatedAt,
		Metadata:     meta,
		Request:      req,
		RawPayload:   []byte("{}"),
		Key:          []byte(req.MessageID),
		KafkaHeaders: map[string][]byte{},
	}
}
