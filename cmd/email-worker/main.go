package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	emailadapter "github.com/example/messaging-microservice/internal/adapters/email"
	"github.com/example/messaging-microservice/internal/config"
	"github.com/example/messaging-microservice/internal/kafka/consumer"
	"github.com/example/messaging-microservice/internal/kafka/producer"
	kafkapublisher "github.com/example/messaging-microservice/internal/kafka/publisher"
	"github.com/example/messaging-microservice/internal/logger"
	"github.com/example/messaging-microservice/internal/models"
	emailprovider "github.com/example/messaging-microservice/internal/providers/email"
	"github.com/example/messaging-microservice/internal/worker"
	emailvalidator "github.com/example/messaging-microservice/internal/worker/validator/email"
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
	log := baseLogger.With().Str("service", "email-worker").Logger()

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
	cons, err := consumer.New(cfg.Kafka.Brokers, cfg.ConsumerGroups.Email, consumerLogger, cfg.Retry.CommitOnSuccessOnly)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create kafka consumer")
	}
	defer func() {
		if err := cons.Close(); err != nil {
			log.Error().Err(err).Msg("failed to close kafka consumer")
		}
	}()

	statusPublisher := kafkapublisher.NewStatusPublisher(prod, cfg.Topics.Email.Status, log.With().Str("component", "status-publisher").Logger())
	if statusPublisher == nil {
		log.Fatal().Msg("failed to create status publisher")
	}
	dlqPublisher := kafkapublisher.NewDLQPublisher(prod, cfg.Topics.Email.DLQ, log.With().Str("component", "dlq-publisher").Logger())
	if dlqPublisher == nil {
		log.Fatal().Msg("failed to create dlq publisher")
	}

	provider, err := emailprovider.NewSMTPProvider(cfg.Providers.SMTP, log.With().Str("component", "smtp-provider").Logger())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise smtp provider")
	}

	adapter, err := emailadapter.NewAdapter(provider, log.With().Str("component", "email-adapter").Logger())
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise email adapter")
	}

	validator := emailvalidator.New(cfg.Validation, log.With().Str("component", "email-validator").Logger())

	engineCfg := worker.Config{
		Channel:           models.ChannelEmail,
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

	topics := []string{cfg.Topics.Email.Request}
	handler := worker.KafkaHandler(engine, cons)

	errCh := make(chan error, 1)
	go func() {
		if err := cons.Consume(ctx, topics, handler); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- err
		}
		close(errCh)
	}()

	log.Info().Str("request_topic", cfg.Topics.Email.Request).Msg("email worker started")

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
	logger.Fatal().Err(err).Str("stage", stage).Msg("email worker init failed")
}
