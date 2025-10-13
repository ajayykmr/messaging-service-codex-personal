package main

import (
	"context"
	"os"
	"time"

	"github.com/rs/zerolog"

	emailadapter "github.com/example/messaging-microservice/internal/adapters/email"
	"github.com/example/messaging-microservice/internal/config"
	"github.com/example/messaging-microservice/internal/models"
	emailprovider "github.com/example/messaging-microservice/internal/providers/email"
	"github.com/example/messaging-microservice/internal/worker"
)

func main() {
	logger := zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout}).With().Timestamp().Logger()

	requiredEnv := map[string]string{
		"KAFKA_BROKERS":                "localhost:9092",
		"KAFKA_EMAIL_REQUEST_TOPIC":    "email.request",
		"KAFKA_EMAIL_STATUS_TOPIC":     "email.status",
		"KAFKA_EMAIL_DLQ_TOPIC":        "email.dlq",
		"KAFKA_SMS_REQUEST_TOPIC":      "sms.request",
		"KAFKA_SMS_STATUS_TOPIC":       "sms.status",
		"KAFKA_SMS_DLQ_TOPIC":          "sms.dlq",
		"KAFKA_WHATSAPP_REQUEST_TOPIC": "wa.request",
		"KAFKA_WHATSAPP_STATUS_TOPIC":  "wa.status",
		"KAFKA_WHATSAPP_DLQ_TOPIC":     "wa.dlq",
		"EMAIL_CONSUMER_GROUP":         "email-consumer",
		"SMS_CONSUMER_GROUP":           "sms-consumer",
		"WHATSAPP_CONSUMER_GROUP":      "wa-consumer",
		"SMTP_HOST":                    "smtp.gmail.com",
		"SMTP_PORT":                    "587",
		// "SMTP_USER":                    "perfectkeshavraj@gmail.com",
		// "SMTP_PASS":                    "udzwbybicoahejsb",
		"SMTP_USER":                "ajay@quantiqueminds.com",
		"SMTP_PASS":                "vsyvxoqcvabwgkte",
		"SMTP_FROM":                "noreply@quantiqueminds.com",
		"TWILIO_ACCOUNT_SID":       "test_sid",
		"TWILIO_AUTH_TOKEN":        "test_token",
		"TWILIO_PHONE_NUMBER":      "+10000000000",
		"PROVIDER_TIMEOUT_SECONDS": "10",
	}

	for key, value := range requiredEnv {
		if err := os.Setenv(key, value); err != nil {
			logger.Fatal().Err(err).Str("key", key).Msg("failed to set env value")
		}
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to load config")
	}

	// provider := emailprovider.NewMockProvider(logger)
	provider, err := emailprovider.NewSMTPProvider(cfg.Providers.SMTP, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialise smtp provider")
	}

	adapter, err := emailadapter.NewAdapter(provider, logger)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to initialise email adapter")
	}

	timeout := time.Duration(cfg.Timeouts.ProviderTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	now := time.Now().UTC()
	request := &models.EmailRequest{
		Envelope: models.Envelope{
			MessageID: "adapter-provider-test",
			Channel:   models.ChannelEmail,
			CreatedAt: now,
		},
		From:    cfg.Providers.SMTP.From,
		To:      []string{"ajay@quantiqueminds.com"},
		Subject: "Adapter provider smoke test",
		Body: models.MessageBody{
			Type:    models.BodyTypeText,
			Content: "Hello from the adapter/provider integration check.",
		},
	}

	validated := &worker.ValidatedMessage{
		Channel:   models.ChannelEmail,
		MessageID: request.MessageID,
		CreatedAt: now,
		Request:   request,
	}

	response, err := adapter.Send(ctx, validated)
	if err != nil {
		logger.Fatal().Err(err).Msg("adapter failed to send message")
	}
	if response == nil || response.Status != "ok" {
		logger.Fatal().Interface("response", response).Msg("adapter returned unexpected response")
	}

	providerID := ""
	if response.Meta != nil {
		providerID = response.Meta["provider_id"]
	}

	logger.Info().
		Str("message_id", request.MessageID).
		Str("provider_id", providerID).
		Str("status", response.Status).
		Msg("adapter and provider working as expected")
}
