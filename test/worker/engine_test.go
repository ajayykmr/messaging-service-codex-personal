package worker_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	common "github.com/example/messaging-microservice/internal/adapters/common"
	"github.com/example/messaging-microservice/internal/models"
	"github.com/example/messaging-microservice/internal/worker"
)

type adapterStub struct {
	mu        sync.Mutex
	responses []callResult
	index     int
}

type callResult struct {
	resp *common.ProviderResponse
	err  error
}

func (a *adapterStub) Send(ctx context.Context, msg *worker.ValidatedMessage) (*common.ProviderResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.responses) == 0 {
		return nil, nil
	}
	if a.index >= len(a.responses) {
		return a.responses[len(a.responses)-1].resp, a.responses[len(a.responses)-1].err
	}
	res := a.responses[a.index]
	a.index++
	return res.resp, res.err
}

type validatorStub struct {
	msg *worker.ValidatedMessage
	err error
}

func (v *validatorStub) ParseAndValidate(ctx context.Context, channel string, payload []byte) (*worker.ValidatedMessage, error) {
	return v.msg, v.err
}

type statusCollector struct {
	mu     sync.Mutex
	events []models.StatusEvent
}

func (s *statusCollector) PublishStatus(ctx context.Context, event models.StatusEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

type dlqCollector struct {
	mu      sync.Mutex
	records []models.DLQRecord
}

func (d *dlqCollector) PublishDLQ(ctx context.Context, record models.DLQRecord) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.records = append(d.records, record)
	return nil
}

func TestEngineHandleRecordSuccess(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(0, 0).UTC()

	validator := &validatorStub{
		msg: &worker.ValidatedMessage{
			Channel:    "email",
			MessageID:  "msg-1",
			RawPayload: []byte(`{"message":"hello"}`),
		},
	}
	adapter := &adapterStub{
		responses: []callResult{
			{resp: &common.ProviderResponse{Status: "ok", Message: "sent"}},
		},
	}
	status := &statusCollector{}
	dlq := &dlqCollector{}

	commitCh := make(chan struct{})
	commitFn := func(context.Context, *worker.Record) error {
		close(commitCh)
		return nil
	}

	cfg := worker.Config{
		Channel:           "email",
		MsgMaxBytes:       1024,
		MaxAttempts:       3,
		BaseBackoff:       0,
		MaxBackoff:        0,
		WorkerConcurrency: 1,
	}

	engine, err := worker.NewEngine(cfg, worker.Dependencies{
		Adapter:         adapter,
		Validator:       validator,
		StatusPublisher: status,
		DLQPublisher:    dlq,
		Committer:       worker.CommitFunc(commitFn),
		Logger:          zerolog.New(io.Discard),
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("unexpected engine error: %v", err)
	}

	record := &worker.Record{
		Topic:   "email.request",
		Key:     []byte("msg-1"),
		Value:   []byte(`{"message":"hello"}`),
		Headers: map[string][]byte{"trace": []byte("abc")},
	}

	engine.HandleRecord(ctx, record)

	select {
	case <-commitCh:
	case <-time.After(time.Second):
		t.Fatalf("expected commit to be called")
	}

	if len(status.events) == 0 {
		t.Fatalf("expected status events")
	}
	expectedOrder := []string{models.StatusEventQueued, models.StatusEventAttempt, models.StatusEventSent}
	if !eventTypesMatch(status.events, expectedOrder) {
		t.Fatalf("unexpected status order: %v", status.events)
	}

	if len(dlq.records) != 0 {
		t.Fatalf("did not expect DLQ records, got %d", len(dlq.records))
	}
}

func TestEngineValidationFailure(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1, 0).UTC()

	validator := &validatorStub{
		msg: nil,
		err: errors.New("invalid"),
	}
	status := &statusCollector{}
	dlq := &dlqCollector{}

	committed := false
	commitFn := func(context.Context, *worker.Record) error {
		committed = true
		return nil
	}

	engine, err := worker.NewEngine(worker.Config{
		Channel:           "email",
		MsgMaxBytes:       10,
		MaxAttempts:       1,
		BaseBackoff:       0,
		MaxBackoff:        0,
		WorkerConcurrency: 1,
	}, worker.Dependencies{
		Adapter:         &adapterStub{},
		Validator:       validator,
		StatusPublisher: status,
		DLQPublisher:    dlq,
		Committer:       worker.CommitFunc(commitFn),
		Logger:          zerolog.New(io.Discard),
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("unexpected engine error: %v", err)
	}

	record := &worker.Record{
		Topic: "email.request",
		Key:   []byte("msg-2"),
		Value: []byte(`{"bad":"payload"}`),
	}

	engine.HandleRecord(ctx, record)

	if len(status.events) != 1 || status.events[0].EventType != models.StatusEventFailed {
		t.Fatalf("expected failed status event, got %+v", status.events)
	}
	if len(dlq.records) != 1 {
		t.Fatalf("expected DLQ record")
	}
	if dlq.records[0].FailureType != string(worker.FailureTypeValidation) {
		t.Fatalf("expected validation failure type, got %s", dlq.records[0].FailureType)
	}
	if !committed {
		t.Fatalf("expected commit on validation failure")
	}
}

func TestEngineRetriesTransientFailures(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(2, 0).UTC()

	validator := &validatorStub{
		msg: &worker.ValidatedMessage{
			Channel:   "email",
			MessageID: "msg-3",
		},
	}
	adapter := &adapterStub{
		responses: []callResult{
			{err: common.ErrTransient},
			{err: common.ErrTransient},
		},
	}
	status := &statusCollector{}
	dlq := &dlqCollector{}

	commitCh := make(chan struct{})
	commitFn := func(context.Context, *worker.Record) error {
		close(commitCh)
		return nil
	}

	engine, err := worker.NewEngine(worker.Config{
		Channel:           "email",
		MsgMaxBytes:       1024,
		MaxAttempts:       2,
		BaseBackoff:       0,
		MaxBackoff:        0,
		WorkerConcurrency: 1,
	}, worker.Dependencies{
		Adapter:         adapter,
		Validator:       validator,
		StatusPublisher: status,
		DLQPublisher:    dlq,
		Committer:       worker.CommitFunc(commitFn),
		Logger:          zerolog.New(io.Discard),
		Now:             func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("unexpected engine error: %v", err)
	}

	record := &worker.Record{
		Topic: "email.request",
		Key:   []byte("msg-3"),
		Value: []byte(`{"message":"retry"}`),
	}

	engine.HandleRecord(ctx, record)

	select {
	case <-commitCh:
	case <-time.After(time.Second):
		t.Fatalf("expected commit after retries")
	}

	if !eventTypesMatch(status.events, []string{
		models.StatusEventQueued,
		models.StatusEventAttempt,
		models.StatusEventAttempt,
		models.StatusEventFailed,
	}) {
		t.Fatalf("unexpected status events %+v", status.events)
	}

	if len(dlq.records) != 1 {
		t.Fatalf("expected single DLQ record")
	}
	if dlq.records[0].FailureType != string(worker.FailureTypeTransient) {
		t.Fatalf("expected transient failure type, got %s", dlq.records[0].FailureType)
	}
}

func eventTypesMatch(events []models.StatusEvent, expected []string) bool {
	if len(events) != len(expected) {
		return false
	}
	for i, evt := range events {
		if evt.EventType != expected[i] {
			return false
		}
	}
	return true
}
