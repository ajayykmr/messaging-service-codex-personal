package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/semaphore"

	common "github.com/example/messaging-microservice/internal/adapters/common"
)

// Config contains the runtime settings the worker engine relies on to
// orchestrate processing, retries, and DLQ handling for a channel.
type Config struct {
	Channel           string
	MsgMaxBytes       int
	MaxAttempts       int
	BaseBackoff       time.Duration
	MaxBackoff        time.Duration
	WorkerConcurrency int
}

// Record represents a Kafka message delivered to the worker. It is a minimal
// abstraction that keeps the engine decoupled from the concrete consumer
// implementation while still exposing the data the engine requires.
type Record struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
	Timestamp time.Time
	Headers   map[string][]byte
}

// Clone returns a deep copy of the record so it can be safely shared with
// asynchronous goroutines without risking data races.
func (r *Record) Clone() *Record {
	if r == nil {
		return nil
	}

	clone := *r
	clone.Key = cloneBytes(r.Key)
	clone.Value = cloneBytes(r.Value)
	if len(r.Headers) > 0 {
		clone.Headers = cloneHeaders(r.Headers)
	}

	return &clone
}

// ValidatedMessage captures the canonical representation of a request after it
// has passed validation. The adapter receives this structure when sending to an
// upstream provider, and the publishers use it to enrich status/DLQ events.
type ValidatedMessage struct {
	Channel      string
	MessageID    string
	TraceID      string
	TenantID     string
	CreatedAt    time.Time
	Metadata     map[string]any
	Request      any
	RawPayload   []byte
	Key          []byte
	KafkaHeaders map[string][]byte
}

// StatusEvent describes a lifecycle update that should be emitted for a
// message. Each event references the attempt count along with provider
// metadata or error information when available.
type StatusEvent struct {
	Type             string
	Attempt          int
	ProviderResponse *common.ProviderResponse
	Error            string
	Duration         time.Duration
	Timestamp        time.Time
}

// FailureType enumerates the DLQ failure classifications supported by the
// engine.
type FailureType string

const (
	// FailureTypePermanent is used when the provider signalled a permanent
	// failure for the request.
	FailureTypePermanent FailureType = "permanent"
	// FailureTypeTransient captures situations where transient errors exhausted
	// the configured retry budget.
	FailureTypeTransient FailureType = "transient"
	// FailureTypeValidation is emitted for validation and size failures.
	FailureTypeValidation FailureType = "validation"
	// FailureTypeUnknown is a safety net for unclassified errors.
	FailureTypeUnknown FailureType = "unknown"
)

// DLQPayload contains the metadata required to construct a DLQ entry for a
// failed message.
type DLQPayload struct {
	FailureType   FailureType
	Attempts      int
	LastError     string
	FirstFailedAt time.Time
	LastAttemptAt time.Time
}

// Adapter defines the behaviour required from channel adapters. Adapters are
// responsible for converting the domain model into provider specific payloads
// and returning a normalized ProviderResponse alongside error classification.
type Adapter interface {
	Send(ctx context.Context, msg *ValidatedMessage) (*common.ProviderResponse, error)
}

// Validator parses and validates inbound Kafka records for a channel. It should
// return a ValidatedMessage describing the request. When a validation error is
// encountered the returned message may be nil or partially populated.
type Validator interface {
	ParseAndValidate(ctx context.Context, channel string, payload []byte) (*ValidatedMessage, error)
}

// StatusPublisher publishes lifecycle updates for a message (queued, attempt,
// sent, failed, etc.). Implementations are expected to marshal the supplied
// event into the canonical StatusEvent model defined in the design document.
type StatusPublisher interface {
	PublishStatus(ctx context.Context, msg *ValidatedMessage, event StatusEvent) error
}

// DLQPublisher writes failed messages to the channel DLQ topic.
type DLQPublisher interface {
	PublishDLQ(ctx context.Context, msg *ValidatedMessage, payload DLQPayload) error
}

// Committer is the abstraction for committing Kafka offsets after processing.
type Committer interface {
	Commit(ctx context.Context, record *Record) error
}

