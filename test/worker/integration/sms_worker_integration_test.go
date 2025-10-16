package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"

	smsadapter "github.com/ajayykmr/messaging-service-go/internal/adapters/sms"
	"github.com/ajayykmr/messaging-service-go/internal/config"
	"github.com/ajayykmr/messaging-service-go/internal/models"
	smsprovider "github.com/ajayykmr/messaging-service-go/internal/providers/sms"
	"github.com/ajayykmr/messaging-service-go/internal/worker"
	smsvalidator "github.com/ajayykmr/messaging-service-go/internal/worker/validator/sms"
)

func TestSMSWorkerIntegrationSuccess(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithLatency(0))
	adapter, err := smsadapter.NewAdapter(provider, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("adapter init: %v", err)
	}

	validator := smsvalidator.New(smsValidationConfig(), zerolog.New(io.Discard))
	status := &statusSink{}
	dlq := &dlqSink{}

	commitCh := make(chan struct{})
	engine, err := worker.NewEngine(smsWorkerConfig(), worker.Dependencies{
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

	raw := marshal(sampleSMSRequest(nil))
	record := &worker.Record{Topic: "messages.sms.request", Key: []byte("msg-1"), Value: raw}

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

func TestSMSWorkerIntegrationPermanentFailure(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithLatency(0))
	adapter, err := smsadapter.NewAdapter(provider, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("adapter init: %v", err)
	}

	validator := smsvalidator.New(smsValidationConfig(), zerolog.New(io.Discard))
	status := &statusSink{}
	dlq := &dlqSink{}

	commitCh := make(chan struct{})
	engine, err := worker.NewEngine(smsWorkerConfig(), worker.Dependencies{
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

	raw := marshal(sampleSMSRequest(map[string]string{"scenario": string(smsprovider.ScenarioPermanent)}))
	record := &worker.Record{Topic: "messages.sms.request", Key: []byte("msg-2"), Value: raw}

	engine.HandleRecord(context.Background(), record)

	<-commitCh

	events := waitForStatusEvents(t, status, 3)
	assertEventTypes(t, events, []string{models.StatusEventQueued, models.StatusEventAttempt, models.StatusEventFailed})
	records := waitForDLQRecords(t, dlq, 1)
	if records[0].FailureType != string(worker.FailureTypePermanent) {
		t.Fatalf("expected permanent failure, got %s", records[0].FailureType)
	}
}

func TestSMSWorkerIntegrationTransientExhaustion(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithLatency(0))
	adapter, err := smsadapter.NewAdapter(provider, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("adapter init: %v", err)
	}

	validator := smsvalidator.New(smsValidationConfig(), zerolog.New(io.Discard))
	status := &statusSink{}
	dlq := &dlqSink{}

	cfg := smsWorkerConfig()
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

	raw := marshal(sampleSMSRequest(map[string]string{"scenario": string(smsprovider.ScenarioTransient)}))
	record := &worker.Record{Topic: "messages.sms.request", Key: []byte("msg-3"), Value: raw}

	engine.HandleRecord(context.Background(), record)

	events := waitForStatusEvents(t, status, 4)
	assertEventTypes(t, events, []string{models.StatusEventQueued, models.StatusEventAttempt, models.StatusEventAttempt, models.StatusEventFailed})

	records := waitForDLQRecords(t, dlq, 1)
	if records[0].FailureType != string(worker.FailureTypeTransient) {
		t.Fatalf("expected transient failure, got %s", records[0].FailureType)
	}
}

func sampleSMSRequest(meta map[string]string) models.SMSRequest {
	if meta == nil {
		meta = map[string]string{}
	}
	return models.SMSRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelSMS,
			CreatedAt: time.Date(2025, 10, 11, 10, 0, 0, 0, time.UTC),
			TraceID:   "trace-id",
			TenantID:  "tenant",
			Meta:      meta,
		},
		From: "+10000000000",
		To:   []string{"+10000000001"},
		Body: models.MessageBody{Type: models.BodyTypeText, Content: "hello"},
	}
}

func marshal(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func smsWorkerConfig() worker.Config {
	return worker.Config{
		Channel:           models.ChannelSMS,
		MsgMaxBytes:       200000,
		MaxAttempts:       3,
		BaseBackoff:       0,
		MaxBackoff:        0,
		WorkerConcurrency: 1,
	}
}

func smsValidationConfig() config.ValidationConfig {
	return config.ValidationConfig{
		SMSRecipientsMax: 10,
		SMSBodyMax:       1600,
		MetaMaxEntries:   10,
		MetaMaxKeyLen:    32,
		MetaMaxValueLen:  64,
		RecipientsMax:    10,
	}
}
