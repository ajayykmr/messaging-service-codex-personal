package publisher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/rs/zerolog"

	"github.com/example/messaging-microservice/internal/models"
)

var errProducerNotInitialised = errors.New("kafka publisher: producer not initialised")

// SyncProducer captures the subset of producer behaviour required by the Kafka publishers.
type SyncProducer interface {
	PublishSync(topic string, key []byte, headers map[string][]byte, payload []byte) error
}

// ErrProducerNotInitialised exposes the sentinel error for callers and tests.
func ErrProducerNotInitialised() error {
	return errProducerNotInitialised
}

// StatusPublisher emits status events to a Kafka topic using the shared producer.
type StatusPublisher struct {
	producer SyncProducer
	topic    string
	logger   zerolog.Logger
}

// NewStatusPublisher constructs a StatusPublisher instance.
func NewStatusPublisher(prod SyncProducer, topic string, logger zerolog.Logger) *StatusPublisher {
	if prod == nil {
		return nil
	}
	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}
	return &StatusPublisher{
		producer: prod,
		topic:    topic,
		logger:   logger,
	}
}

// PublishStatus writes the supplied status event to Kafka synchronously.
func (p *StatusPublisher) PublishStatus(_ context.Context, event models.StatusEvent) error {
	if p == nil || p.producer == nil {
		return errProducerNotInitialised
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("kafka publisher: marshal status event: %w", err)
	}

	key := []byte(event.MessageID)
	headers := map[string][]byte{
		"content-type": []byte("application/json"),
	}

	if err := p.producer.PublishSync(p.topic, cloneBytes(key), headers, payload); err != nil {
		return fmt.Errorf("kafka publisher: publish status event: %w", err)
	}
	return nil
}

// DLQPublisher writes DLQ records to the configured Kafka topic.
type DLQPublisher struct {
	producer SyncProducer
	topic    string
	logger   zerolog.Logger
}

// NewDLQPublisher constructs a DLQPublisher instance.
func NewDLQPublisher(prod SyncProducer, topic string, logger zerolog.Logger) *DLQPublisher {
	if prod == nil {
		return nil
	}
	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}
	return &DLQPublisher{
		producer: prod,
		topic:    topic,
		logger:   logger,
	}
}

// PublishDLQ writes the supplied DLQ record to Kafka synchronously.
func (p *DLQPublisher) PublishDLQ(_ context.Context, record models.DLQRecord) error {
	if p == nil || p.producer == nil {
		return errProducerNotInitialised
	}

	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("kafka publisher: marshal dlq record: %w", err)
	}

	key := []byte(record.MessageID)
	headers := map[string][]byte{
		"content-type": []byte("application/json"),
	}

	if err := p.producer.PublishSync(p.topic, cloneBytes(key), headers, payload); err != nil {
		return fmt.Errorf("kafka publisher: publish dlq record: %w", err)
	}
	return nil
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}
