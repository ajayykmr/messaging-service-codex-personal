package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	emailadapter "github.com/ajayykmr/messaging-service-go/internal/adapters/email"
	"github.com/ajayykmr/messaging-service-go/internal/config"
	"github.com/ajayykmr/messaging-service-go/internal/models"
	emailprovider "github.com/ajayykmr/messaging-service-go/internal/providers/email"
	"github.com/ajayykmr/messaging-service-go/internal/worker"
	emailvalidator "github.com/ajayykmr/messaging-service-go/internal/worker/validator/email"
)

type stubProvider struct {
	mu    sync.Mutex
	calls []*emailprovider.Payload
	resp  *emailprovider.RawResponse
	err   error
}

func (s *stubProvider) Send(ctx context.Context, payload *emailprovider.Payload) (*emailprovider.RawResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, payload)
	return s.resp, s.err
}

func (s *stubProvider) lastCall() *emailprovider.Payload {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return nil
	}
	return s.calls[len(s.calls)-1]
}

type statusSink struct {
	mu     sync.Mutex
	events []models.StatusEvent
}

func (s *statusSink) PublishStatus(ctx context.Context, event models.StatusEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *statusSink) snapshot() []models.StatusEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]models.StatusEvent, len(s.events))
	copy(out, s.events)
	return out
}

type dlqSink struct {
	mu      sync.Mutex
	records []models.DLQRecord
}

func (d *dlqSink) PublishDLQ(ctx context.Context, record models.DLQRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, record)
	return nil
}

func (d *dlqSink) snapshot() []models.DLQRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]models.DLQRecord, len(d.records))
	copy(out, d.records)
	return out
}

func waitForStatusEvents(t *testing.T, sink *statusSink, n int) []models.StatusEvent {
	deadline := time.After(500 * time.Millisecond)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		events := sink.snapshot()
		if len(events) >= n {
			return events
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("timed out waiting for %d status events (have %d)", n, len(events))
		}
	}
}

func waitForDLQRecords(t *testing.T, sink *dlqSink, n int) []models.DLQRecord {
	deadline := time.After(500 * time.Millisecond)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		records := sink.snapshot()
		if len(records) >= n {
			return records
		}
		if n == 0 && len(records) == 0 {
			return records
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatalf("timed out waiting for %d dlq records (have %d)", n, len(records))
		}
	}
}