// Dependencies collects the runtime collaborators required by the engine.
type Dependencies struct {
	Adapter         Adapter
	Validator       Validator
	StatusPublisher StatusPublisher
	DLQPublisher    DLQPublisher
	Committer       Committer
	Logger          zerolog.Logger
	Now             func() time.Time
}

// Engine orchestrates validation, retries, backoff, DLQ handling and offset
// commits for inbound Kafka records in accordance with the detailed design.
type Engine struct {
	cfg             Config
	adapter         Adapter
	validator       Validator
	statusPublisher StatusPublisher
	dlqPublisher    DLQPublisher
	committer       Committer
	logger          zerolog.Logger

	semaphore *semaphore.Weighted

	now func() time.Time

	randMu sync.Mutex
	rnd    *rand.Rand
}

// NewEngine constructs a worker engine using the supplied configuration and
// collaborators. The configuration and dependencies are validated to prevent
// misconfiguration at startup.
func NewEngine(cfg Config, deps Dependencies) (*Engine, error) {
	if cfg.Channel == "" {
		return nil, errors.New("worker: channel must be provided")
	}
	if cfg.MaxAttempts < 1 {
		return nil, errors.New("worker: max attempts must be >= 1")
	}
	if cfg.WorkerConcurrency < 1 {
		return nil, errors.New("worker: worker concurrency must be >= 1")
	}
	if cfg.MsgMaxBytes < 0 {
		return nil, errors.New("worker: msg max bytes cannot be negative")
	}
	if deps.Adapter == nil {
		return nil, errors.New("worker: adapter dependency is required")
	}
	if deps.Validator == nil {
		return nil, errors.New("worker: validator dependency is required")
	}
	if deps.StatusPublisher == nil {
		return nil, errors.New("worker: status publisher dependency is required")
	}
	if deps.DLQPublisher == nil {
		return nil, errors.New("worker: DLQ publisher dependency is required")
	}
	if deps.Committer == nil {
		return nil, errors.New("worker: committer dependency is required")
	}

	logger := deps.Logger
	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}
	logger = logger.With().Str("component", "worker_engine").Logger()

	nowFunc := deps.Now
	if nowFunc == nil {
		nowFunc = time.Now
	}

	eng := &Engine{
		cfg:             cfg,
		adapter:         deps.Adapter,
		validator:       deps.Validator,
		statusPublisher: deps.StatusPublisher,
		dlqPublisher:    deps.DLQPublisher,
		committer:       deps.Committer,
		logger:          logger,
		semaphore:       semaphore.NewWeighted(int64(cfg.WorkerConcurrency)),
		now:             nowFunc,
		rnd:             rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	return eng, nil
}

