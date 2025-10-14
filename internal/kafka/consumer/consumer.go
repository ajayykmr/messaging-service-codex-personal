package consumer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	"github.com/rs/zerolog"
)

const (
	defaultSessionTimeout   = 30 * time.Second
	defaultHeartbeat        = 3 * time.Second
	defaultRebalanceTimeout = 30 * time.Second
	defaultConsumeBackoff   = time.Second
)

// Handler is invoked for every record delivered by the consumer.
type Handler func(ctx context.Context, record *Record) error

// Option customises the consumer during construction.
type Option func(*options)

type options struct {
	config *sarama.Config
}

// WithConfig allows callers to supply a Sarama config. The configuration is
// cloned internally so the caller retains ownership.
func WithConfig(cfg *sarama.Config) Option {
	return func(o *options) {
		if cfg != nil {
			o.config = cfg
		}
	}
}

// Consumer wraps a Sarama consumer group, providing manual commit support and
// readiness tracking consistent with the detailed design specification.
type Consumer struct {
	logger zerolog.Logger

	group        sarama.ConsumerGroup
	groupID      string
	topics       []string
	handler      Handler
	commitOnAck  bool
	errorsDoneCh chan struct{}

	ready atomic.Bool

	mu sync.RWMutex

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Record represents a Kafka message delivered by the consumer.
type Record struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
	Timestamp time.Time
	Headers   map[string][]byte

	session sarama.ConsumerGroupSession
	message *sarama.ConsumerMessage

	mu        sync.Mutex
	committed bool
}

// New constructs a consumer for the supplied brokers and consumer group.
func New(brokers []string, groupID string, logger zerolog.Logger, commitOnSuccessOnly bool, opts ...Option) (*Consumer, error) {
	if len(brokers) == 0 {
		return nil, errors.New("kafka consumer: at least one broker is required")
	}
	if groupID == "" {
		return nil, errors.New("kafka consumer: group id is required")
	}

	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}

	settings := &options{
		config: defaultConfig(commitOnSuccessOnly),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(settings)
		}
	}

	cfg := cloneConfig(settings.config)
	cfg.Consumer.Offsets.AutoCommit.Enable = !commitOnSuccessOnly

	group, err := sarama.NewConsumerGroup(brokers, groupID, cfg)
	if err != nil {
		return nil, fmt.Errorf("kafka consumer: create consumer group: %w", err)
	}

	c := &Consumer{
		logger:       logger,
		group:        group,
		groupID:      groupID,
		commitOnAck:  commitOnSuccessOnly,
		errorsDoneCh: make(chan struct{}),
	}

	go c.consumeErrors()

	return c, nil
}

// Consume subscribes to the provided topics and invokes the supplied handler
// for each record. The call blocks until the provided context is cancelled or
// an unrecoverable error occurs.
func (c *Consumer) Consume(ctx context.Context, topics []string, handler Handler) error {
	if len(topics) == 0 {
		return errors.New("kafka consumer: at least one topic is required")
	}
	if handler == nil {
		return errors.New("kafka consumer: handler is required")
	}

	c.mu.Lock()
	c.topics = append([]string(nil), topics...)
	c.handler = handler
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	c.wg.Add(1)
	defer c.wg.Done()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := c.group.Consume(ctx, topics, &groupHandler{consumer: c})
		if err != nil {
			if errors.Is(err, sarama.ErrClosedConsumerGroup) {
				return nil
			}
			c.logger.Error().Err(err).Msg("kafka consumer: consume error")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(defaultConsumeBackoff):
			}
			continue
		}
	}
}

// Commit marks the record as processed. When commit-on-success is enabled the
// offset is flushed immediately; otherwise it is marked and relies on the
// configured auto-commit interval.
func (c *Consumer) Commit(_ context.Context, record *Record) error {
	if record == nil {
		return errors.New("kafka consumer: record is required")
	}
	if record.session == nil || record.message == nil {
		return errors.New("kafka consumer: record missing session data")
	}

	record.mu.Lock()
	if record.committed {
		record.mu.Unlock()
		return nil
	}
	record.committed = true
	record.mu.Unlock()

	record.session.MarkMessage(record.message, "")
	if c.commitOnAck {
		record.session.Commit()
	}
	return nil
}

// IsReady returns true once the consumer has joined the group and is actively
// consuming.
func (c *Consumer) IsReady() bool {
	return c.ready.Load()
}

// Close shuts down the consumer group and associated goroutines.
func (c *Consumer) Close() error {
	if c.cancel != nil {
		c.cancel()
	}
	err := c.group.Close()
	c.wg.Wait()
	<-c.errorsDoneCh
	return err
}

func (c *Consumer) consumeErrors() {
	defer close(c.errorsDoneCh)
	for err := range c.group.Errors() {
		if err != nil {
			c.logger.Error().Err(err).Msg("kafka consumer error")
		}
	}
}

type groupHandler struct {
	consumer *Consumer
}

func (h *groupHandler) Setup(session sarama.ConsumerGroupSession) error {
	h.consumer.ready.Store(true)
	h.consumer.logger.Info().
		Str("group_id", h.consumer.groupID).
		Msg("kafka consumer group ready")
	return nil
}

func (h *groupHandler) Cleanup(session sarama.ConsumerGroupSession) error {
	h.consumer.ready.Store(false)
	h.consumer.logger.Info().
		Str("group_id", h.consumer.groupID).
		Msg("kafka consumer group cleanup")
	return nil
}

func (h *groupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		record := &Record{
			Topic:     msg.Topic,
			Partition: msg.Partition,
			Offset:    msg.Offset,
			Key:       cloneBytes(msg.Key),
			Value:     cloneBytes(msg.Value),
			Timestamp: msg.Timestamp,
			Headers:   fromHeaders(msg.Headers),
			session:   session,
			message:   msg,
		}

		h.consumer.mu.RLock()
		handler := h.consumer.handler
		h.consumer.mu.RUnlock()

		if handler == nil {
			h.consumer.logger.Error().Msg("kafka consumer: message received without handler")
			continue
		}

		msgCtx := session.Context()
		if err := handler(msgCtx, record); err != nil {
			h.consumer.logger.Error().
				Err(err).
				Str("topic", msg.Topic).
				Int32("partition", msg.Partition).
				Int64("offset", msg.Offset).
				Msg("kafka consumer handler error")
		}
	}

	return nil
}

func defaultConfig(commitOnSuccessOnly bool) *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_5_0_0
	cfg.ClientID = "messaging-service-consumer"

	cfg.Consumer.Group.Session.Timeout = defaultSessionTimeout
	cfg.Consumer.Group.Heartbeat.Interval = defaultHeartbeat
	cfg.Consumer.Group.Rebalance.Timeout = defaultRebalanceTimeout
	cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRange
	cfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	cfg.Consumer.Offsets.AutoCommit.Enable = !commitOnSuccessOnly
	cfg.Consumer.Return.Errors = true

	return cfg
}

func cloneConfig(cfg *sarama.Config) *sarama.Config {
	if cfg == nil {
		return defaultConfig(false)
	}
	cloned := *cfg
	return &cloned
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func fromHeaders(headers []*sarama.RecordHeader) map[string][]byte {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string][]byte, len(headers))
	for _, h := range headers {
		if h == nil || len(h.Key) == 0 {
			continue
		}
		out[string(h.Key)] = cloneBytes(h.Value)
	}
	return out
}
