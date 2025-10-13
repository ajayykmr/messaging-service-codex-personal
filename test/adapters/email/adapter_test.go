package email_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	common "github.com/example/messaging-microservice/internal/adapters/common"
	emailadapter "github.com/example/messaging-microservice/internal/adapters/email"
	"github.com/example/messaging-microservice/internal/models"
	emailprovider "github.com/example/messaging-microservice/internal/providers/email"
	"github.com/example/messaging-microservice/internal/worker"
)

func TestAdapterSendSuccess(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop(), emailprovider.WithRandomSeed(1))
	adapter, err := emailadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := buildValidatedMessage()

	resp, err := adapter.Send(context.Background(), msg)
	if err != nil {
		t.Fatalf("unexpected send error: %v", err)
	}

	if resp.Status != "ok" {
		t.Fatalf("expected status ok, got %s", resp.Status)
	}

	if resp.Code == nil || *resp.Code != 250 {
		t.Fatalf("expected smtp code 250, got %+v", resp.Code)
	}

	if resp.Message != "sent" {
		t.Fatalf("unexpected message: %s", resp.Message)
	}

	if resp.Meta == nil || resp.Meta["provider_id"] == "" {
		t.Fatalf("expected provider id in meta, got %+v", resp.Meta)
	}

	if resp.Raw == "" {
		t.Fatalf("expected raw response body to be captured")
	}
}

func TestAdapterSendPermanentFailure(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop(), emailprovider.WithRandomSeed(1))
	adapter, err := emailadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := buildValidatedMessage()
	msg.KafkaHeaders = map[string][]byte{
		"X-Mock-Provider-Scenario": []byte("permanent"),
	}

	resp, err := adapter.Send(context.Background(), msg)
	if !errors.Is(err, common.ErrPermanent) {
		t.Fatalf("expected permanent error classification, got %v", err)
	}
	if resp.Status != "rejected" {
		t.Fatalf("expected rejected status, got %s", resp.Status)
	}
	if resp.Code == nil || *resp.Code != 550 {
		t.Fatalf("expected smtp code 550, got %+v", resp.Code)
	}
}

func TestAdapterSendTransientFailure(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop(), emailprovider.WithRandomSeed(1))
	adapter, err := emailadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := buildValidatedMessage()
	msg.KafkaHeaders = map[string][]byte{
		"X-Mock-Provider-Scenario": []byte("transient"),
	}

	resp, err := adapter.Send(context.Background(), msg)
	if !errors.Is(err, common.ErrTransient) {
		t.Fatalf("expected transient error classification, got %v", err)
	}
	if resp.Status != "rate_limited" {
		t.Fatalf("expected rate_limited status, got %s", resp.Status)
	}
	if resp.Code == nil || *resp.Code != 451 {
		t.Fatalf("expected smtp code 451, got %+v", resp.Code)
	}
}

func TestAdapterSendTimeout(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop(), emailprovider.WithRandomSeed(1))
	adapter, err := emailadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := buildValidatedMessage()
	msg.KafkaHeaders = map[string][]byte{
		"X-Mock-Provider-Scenario": []byte("timeout"),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err = adapter.Send(ctx, msg)
	if !errors.Is(err, common.ErrTransient) {
		t.Fatalf("expected timeout to be classified as transient, got %v", err)
	}
}

func TestAdapterWithInvalidRequestType(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop(), emailprovider.WithRandomSeed(1))
	adapter, err := emailadapter.NewAdapter(provider, zerolog.Nop())
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}

	msg := &worker.ValidatedMessage{Request: "not-an-email"}
	if _, err := adapter.Send(context.Background(), msg); !errors.Is(err, common.ErrPermanent) {
		t.Fatalf("expected permanent error for invalid request type, got %v", err)
	}
}

func buildValidatedMessage() *worker.ValidatedMessage {
	req := &models.EmailRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelEmail,
			TraceID:   "trace-id",
			TenantID:  "tenant-123",
			CreatedAt: time.Now().UTC(),
			Meta:      map[string]string{"source": "test"},
		},
		From:    "noreply@example.com",
		To:      []string{"user@example.com"},
		Subject: "Welcome",
		Body: models.MessageBody{
			Type:    models.BodyTypeHTML,
			Content: "<p>Hello</p>",
		},
	}

	return &worker.ValidatedMessage{
		Channel:   models.ChannelEmail,
		MessageID: req.MessageID,
		TraceID:   req.TraceID,
		TenantID:  req.TenantID,
		Metadata:  map[string]any{"source": "test"},
		Request:   req,
	}
}