// HandleRecord performs upfront validation for record size, parses the payload
// and triggers asynchronous processing with retry handling.
func (e *Engine) HandleRecord(ctx context.Context, record *Record) {
	if record == nil {
		return
	}

	if e.cfg.MsgMaxBytes > 0 && len(record.Value) > e.cfg.MsgMaxBytes {
		err := fmt.Errorf("payload exceeds maximum size: got %d bytes, limit %d bytes", len(record.Value), e.cfg.MsgMaxBytes)
		msg := e.partialMessageFromRecord(record)
		e.logger.Warn().
			Str("channel", msg.Channel).
			Str("message_id", msg.MessageID).
			Err(err).
			Msg("worker: record discarded because it exceeds configured size limit")
		now := e.now()
		e.publishStatus(ctx, msg, StatusEvent{Type: "failed", Attempt: 0, Error: err.Error(), Timestamp: now})
		e.publishDLQ(ctx, msg, DLQPayload{FailureType: FailureTypeValidation, Attempts: 0, LastError: err.Error(), FirstFailedAt: now, LastAttemptAt: now})
		e.commitRecord(ctx, record)
		return
	}

	validated, err := e.validator.ParseAndValidate(ctx, e.cfg.Channel, record.Value)
	if err != nil {
		if validated == nil {
			validated = e.partialMessageFromRecord(record)
		}
		if validated.Channel == "" {
			validated.Channel = e.cfg.Channel
		}
		if validated.MessageID == "" {
			validated.MessageID = string(record.Key)
		}
		if len(validated.RawPayload) == 0 {
			validated.RawPayload = cloneBytes(record.Value)
		}
		if len(validated.Key) == 0 {
			validated.Key = cloneBytes(record.Key)
		}
		now := e.now()
		e.logger.Warn().
			Str("channel", validated.Channel).
			Str("message_id", validated.MessageID).
			Err(err).
			Msg("worker: validation failed for record")
		e.publishStatus(ctx, validated, StatusEvent{Type: "failed", Attempt: 0, Error: err.Error(), Timestamp: now})
		e.publishDLQ(ctx, validated, DLQPayload{FailureType: FailureTypeValidation, Attempts: 0, LastError: err.Error(), FirstFailedAt: now, LastAttemptAt: now})
		e.commitRecord(ctx, record)
		return
	}

	if validated.Channel == "" {
		validated.Channel = e.cfg.Channel
	}
	if validated.MessageID == "" {
		validated.MessageID = string(record.Key)
	}
	if len(validated.RawPayload) == 0 {
		validated.RawPayload = cloneBytes(record.Value)
	}
	if len(validated.Key) == 0 {
		validated.Key = cloneBytes(record.Key)
	}
	if len(validated.KafkaHeaders) == 0 && len(record.Headers) > 0 {
		validated.KafkaHeaders = cloneHeaders(record.Headers)
	}

	if err := e.semaphore.Acquire(ctx, 1); err != nil {
		e.logger.Error().
			Str("channel", validated.Channel).
			Str("message_id", validated.MessageID).
			Err(err).
			Msg("worker: failed to acquire concurrency semaphore")
		return
	}

	recCopy := record.Clone()

	go e.processRecord(ctx, recCopy, validated)
}

func (e *Engine) processRecord(ctx context.Context, record *Record, msg *ValidatedMessage) {
	defer e.semaphore.Release(1)

	if ctx.Err() != nil {
		e.logger.Warn().
			Str("channel", msg.Channel).
			Str("message_id", msg.MessageID).
			Msg("worker: context cancelled before processing began")
		return
	}

	e.publishStatus(ctx, msg, StatusEvent{Type: "queued", Attempt: 0})

	attempt := 1
	firstFailedAt := time.Time{}

	for {
		e.publishStatus(ctx, msg, StatusEvent{Type: "attempt", Attempt: attempt})
		start := e.now()
		providerResp, err := e.adapter.Send(ctx, msg)
		duration := e.now().Sub(start)

		logEvent := e.logger.With().
			Str("channel", msg.Channel).
			Str("message_id", msg.MessageID).
			Int("attempt", attempt).
			Dur("duration", duration).
			Logger()

		if err == nil {
			logEvent.Info().Msg("worker: message sent successfully")
			e.publishStatus(ctx, msg, StatusEvent{Type: "sent", Attempt: attempt, ProviderResponse: providerResp, Duration: duration})
			e.commitRecord(ctx, record)
			return
		}

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			logEvent.Warn().Err(err).Msg("worker: context cancelled during send; deferring commit for reprocessing")
			return
		}

		logEvent.Warn().Err(err).Msg("worker: adapter returned error")

		now := e.now()
		if firstFailedAt.IsZero() {
			firstFailedAt = now
		}

		if errors.Is(err, common.ErrPermanent) {
			e.publishStatus(ctx, msg, StatusEvent{Type: "failed", Attempt: attempt, ProviderResponse: providerResp, Error: err.Error(), Duration: duration, Timestamp: now})
			e.publishDLQ(ctx, msg, DLQPayload{FailureType: FailureTypePermanent, Attempts: attempt, LastError: err.Error(), FirstFailedAt: firstFailedAt, LastAttemptAt: now})
			e.commitRecord(ctx, record)
			return
		}

		if attempt >= e.cfg.MaxAttempts {
			e.publishStatus(ctx, msg, StatusEvent{Type: "failed", Attempt: attempt, ProviderResponse: providerResp, Error: err.Error(), Duration: duration, Timestamp: now})
			failureType := FailureTypeTransient
			if !errors.Is(err, common.ErrTransient) {
				failureType = FailureTypeUnknown
			}
			e.publishDLQ(ctx, msg, DLQPayload{FailureType: failureType, Attempts: attempt, LastError: err.Error(), FirstFailedAt: firstFailedAt, LastAttemptAt: now})
			e.commitRecord(ctx, record)
			return
		}

		backoff := e.computeBackoff(attempt)
		if backoff > 0 {
			logEvent.Info().Dur("backoff", backoff).Msg("worker: scheduling retry after transient error")
		}

		if !e.wait(ctx, backoff) {
			e.logger.Warn().
				Str("channel", msg.Channel).
				Str("message_id", msg.MessageID).
				Int("attempt", attempt).
				Msg("worker: context cancelled while waiting for retry; message will be retried on next poll")
			return
		}

		attempt++
	}
}

