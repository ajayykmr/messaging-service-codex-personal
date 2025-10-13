package producer

import (
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
	defaultMetadataRefreshInterval = 30 * time.Second
)

// Option customises the producer during construction.
type Option func(*options)

type options struct {
	config          *sarama.Config
	refreshInterval time.Duration
}

// WithConfig allows callers to supply a preconfigured Sarama config. The
// configuration is cloned internally so the caller retains ownership.
func WithConfig(cfg *sarama.Config) Option {
	return func(o *options) {
		if cfg != nil {
			o.config = cfg
		}
	}
}

// WithMetadataRefreshInterval overrides the interval used when refreshing
// cluster metadata to keep readiness information current.
func WithMetadataRefreshInterval(interval time.Duration) Option {
	return func(o *options) {
		if interval > 0 {
			o.refreshInterval = interval
		}
	}
}

// Producer wraps a Sarama sync and async producer pair, exposing convenience
// helpers that align with the detailed design while tracking readiness based on
// periodic metadata refreshes.
type Producer struct {
	logger zerolog.Logger

	client        sarama.Client
	syncProducer  sarama.SyncProducer
	asyncProducer sarama.AsyncProducer

	refreshInterval time.Duration

	ready atomic.Bool

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// New constructs a Producer using the supplied broker list and logger.
func New(brokers []string, logger zerolog.Logger, opts ...Option) (*Producer, error) {
	if len(brokers) == 0 {
		return nil, errors.New("kafka producer: at least one broker is required")
	}

	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}

	settings := &options{
		config:          defaultConfig(),
		refreshInterval: defaultMetadataRefreshInterval,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(settings)
		}
	}

	cfg := cloneConfig(settings.config)
	if settings.refreshInterval > 0 {
		cfg.Metadata.RefreshFrequency = settings.refreshInterval
	}

	client, err := sarama.NewClient(brokers, cfg)
	if err != nil {
		return nil, fmt.Errorf("kafka producer: create client: %w", err)
	}

	syncProd, err := sarama.NewSyncProducerFromClient(client)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("kafka producer: create sync producer: %w", err)
	}

	asyncProd, err := sarama.NewAsyncProducerFromClient(client)
	if err != nil {
		syncProd.Close()
		client.Close()
		return nil, fmt.Errorf("kafka producer: create async producer: %w", err)
	}

	p := &Producer{
		logger:          logger,
		client:          client,
		syncProducer:    syncProd,
		asyncProducer:   asyncProd,
		refreshInterval: settings.refreshInterval,
		stopCh:          make(chan struct{}),
	}

	if err := p.refreshMetadata(); err != nil {
		logger.Error().Err(err).Msg("kafka producer initial metadata refresh failed")
	} else {
		p.ready.Store(true)
	}

	p.wg.Add(2)
	go p.watchMetadata()
	go p.consumeAsyncErrors()

	return p, nil
}

// PublishSync publishes a message and waits for the Kafka broker to acknowledge
// receipt. Required acks default to WaitForAll due to the default config.
func (p *Producer) PublishSync(topic string, key []byte, headers map[string][]byte, payload []byte) error {
	if topic == "" {
		return errors.New("kafka producer: topic is required")
	}

	msg := &sarama.ProducerMessage{
		Topic:   topic,
		Value:   sarama.ByteEncoder(payload),
		Headers: toRecordHeaders(headers),
	}
	if len(key) > 0 {
		msg.Key = sarama.ByteEncoder(key)
	}

	_, _, err := p.syncProducer.SendMessage(msg)
	if err != nil {
		p.ready.Store(false)
		return fmt.Errorf("kafka producer: send sync: %w", err)
	}

	p.ready.Store(true)
	return nil
}

// PublishAsync enqueues a message on the async producer channel. Errors are
// surfaced via the producer error channel and logged.
func (p *Producer) PublishAsync(topic string, key []byte, headers map[string][]byte, payload []byte) error {
	if topic == "" {
		return errors.New("kafka producer: topic is required")
	}

	msg := &sarama.ProducerMessage{
		Topic:   topic,
		Value:   sarama.ByteEncoder(payload),
		Headers: toRecordHeaders(headers),
	}
	if len(key) > 0 {
		msg.Key = sarama.ByteEncoder(key)
	}

	select {
	case p.asyncProducer.Input() <- msg:
		return nil
	default:
		return errors.New("kafka producer: async input buffer full")
	}
}

// IsReady indicates whether the producer has successfully refreshed metadata
// recently.
func (p *Producer) IsReady() bool {
	return p.ready.Load()
}

// Close releases the underlying Sarama producers and stops background goroutines.
func (p *Producer) Close() error {
	close(p.stopCh)
	p.wg.Wait()

	var errs []error
	if err := p.asyncProducer.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.syncProducer.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.client.Close(); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

func (p *Producer) watchMetadata() {
	defer p.wg.Done()

	ticker := time.NewTicker(p.refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			if err := p.refreshMetadata(); err != nil {
				p.logger.Error().Err(err).Msg("kafka producer metadata refresh failed")
				p.ready.Store(false)
			} else {
				p.ready.Store(true)
			}
		}
	}
}

func (p *Producer) refreshMetadata() error {
	return p.client.RefreshMetadata()
}

func (p *Producer) consumeAsyncErrors() {
	defer p.wg.Done()

	for {
		select {
		case <-p.stopCh:
			return
		case err, ok := <-p.asyncProducer.Errors():
			if !ok {
				return
			}
			p.ready.Store(false)
			if err != nil {
				p.logger.Error().
					Err(err.Err).
					Str("topic", err.Msg.Topic).
					Msg("kafka producer async error")
			}
		}
	}
}

func toRecordHeaders(headers map[string][]byte) []sarama.RecordHeader {
	if len(headers) == 0 {
		return nil
	}
	out := make([]sarama.RecordHeader, 0, len(headers))
	for k, v := range headers {
		out = append(out, sarama.RecordHeader{
			Key:   []byte(k),
			Value: cloneBytes(v),
		})
	}
	return out
}

func cloneBytes(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func defaultConfig() *sarama.Config {
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_5_0_0
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.Producer.Retry.Max = 6
	cfg.Producer.Retry.Backoff = 250 * time.Millisecond
	cfg.Producer.Return.Errors = true
	cfg.Producer.Return.Successes = true
	cfg.Producer.Idempotent = true
	cfg.Net.MaxOpenRequests = 1
	cfg.Metadata.Full = true
	cfg.Metadata.RefreshFrequency = defaultMetadataRefreshInterval
	cfg.Producer.Flush.Bytes = 0
	cfg.Producer.Flush.Messages = 0
	cfg.Producer.Flush.Frequency = 0
	return cfg
}

func cloneConfig(cfg *sarama.Config) *sarama.Config {
	if cfg == nil {
		return defaultConfig()
	}
	cloned := *cfg
	return &cloned
}
