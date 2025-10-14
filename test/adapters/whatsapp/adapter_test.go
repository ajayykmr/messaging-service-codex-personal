package whatsapp_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	common "github.com/example/messaging-microservice/internal/adapters/common"
	waadapter "github.com/example/messaging-microservice/internal/adapters/whatsapp"
	"github.com/example/messaging-microservice/internal/models"
	waprovider "github.com/example/messaging-microservice/internal/providers/whatsapp"
)

func TestAdapterSendSuccess(t *testing.T) {
	provider := waprovider.NewMockProvider(zerolog.Nop())
	adapter, err := waadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := buildValidatedMessage(nil)

	resp, err := adapter.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected send error: %v", err)
	}
	if resp.Status != "sent" {
		t.Fatalf("expected status sent, got %s", resp.Status)
	}
	if resp.Code == nil || *resp.Code != 200 {
		t.Fatalf("expected provider code 200, got %+v", resp.Code)
	}
}

func TestAdapterSendPermanentFailure(t *testing.T) {
	provider := waprovider.NewMockProvider(zerolog.Nop(), waprovider.WithScenario(waprovider.ScenarioPermanent))
	adapter, err := waadapter.NewAdapter(provider, zerolog.Nop())
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
	provider := waprovider.NewMockProvider(zerolog.Nop(), waprovider.WithScenario(waprovider.ScenarioTransient))
	adapter, err := waadapter.NewAdapter(provider, zerolog.Nop())
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
	provider := waprovider.NewMockProvider(zerolog.Nop())
	adapter, err := waadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := &common.ValidatedMessage{Request: "not-wa"}
	if _, err := adapter.Send(context.Background(), msg); !errors.Is(err, common.ErrPermanent) {
		t.Fatalf("expected permanent error for invalid request type, got %v", err)
	}
}

func buildValidatedMessage(meta map[string]any) *common.ValidatedMessage {
	if meta == nil {
		meta = map[string]any{}
	}
	req := &models.WhatsAppRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelWhatsApp,
			TraceID:   "trace-id",
			TenantID:  "tenant-123",
			CreatedAt: time.Now().UTC(),
		},
		From: "+10000000000",
		To:   []string{"+10000000002"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello via whatsapp"},
	}

	return &common.ValidatedMessage{
		Channel:      models.ChannelWhatsApp,
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
