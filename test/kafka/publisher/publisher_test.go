package publisher_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	kafkapublisher "github.com/ajayykmr/messaging-service-go/internal/kafka/publisher"
	"github.com/ajayykmr/messaging-service-go/internal/models"
)

type fakeSyncProducer struct {
	err     error
	topic   string
	key     []byte
	headers map[string][]byte
	payload []byte
}

func (f *fakeSyncProducer) PublishSync(topic string, key []byte, headers map[string][]byte, payload []byte) error {
	f.topic = topic
	f.key = append([]byte(nil), key...)
	f.headers = headers
	f.payload = append([]byte(nil), payload...)
	return f.err
}

func TestStatusPublisherPublishesEvent(t *testing.T) {
	prod := &fakeSyncProducer{}
	pub := kafkapublisher.NewStatusPublisher(prod, "status-topic", zerolog.Nop())
	if pub == nil {
		t.Fatalf("expected publisher instance")
	}

	event := models.StatusEvent{
		MessageID: "message-1",
		Channel:   "email",
		EventType: models.StatusEventQueued,
		Attempt:   0,
		Timestamp: time.Unix(123, 0).UTC(),
	}

	if err := pub.PublishStatus(context.Background(), event); err != nil {
		t.Fatalf("unexpected publish error: %v", err)
	}

	if prod.topic != "status-topic" {
		t.Fatalf("expected topic status-topic, got %s", prod.topic)
	}
	if string(prod.key) != "message-1" {
		t.Fatalf("expected key message-1, got %s", string(prod.key))
	}
	if ct := prod.headers["content-type"]; string(ct) != "application/json" {
		t.Fatalf("expected content-type header, got %s", string(ct))
	}

	var payload models.StatusEvent
	if err := json.Unmarshal(prod.payload, &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if payload.EventType != models.StatusEventQueued || payload.Channel != "email" {
		t.Fatalf("unexpected payload %+v", payload)
	}
}

func TestStatusPublisherPropagatesProducerError(t *testing.T) {
	expectedErr := errors.New("broker down")
	prod := &fakeSyncProducer{err: expectedErr}

	pub := kafkapublisher.NewStatusPublisher(prod, "status-topic", zerolog.Nop())
	err := pub.PublishStatus(context.Background(), models.StatusEvent{MessageID: "id"})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected producer error, got %v", err)
	}
}

func TestStatusPublisherHandlesNilInstance(t *testing.T) {
	var pub *kafkapublisher.StatusPublisher
	if err := pub.PublishStatus(context.Background(), models.StatusEvent{}); !errors.Is(err, kafkapublisher.ErrProducerNotInitialised()) {
		t.Fatalf("expected not initialised error, got %v", err)
	}
}

func TestDLQPublisherPublishesRecord(t *testing.T) {
	prod := &fakeSyncProducer{}
	pub := kafkapublisher.NewDLQPublisher(prod, "dlq-topic", zerolog.Nop())

	record := models.DLQRecord{
		MessageID: "message-2",
		Channel:   "email",
		Attempts:  3,
	}

	if err := pub.PublishDLQ(context.Background(), record); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if prod.topic != "dlq-topic" {
		t.Fatalf("expected dlq-topic, got %s", prod.topic)
	}

	var decoded models.DLQRecord
	if err := json.Unmarshal(prod.payload, &decoded); err != nil {
		t.Fatalf("failed to decode payload: %v", err)
	}
	if decoded.Attempts != 3 || decoded.MessageID != "message-2" {
		t.Fatalf("unexpected DLQ payload %+v", decoded)
	}
}

func TestDLQPublisherPropagatesProducerError(t *testing.T) {
	expectedErr := errors.New("inject")
	prod := &fakeSyncProducer{err: expectedErr}
	pub := kafkapublisher.NewDLQPublisher(prod, "dlq-topic", zerolog.Nop())

	if err := pub.PublishDLQ(context.Background(), models.DLQRecord{MessageID: "id"}); !errors.Is(err, expectedErr) {
		t.Fatalf("expected producer error, got %v", err)
	}
}
