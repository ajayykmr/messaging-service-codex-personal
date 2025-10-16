package config_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ajayykmr/messaging-service-go/internal/config"
)

func setCommonRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("KAFKA_BROKERS", "broker-a:9092")
	t.Setenv("KAFKA_EMAIL_REQUEST_TOPIC", "email.request")
	t.Setenv("KAFKA_EMAIL_STATUS_TOPIC", "email.status")
	t.Setenv("KAFKA_EMAIL_DLQ_TOPIC", "email.dlq")
	t.Setenv("KAFKA_SMS_REQUEST_TOPIC", "sms.request")
	t.Setenv("KAFKA_SMS_STATUS_TOPIC", "sms.status")
	t.Setenv("KAFKA_SMS_DLQ_TOPIC", "sms.dlq")
	t.Setenv("KAFKA_WHATSAPP_REQUEST_TOPIC", "wa.request")
	t.Setenv("KAFKA_WHATSAPP_STATUS_TOPIC", "wa.status")
	t.Setenv("KAFKA_WHATSAPP_DLQ_TOPIC", "wa.dlq")
	t.Setenv("EMAIL_CONSUMER_GROUP", "email-consumer")
	t.Setenv("SMS_CONSUMER_GROUP", "sms-consumer")
	t.Setenv("WHATSAPP_CONSUMER_GROUP", "wa-consumer")
}

func TestLoadSuccess(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("APP_PORT", "9000")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("KAFKA_BROKERS", "broker-a:9092, broker-b:9093")
	t.Setenv("KAFKA_EMAIL_REQUEST_TOPIC", "email.request")
	t.Setenv("KAFKA_EMAIL_STATUS_TOPIC", "email.status")
	t.Setenv("KAFKA_EMAIL_DLQ_TOPIC", "email.dlq")
	t.Setenv("KAFKA_SMS_REQUEST_TOPIC", "sms.request")
	t.Setenv("KAFKA_SMS_STATUS_TOPIC", "sms.status")
	t.Setenv("KAFKA_SMS_DLQ_TOPIC", "sms.dlq")
	t.Setenv("KAFKA_WHATSAPP_REQUEST_TOPIC", "wa.request")
	t.Setenv("KAFKA_WHATSAPP_STATUS_TOPIC", "wa.status")
	t.Setenv("KAFKA_WHATSAPP_DLQ_TOPIC", "wa.dlq")
	t.Setenv("EMAIL_CONSUMER_GROUP", "email-consumer")
	t.Setenv("SMS_CONSUMER_GROUP", "sms-consumer")
	t.Setenv("WHATSAPP_CONSUMER_GROUP", "wa-consumer")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_USER", "mailer@example.com")
	t.Setenv("SMTP_PASS", "topsecret")
	t.Setenv("SMTP_FROM", "noreply@example.com")
	t.Setenv("TWILIO_ACCOUNT_SID", "sid")
	t.Setenv("TWILIO_AUTH_TOKEN", "token")
	t.Setenv("TWILIO_PHONE_NUMBER", "+1234567890")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantBrokers := []string{"broker-a:9092", "broker-b:9093"}
	if !reflect.DeepEqual(cfg.Kafka.Brokers, wantBrokers) {
		t.Fatalf("expected brokers %v, got %v", wantBrokers, cfg.Kafka.Brokers)
	}

	if cfg.App.Env != "production" {
		t.Fatalf("expected app env production, got %s", cfg.App.Env)
	}
	if cfg.App.Port != 9000 {
		t.Fatalf("expected app port 9000, got %d", cfg.App.Port)
	}
	if cfg.App.LogLevel != "warn" {
		t.Fatalf("expected log level warn, got %s", cfg.App.LogLevel)
	}
	if cfg.Providers.EmailProvider != "mock" {
		t.Fatalf("expected email provider mock, got %s", cfg.Providers.EmailProvider)
	}
	if cfg.Providers.SMSProvider != "mock" {
		t.Fatalf("expected sms provider mock, got %s", cfg.Providers.SMSProvider)
	}
	if cfg.Providers.WhatsAppProvider != "mock" {
		t.Fatalf("expected whatsapp provider mock, got %s", cfg.Providers.WhatsAppProvider)
	}
	if cfg.Providers.SMTP.From != "noreply@example.com" {
		t.Fatalf("expected smtp from noreply@example.com, got %s", cfg.Providers.SMTP.From)
	}
	if cfg.Timeouts.ProviderTimeoutSeconds != 30 {
		t.Fatalf("expected default provider timeout 30, got %d", cfg.Timeouts.ProviderTimeoutSeconds)
	}
}

