package worker

import (
	"context"

	"github.com/ajayykmr/messaging-service-go/internal/kafka/consumer"
)

// NewRecordFromConsumer constructs a worker record from the supplied Kafka
// consumer record and binds the provided commit function. The commit function is
// invoked when the engine successfully processes the record and needs to commit
// the underlying offset.
func NewRecordFromConsumer(rec *consumer.Record, commit func(context.Context) error) *Record {
	if rec == nil {
		return nil
	}

	wr := &Record{
		Topic:     rec.Topic,
		Partition: rec.Partition,
		Offset:    rec.Offset,
		Key:       cloneBytes(rec.Key),
		Value:     cloneBytes(rec.Value),
		Timestamp: rec.Timestamp,
		Headers:   cloneHeaders(rec.Headers),
	}

	if commit != nil {
		wr.setCommitFn(commit)
	}

	return wr
}
