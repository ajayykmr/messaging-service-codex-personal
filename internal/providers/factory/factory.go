package factory

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"

	"github.com/example/messaging-microservice/internal/config"
	emailprovider "github.com/example/messaging-microservice/internal/providers/email"
	smsprovider "github.com/example/messaging-microservice/internal/providers/sms"
	waprovider "github.com/example/messaging-microservice/internal/providers/whatsapp"
)

// Email constructs the configured email provider, supporting SMTP and mock backends.
func Email(cfg config.ProviderConfig, logger zerolog.Logger) (emailprovider.Provider, error) {
	backend := normalize(cfg.EmailProvider, "mock")
	switch backend {
	case "smtp":
		provider, err := emailprovider.NewSMTPProvider(cfg.SMTP, logger)
		if err != nil {
			return nil, fmt.Errorf("factory: smtp provider init: %w", err)
		}
		logger.Info().
			Str("backend", "smtp").
			Msg("email provider initialised")
		return provider, nil
	case "mock":
		provider := emailprovider.NewMockProvider(logger)
		logger.Info().
			Str("backend", "mock").
			Msg("email provider initialised")
		return provider, nil
	default:
		return nil, fmt.Errorf("factory: unsupported email provider backend %q", cfg.EmailProvider)
	}
}

// SMS constructs the configured SMS provider. Supports mock and Twilio backends.
func SMS(cfg config.ProviderConfig, logger zerolog.Logger) (smsprovider.Provider, error) {
	backend := normalize(cfg.SMSProvider, "mock")
	switch backend {
	case "twilio":
		provider, err := smsprovider.NewTwilioProvider(cfg.Twilio, logger)
		if err != nil {
			return nil, fmt.Errorf("factory: twilio sms provider init: %w", err)
		}
		logger.Info().
			Str("backend", "twilio").
			Msg("sms provider initialised")
		return provider, nil
	case "mock":
		provider := smsprovider.NewMockProvider(logger)
		logger.Info().
			Str("backend", "mock").
			Msg("sms provider initialised")
		return provider, nil
	default:
		return nil, fmt.Errorf("factory: unsupported sms provider backend %q", cfg.SMSProvider)
	}
}

// WhatsApp constructs the configured WhatsApp provider. Supports mock and Twilio backends.
func WhatsApp(cfg config.ProviderConfig, logger zerolog.Logger) (waprovider.Provider, error) {
	backend := normalize(cfg.WhatsAppProvider, "mock")
	switch backend {
	case "twilio":
		provider, err := waprovider.NewTwilioProvider(cfg.Twilio, logger)
		if err != nil {
			return nil, fmt.Errorf("factory: twilio whatsapp provider init: %w", err)
		}
		logger.Info().
			Str("backend", "twilio").
			Msg("whatsapp provider initialised")
		return provider, nil
	case "mock":
		provider := waprovider.NewMockProvider(logger)
		logger.Info().
			Str("backend", "mock").
			Msg("whatsapp provider initialised")
		return provider, nil
	default:
		return nil, fmt.Errorf("factory: unsupported whatsapp provider backend %q", cfg.WhatsAppProvider)
	}
}

func normalize(value, def string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return def
	}
	return value
}