func TestLoadMockProviders(t *testing.T) {
	t.Setenv("EMAIL_PROVIDER", "mock")
	t.Setenv("SMS_PROVIDER", "mock")
	t.Setenv("WHATSAPP_PROVIDER", "mock")
	setCommonRequiredEnv(t)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error loading mock providers: %v", err)
	}

	if cfg.Providers.EmailProvider != "mock" {
		t.Fatalf("expected email provider mock, got %s", cfg.Providers.EmailProvider)
	}
	if cfg.Providers.SMSProvider != "mock" {
		t.Fatalf("expected sms provider mock, got %s", cfg.Providers.SMSProvider)
	}
	if cfg.Providers.WhatsAppProvider != "mock" {
		t.Fatalf("expected whatsapp provider mock, got %s", cfg.Providers.WhatsAppProvider)
	}
	if cfg.Providers.SMTP.Host != "" {
		t.Fatalf("expected smtp host empty when using mock, got %s", cfg.Providers.SMTP.Host)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	required := map[string]string{
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
		"SMTP_HOST":                    "smtp.example.com",
		"SMTP_PORT":                    "587",
		"SMTP_USER":                    "mailer@example.com",
		"SMTP_PASS":                    "secret",
		"SMTP_FROM":                    "noreply@example.com",
		"TWILIO_ACCOUNT_SID":           "sid",
		"TWILIO_AUTH_TOKEN":            "token",
		"TWILIO_PHONE_NUMBER":          "+1234567890",
	}
	for key, value := range required {
		t.Setenv(key, value)
	}

	_, err := config.Load()
	if err == nil {
		t.Fatalf("expected error when KAFKA_BROKERS is missing")
	}
	if !strings.Contains(err.Error(), "KAFKA_BROKERS is required") {
		t.Fatalf("expected error message to mention missing brokers, got %q", err.Error())
	}
}

func TestLoadSMSProviderTwilioRequiresCredentials(t *testing.T) {
	t.Setenv("SMS_PROVIDER", "twilio")
	setCommonRequiredEnv(t)

	_, err := config.Load()
	if err == nil {
		t.Fatalf("expected error when twilio credentials missing")
	}

	msg := err.Error()
	if !strings.Contains(msg, "TWILIO_ACCOUNT_SID is required") {
		t.Fatalf("expected error about missing TWILIO_ACCOUNT_SID, got %q", msg)
	}
	if !strings.Contains(msg, "TWILIO_AUTH_TOKEN is required") {
		t.Fatalf("expected error about missing TWILIO_AUTH_TOKEN, got %q", msg)
	}
	if !strings.Contains(msg, "TWILIO_PHONE_NUMBER is required") {
		t.Fatalf("expected error about missing TWILIO_PHONE_NUMBER, got %q", msg)
	}
}

func TestLoadWhatsAppProviderTwilioRequiresCredentials(t *testing.T) {
	t.Setenv("WHATSAPP_PROVIDER", "twilio")
	setCommonRequiredEnv(t)

	_, err := config.Load()
	if err == nil {
		t.Fatalf("expected error when twilio credentials missing for whatsapp")
	}

	msg := err.Error()
	if !strings.Contains(msg, "TWILIO_ACCOUNT_SID is required") {
		t.Fatalf("expected error about missing TWILIO_ACCOUNT_SID, got %q", msg)
	}
	if !strings.Contains(msg, "TWILIO_AUTH_TOKEN is required") {
		t.Fatalf("expected error about missing TWILIO_AUTH_TOKEN, got %q", msg)
	}
	if !strings.Contains(msg, "TWILIO_PHONE_NUMBER is required") {
		t.Fatalf("expected error about missing TWILIO_PHONE_NUMBER, got %q", msg)
	}
}

func TestLoadInvalidSMSProvider(t *testing.T) {
	setCommonRequiredEnv(t)
	t.Setenv("SMS_PROVIDER", "invalid")

	_, err := config.Load()
	if err == nil {
		t.Fatalf("expected error when sms provider invalid")
	}
	if !strings.Contains(err.Error(), "SMS_PROVIDER must be one of") {
		t.Fatalf("expected sms provider validation error, got %q", err.Error())
	}
}

func TestLoadInvalidWhatsAppProvider(t *testing.T) {
	setCommonRequiredEnv(t)
	t.Setenv("WHATSAPP_PROVIDER", "invalid")

	_, err := config.Load()
	if err == nil {
		t.Fatalf("expected error when whatsapp provider invalid")
	}
	if !strings.Contains(err.Error(), "WHATSAPP_PROVIDER must be one of") {
		t.Fatalf("expected whatsapp provider validation error, got %q", err.Error())
	}
}