func TestEmailWorkerIntegrationSuccess(t *testing.T) {
	provider := &stubProvider{
		resp: &emailprovider.RawResponse{ID: "smtp-123", Code: 250, Timestamp: time.Unix(100, 0)},
	}

	adapter, err := emailadapter.NewAdapter(provider, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("adapter init: %v", err)
	}

	validator := emailvalidator.New(validationConfig(), zerolog.New(io.Discard))
	status := &statusSink{}
	dlq := &dlqSink{}

	commitCh := make(chan struct{})
	engine, err := worker.NewEngine(workerConfig(), worker.Dependencies{
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

	raw := mustMarshal(sampleEmailRequest())
	record := &worker.Record{Topic: "messages.email.request", Key: []byte("key"), Value: raw}

	engine.HandleRecord(context.Background(), record)

	select {
	case <-commitCh:
	case <-time.After(time.Second):
		t.Fatal("expected commit to be called")
	}

	if provider.lastCall() == nil {
		t.Fatal("expected provider to be invoked")
	}

	events := waitForStatusEvents(t, status, 3)
	expectedTypes := []string{models.StatusEventQueued, models.StatusEventAttempt, models.StatusEventSent}
	assertEventTypes(t, events, expectedTypes)

	if records := waitForDLQRecords(t, dlq, 0); len(records) != 0 {
		t.Fatalf("expected no dlq records, got %v", records)
	}
}

func TestEmailWorkerIntegrationPermanentFailure(t *testing.T) {
	provider := &stubProvider{
		resp: &emailprovider.RawResponse{Code: 550},
		err:  errors.New("smtp 550 invalid recipient"),
	}

	adapter, err := emailadapter.NewAdapter(provider, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("adapter init: %v", err)
	}

	validator := emailvalidator.New(validationConfig(), zerolog.New(io.Discard))
	status := &statusSink{}
	dlq := &dlqSink{}

	commitCh := make(chan struct{})
	engine, err := worker.NewEngine(workerConfig(), worker.Dependencies{
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

	raw := mustMarshal(sampleEmailRequest())
	record := &worker.Record{Topic: "messages.email.request", Key: []byte("key"), Value: raw}

	engine.HandleRecord(context.Background(), record)

	<-commitCh

	events := waitForStatusEvents(t, status, 3)
	expectedTypes := []string{models.StatusEventQueued, models.StatusEventAttempt, models.StatusEventFailed}
	assertEventTypes(t, events, expectedTypes)

	records := waitForDLQRecords(t, dlq, 1)
	if records[0].FailureType != string(worker.FailureTypePermanent) {
		t.Fatalf("expected permanent failure, got %s", records[0].FailureType)
	}
}

func TestEmailWorkerIntegrationTransientExhaustion(t *testing.T) {
	provider := &stubProvider{
		resp: &emailprovider.RawResponse{Code: 421},
		err:  errors.New("smtp 421 temporary failure"),
	}

	adapter, err := emailadapter.NewAdapter(provider, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("adapter init: %v", err)
	}

	validator := emailvalidator.New(validationConfig(), zerolog.New(io.Discard))
	status := &statusSink{}
	dlq := &dlqSink{}

	cfg := workerConfig()
	cfg.MaxAttempts = 2

	commitCh := make(chan struct{})
	engine, err := worker.NewEngine(cfg, worker.Dependencies{
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

	raw := mustMarshal(sampleEmailRequest())
	record := &worker.Record{Topic: "messages.email.request", Key: []byte("key"), Value: raw}

	engine.HandleRecord(context.Background(), record)

	<-commitCh

	events := waitForStatusEvents(t, status, 4)
	expectedTypes := []string{models.StatusEventQueued, models.StatusEventAttempt, models.StatusEventAttempt, models.StatusEventFailed}
	assertEventTypes(t, events, expectedTypes)

	records := waitForDLQRecords(t, dlq, 1)
	if records[0].FailureType != string(worker.FailureTypeTransient) {
		t.Fatalf("expected transient failure, got %s", records[0].FailureType)
	}
}

func assertEventTypes(t *testing.T, events []models.StatusEvent, expected []string) {
	if len(events) != len(expected) {
		t.Fatalf("unexpected number of events: got %d want %d", len(events), len(expected))
	}
	for i, typ := range expected {
		if events[i].EventType != typ {
			t.Fatalf("event %d: expected %s, got %s", i, typ, events[i].EventType)
		}
	}
}

func workerConfig() worker.Config {
	return worker.Config{
		Channel:           models.ChannelEmail,
		MsgMaxBytes:       200000,
		MaxAttempts:       3,
		BaseBackoff:       0,
		MaxBackoff:        0,
		WorkerConcurrency: 1,
	}
}

func validationConfig() config.ValidationConfig {
	return config.ValidationConfig{
		RecipientsMax:   10,
		SubjectMaxLen:   255,
		BodyMaxBytes:    100000,
		MetaMaxEntries:  10,
		MetaMaxKeyLen:   32,
		MetaMaxValueLen: 64,
	}
}

func sampleEmailRequest() models.EmailRequest {
	return models.EmailRequest{
		Envelope: models.Envelope{
			MessageID: "b0c9c2b0-1f3a-4d2d-9e3f-123456789abc",
			Channel:   models.ChannelEmail,
			CreatedAt: time.Date(2025, 10, 11, 10, 0, 0, 0, time.UTC),
			Meta:      map[string]string{"source": "test"},
		},
		From:    "noreply@example.com",
		To:      []string{"user@example.com"},
		Subject: "Hello",
		Body: models.MessageBody{
			Type:    models.BodyTypeText,
			Content: "Body",
		},
	}
}

func mustMarshal(req models.EmailRequest) []byte {
	raw, err := json.Marshal(req)
	if err != nil {
		panic(err)
	}
	return raw
}
