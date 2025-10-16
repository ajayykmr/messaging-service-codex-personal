package integration_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"

	waadapter "github.com/ajayykmr/messaging-service-go/internal/adapters/whatsapp"
	"github.com/ajayykmr/messaging-service-go/internal/config"
	"github.com/ajayykmr/messaging-service-go/internal/models"
	waprovider "github.com/ajayykmr/messaging-service-go/internal/providers/whatsapp"
	"github.com/ajayykmr/messaging-service-go/internal/worker"
	wav "github.com/ajayykmr/messaging-service-go/internal/worker/validator/whatsapp"
)

func TestWhatsAppWorkerIntegrationSuccess(t *testing.T) {
	provider := waprovider.NewMockProvider(zerolog.Nop(), waprovider.WithLatency(0))
	adapter, err := waadapter.NewAdapter(provider, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("adapter init: %v", err)
	}

	validator := wav.New(whatsAppValidationConfig(), zerolog.New(io.Discard))
	status := &statusSink{}
	dlq := &dlqSink{}

	commitCh := make(chan struct{})
	engine, err := worker.NewEngine(whatsAppWorkerConfig(), worker.Dependencies{
		Adapter:         adapter,
		Validator:       validator,
		StatusPublisher: status,
		DLQPublisher:    dlq,
		Committer: worker.CommitFunc(func(ctx context.Context, rec *worker.Record) error {
			close(commitCh)
			return nil
		}),
		Logger: zerolog.New(io.Discard),
		Now:    func() time.Time { return time.Unix(200, 0) },
	})
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}

	raw := marshal(sampleWhatsAppRequest(nil))
	record := &worker.Record{Topic: "messages.whatsapp.request", Key: []byte("msg-wa-1"), Value: raw}

	engine.HandleRecord(context.Background(), record)

	select {
	case <-commitCh:
	case <-time.After(time.Second):
		t.Fatal("expected commit to be called")
	}

	events := waitForStatusEvents(t, status, 3)
	assertEventTypes(t, events, []string{models.StatusEventQueued, models.StatusEventAttempt, models.StatusEventSent})
	if records := waitForDLQRecords(t, dlq, 0); len(records) != 0 {
		t.Fatalf("expected no dlq records, got %v", records)
	}
}

func TestWhatsAppWorkerIntegrationPermanentFailure(t *testing.T) {
	provider := waprovider.NewMockProvider(zerolog.Nop(), waprovider.WithLatency(0))
	adapter, err := waadapter.NewAdapter(provider, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("adapter init: %v", err)
	}

	validator := wav.New(whatsAppValidationConfig(), zerolog.New(io.Discard))
	status := &statusSink{}
	dlq := &dlqSink{}

	commitCh := make(chan struct{})
	engine, err := worker.NewEngine(whatsAppWorkerConfig(), worker.Dependencies{
		Adapter:         adapter,
		Validator:       validator,
		StatusPublisher: status,
		DLQPublisher:    dlq,
		Committer: worker.CommitFunc(func(ctx context.Context, rec *worker.Record) error {
			close(commitCh)
			return nil
		}),
		Logger: zerolog.New(io.Discard),
		Now:    func() time.Time { return time.Unix(200, 0) },
	})
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}

	raw := marshal(sampleWhatsAppRequest(map[string]string{"scenario": string(waprovider.ScenarioPermanent)}))
	record := &worker.Record{Topic: "messages.whatsapp.request", Key: []byte("msg-wa-2"), Value: raw}

	engine.HandleRecord(context.Background(), record)

	<-commitCh

	events := waitForStatusEvents(t, status, 3)
	assertEventTypes(t, events, []string{models.StatusEventQueued, models.StatusEventAttempt, models.StatusEventFailed})
	records := waitForDLQRecords(t, dlq, 1)
	if records[0].FailureType != string(worker.FailureTypePermanent) {
		t.Fatalf("expected permanent failure, got %s", records[0].FailureType)
	}
}

func TestWhatsAppWorkerIntegrationTransientExhaustion(t *testing.T) {
	provider := waprovider.NewMockProvider(zerolog.Nop(), waprovider.WithLatency(0))
	adapter, err := waadapter.NewAdapter(provider, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("adapter init: %v", err)
	}

	validator := wav.New(whatsAppValidationConfig(), zerolog.New(io.Discard))
	status := &statusSink{}
	dlq := &dlqSink{}

	cfg := whatsAppWorkerConfig()
	cfg.MaxAttempts = 2

	engine, err := worker.NewEngine(cfg, worker.Dependencies{
		Adapter:         adapter,
		Validator:       validator,
		StatusPublisher: status,
		DLQPublisher:    dlq,
		Committer:       worker.CommitFunc(func(ctx context.Context, rec *worker.Record) error { return nil }),
		Logger:          zerolog.New(io.Discard),
		Now:             func() time.Time { return time.Unix(200, 0) },
	})
	if err != nil {
		t.Fatalf("engine init: %v", err)
	}

	raw := marshal(sampleWhatsAppRequest(map[string]string{"scenario": string(waprovider.ScenarioTransient)}))
	record := &worker.Record{Topic: "messages.whatsapp.request", Key: []byte("msg-wa-3"), Value: raw}

	engine.HandleRecord(context.Background(), record)

	events := waitForStatusEvents(t, status, 4)
	assertEventTypes(t, events, []string{models.StatusEventQueued, models.StatusEventAttempt, models.StatusEventAttempt, models.StatusEventFailed})

	records := waitForDLQRecords(t, dlq, 1)
	if records[0].FailureType != string(worker.FailureTypeTransient) {
		t.Fatalf("expected transient failure, got %s", records[0].FailureType)
	}
}

func sampleWhatsAppRequest(meta map[string]string) models.WhatsAppRequest {
	if meta == nil {
		meta = map[string]string{}
	}
	return models.WhatsAppRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelWhatsApp,
			CreatedAt: time.Date(2025, 10, 11, 10, 0, 0, 0, time.UTC),
			TraceID:   "trace-id",
			TenantID:  "tenant",
			Meta:      meta,
		},
		From: "+10000000000",
		To:   []string{"+10000000002"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello via whatsapp"},
	}
}

func whatsAppWorkerConfig() worker.Config {
	return worker.Config{
		Channel:           models.ChannelWhatsApp,
		MsgMaxBytes:       200000,
		MaxAttempts:       3,
		BaseBackoff:       0,
		MaxBackoff:        0,
		WorkerConcurrency: 1,
	}
}

func whatsAppValidationConfig() config.ValidationConfig {
	return config.ValidationConfig{
		RecipientsMax:   10,
		WABodyMax:       4096,
		MetaMaxEntries:  10,
		MetaMaxKeyLen:   32,
		MetaMaxValueLen: 64,
	}
}
