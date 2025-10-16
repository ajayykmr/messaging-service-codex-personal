package worker

import (
	"context"

	"github.com/ajayykmr/messaging-service-go/internal/kafka/consumer"
)

// KafkaHandler returns a consumer.Handler that transforms Kafka consumer
// records into worker records and delegates processing to the supplied engine.
func KafkaHandler(engine *Engine, cons *consumer.Consumer) consumer.Handler {
	return func(ctx context.Context, rec *consumer.Record) error {
		if engine == nil || rec == nil {
			return nil
		}

		commitFn := func(context.Context) error { return nil }
		if cons != nil {
			commitFn = func(c context.Context) error {
				return cons.Commit(c, rec)
			}
		}

		wr := NewRecordFromConsumer(rec, commitFn)
		engine.HandleRecord(ctx, wr)
		return nil
	}
}