func (e *Engine) computeBackoff(attempt int) time.Duration {
	if e.cfg.BaseBackoff <= 0 {
		return 0
	}

	multiplier := math.Pow(2, float64(attempt-1))
	raw := time.Duration(float64(e.cfg.BaseBackoff) * multiplier)
	if e.cfg.MaxBackoff > 0 && raw > e.cfg.MaxBackoff {
		raw = e.cfg.MaxBackoff
	}

	return e.fullJitter(raw)
}

func (e *Engine) fullJitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}

	e.randMu.Lock()
	defer e.randMu.Unlock()

	n := e.rnd.Int63n(int64(max) + 1)
	return time.Duration(n)
}

func (e *Engine) wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (e *Engine) publishStatus(ctx context.Context, msg *ValidatedMessage, event StatusEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = e.now()
	}
	if e.statusPublisher == nil || msg == nil {
		return
	}
	if err := e.statusPublisher.PublishStatus(ctx, msg, event); err != nil {
		e.logger.Error().
			Str("channel", msg.Channel).
			Str("message_id", msg.MessageID).
			Str("event", event.Type).
			Err(err).
			Msg("worker: failed to publish status event")
	}
}

func (e *Engine) publishDLQ(ctx context.Context, msg *ValidatedMessage, payload DLQPayload) {
	if payload.FirstFailedAt.IsZero() {
		payload.FirstFailedAt = e.now()
	}
	if payload.LastAttemptAt.IsZero() {
		payload.LastAttemptAt = payload.FirstFailedAt
	}
	if e.dlqPublisher == nil || msg == nil {
		return
	}
	if err := e.dlqPublisher.PublishDLQ(ctx, msg, payload); err != nil {
		e.logger.Error().
			Str("channel", msg.Channel).
			Str("message_id", msg.MessageID).
			Err(err).
			Msg("worker: failed to publish DLQ record")
	}
}

func (e *Engine) commitRecord(ctx context.Context, record *Record) {
	if record == nil {
		return
	}
	if err := e.committer.Commit(ctx, record); err != nil {
		e.logger.Error().
			Str("topic", record.Topic).
			Int32("partition", record.Partition).
			Int64("offset", record.Offset).
			Err(err).
			Msg("worker: failed to commit record offset")
	}
}

func (e *Engine) partialMessageFromRecord(record *Record) *ValidatedMessage {
	msg := &ValidatedMessage{
		Channel:      e.cfg.Channel,
		MessageID:    string(record.Key),
		RawPayload:   cloneBytes(record.Value),
		Key:          cloneBytes(record.Key),
		KafkaHeaders: cloneHeaders(record.Headers),
	}
	return msg
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	clone := make([]byte, len(b))
	copy(clone, b)
	return clone
}

func cloneHeaders(headers map[string][]byte) map[string][]byte {
	if len(headers) == 0 {
		return nil
	}
	clone := make(map[string][]byte, len(headers))
	for k, v := range headers {
		clone[k] = cloneBytes(v)
	}
	return clone
}
