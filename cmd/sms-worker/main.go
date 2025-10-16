package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	smsadapter "github.com/ajayykmr/messaging-service-go/internal/adapters/sms"
	"github.com/ajayykmr/messaging-service-go/internal/config"
	"github.com/ajayykmr/messaging-service-go/internal/kafka/consumer"
	"github.com/ajayykmr/messaging-service-go/internal/kafka/producer"
	kafkapublisher "github.com/ajayykmr/messaging-service-go/internal/kafka/publisher"
	"github.com/ajayykmr/messaging-service-go/internal/logger"
	"github.com/ajayykmr/messaging-service-go/internal/models"
	"github.com/ajayykmr/messaging-service-go/internal/providers/factory"
	"github.com/ajayykmr/messaging-service-go/internal/worker"
	smsvalidator "github.com/ajayykmr/messaging-service-go/internal/worker/validator/sms"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		fail("config load", err)
	}

	baseLogger, err := logger.New(cfg.App.Env, cfg.App.LogLevel)
	if err != nil {
		fail("logger init", err)
	}
	log := baseLogger.With().Str("service", "sms-worker").Logger()

	kafkaLogger := log.With().Str("component", "kafka").Logger()
	prod, err := producer.New(cfg.Kafka.Brokers, kafkaLogger)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create kafka producer")
	}
	defer func() {
		if err := prod.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close kafka producer")
		}
	}()

	consumerLogger := log.With().Str("component", "consumer").Logger()
	cons, err := consumer.New(cfg.Kafka.Brokers, cfg.ConsumerGroups.SMS, consumerLogger, cfg.Retry.CommitOnSuccessOnly)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create kafka consumer")
	}
	defer func() {
		if err := cons.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close kafka consumer")
		}
	}()

	statusPublisher := kafkapublisher.NewStatusPublisher(prod, cfg.Topics.SMS.Status, log.With().Str("component", "status-publisher").Logger())
	if statusPublisher == nil {
		log.Fatal().Msg("failed to create status publisher")
	}
	dlqPublisher := kafkapublisher.NewDLQPublisher(prod, cfg.Topics.SMS.DLQ, log.With().Str("component", "dlq-publisher").Logger())
	if dlqPublisher == nil {
		log.Fatal().Msg("failed to create dlq publisher")
	}

	providerLogger := log.With().
		Str("component", "sms-provider").
		Str("backend", strings.ToLower(strings.TrimSpace(cfg.Providers.SMSProvider))).
		Logger()
	provider, err := factory.SMS(cfg.Providers, providerLogger)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise sms provider")
	}

	adapter, err := smsadapter.NewAdapter(provider, log.With().Str("component", "sms-adapter").Logger())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise sms adapter")
	}

	validator := smsvalidator.New(cfg.Validation, log.With().Str("component", "sms-validator").Logger())

	engineCfg := worker.Config{
		Channel:           models.ChannelSMS,
		MsgMaxBytes:       cfg.Validation.MsgMaxBytes,
		MaxAttempts:       cfg.Retry.MaxAttempts,
		BaseBackoff:       time.Duration(cfg.Retry.BaseBackoffSeconds) * time.Second,
		MaxBackoff:        time.Duration(cfg.Retry.MaxBackoffSeconds) * time.Second,
		WorkerConcurrency: cfg.Retry.WorkerConcurrency,
	}

	engine, err := worker.NewEngine(engineCfg, worker.Dependencies{
		Adapter:         adapter,
		Validator:       validator,
		StatusPublisher: statusPublisher,
		DLQPublisher:    dlqPublisher,
		Committer: worker.CommitFunc(func(ctx context.Context, record *worker.Record) error {
			return record.Commit(ctx)
		}),
		Logger: log.With().Str("component", "worker-engine").Logger(),
		Now:    time.Now,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise worker engine")
	}

	topics := []string{cfg.Topics.SMS.Request}
	handler := worker.KafkaHandler(engine, cons)

	errCh := make(chan error, 1)
	go func() {
		if err := cons.Consume(ctx, topics, handler); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
		close(errCh)
	}()

	log.Info().Str("request_topic", cfg.Topics.SMS.Request).Msg("sms worker started")

	select {
	case <-ctx.Done():
		log.Info().Msg("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			log.Error().Err(err).Msg("consumer terminated with error")
		}
	}
}

func fail(stage string, err error) {
	logger := zerolog.New(os.Stdout).With().Timestamp().Logger()
	logger.Fatal().Err(err).Str("stage", stage).Msg("sms worker init failed")
}
