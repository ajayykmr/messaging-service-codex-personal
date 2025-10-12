package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config captures all runtime configuration for the messaging service.
// The shape of this struct mirrors the specification documented in
// docs/design.md to keep configuration deterministic and discoverable.
type Config struct {
	App            AppConfig
	Kafka          KafkaConfig
	Topics         TopicConfig
	ConsumerGroups ConsumerGroupConfig
	Retry          RetryConfig
	Validation     ValidationConfig
	Providers      ProviderConfig
	Timeouts       TimeoutConfig
	Health         HealthConfig
}

// AppConfig contains generic application level settings.
type AppConfig struct {
	Env      string
	Port     int
	LogLevel string
}

// KafkaConfig defines broker information and cluster level tuning.
type KafkaConfig struct {
	Brokers           []string
	RequestPartitions int
	ReplicationFactor int
}

// TopicPair groups request, status and DLQ topics for a single channel.
type TopicPair struct {
	Request string
	Status  string
	DLQ     string
}

// TopicConfig enumerates the topics for each supported channel.
type TopicConfig struct {
	Email    TopicPair
	SMS      TopicPair
	WhatsApp TopicPair
}

// ConsumerGroupConfig provides the consumer group name per channel.
type ConsumerGroupConfig struct {
	Email    string
	SMS      string
	WhatsApp string
}

// RetryConfig controls worker retry and backoff behaviour.
type RetryConfig struct {
	MaxAttempts         int
	BaseBackoffSeconds  int
	MaxBackoffSeconds   int
	BackoffStrategy     string
	BackoffJitter       string
	WorkerConcurrency   int
	CommitOnSuccessOnly bool
}

// ValidationConfig holds the limits used while validating inbound requests.
type ValidationConfig struct {
	MsgMaxBytes      int
	RecipientsMax    int
	SubjectMaxLen    int
	BodyMaxBytes     int
	SMSRecipientsMax int
	SMSBodyMax       int
	WABodyMax        int
	MetaMaxEntries   int
	MetaMaxKeyLen    int
	MetaMaxValueLen  int
}

// SMTPConfig stores SMTP credentials for email delivery.
type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
	From string
}

// TwilioConfig stores Twilio credentials for SMS/WhatsApp delivery.
type TwilioConfig struct {
	AccountSID  string
	AuthToken   string
	PhoneNumber string
}

// ProviderConfig wraps configuration for external providers.
type ProviderConfig struct {
	SMTP   SMTPConfig
	Twilio TwilioConfig
}

// TimeoutConfig contains timeout thresholds for outbound providers.
type TimeoutConfig struct {
	ProviderTimeoutSeconds int
}

// HealthConfig controls health-check polling and timeout behaviour.
type HealthConfig struct {
	PollIntervalSeconds int
	CheckTimeoutMs      int
	HandlerTimeoutMs    int
	EnableProviderProbe bool
}

// Load reads environment variables, applies defaults, validates required
// values and returns a populated Config instance.
func Load() (*Config, error) {
	_ = godotenv.Load()

	ldr := &envLoader{}

	cfg := &Config{}
	cfg.App.Env = ldr.getString("APP_ENV", "development", false)
	cfg.App.Port = ldr.getInt("APP_PORT", 8080, false)
	cfg.App.LogLevel = ldr.getString("LOG_LEVEL", "info", false)

	cfg.Kafka.Brokers = ldr.getStringSlice("KAFKA_BROKERS", true)
	cfg.Kafka.RequestPartitions = ldr.getInt("KAFKA_REQUEST_PARTITIONS", 6, false)
	cfg.Kafka.ReplicationFactor = ldr.getInt("KAFKA_REPLICATION_FACTOR", 1, false)

	cfg.Topics.Email = TopicPair{
		Request: ldr.getString("KAFKA_EMAIL_REQUEST_TOPIC", "", true),
		Status:  ldr.getString("KAFKA_EMAIL_STATUS_TOPIC", "", true),
		DLQ:     ldr.getString("KAFKA_EMAIL_DLQ_TOPIC", "", true),
	}
	cfg.Topics.SMS = TopicPair{
		Request: ldr.getString("KAFKA_SMS_REQUEST_TOPIC", "", true),
		Status:  ldr.getString("KAFKA_SMS_STATUS_TOPIC", "", true),
		DLQ:     ldr.getString("KAFKA_SMS_DLQ_TOPIC", "", true),
	}
	cfg.Topics.WhatsApp = TopicPair{
		Request: ldr.getString("KAFKA_WHATSAPP_REQUEST_TOPIC", "", true),
		Status:  ldr.getString("KAFKA_WHATSAPP_STATUS_TOPIC", "", true),
		DLQ:     ldr.getString("KAFKA_WHATSAPP_DLQ_TOPIC", "", true),
	}

	cfg.ConsumerGroups.Email = ldr.getString("EMAIL_CONSUMER_GROUP", "", true)
	cfg.ConsumerGroups.SMS = ldr.getString("SMS_CONSUMER_GROUP", "", true)
	cfg.ConsumerGroups.WhatsApp = ldr.getString("WHATSAPP_CONSUMER_GROUP", "", true)

	cfg.Retry.MaxAttempts = ldr.getInt("MAX_ATTEMPTS", 3, false)
	cfg.Retry.BaseBackoffSeconds = ldr.getInt("BASE_BACKOFF_SECONDS", 10, false)
	cfg.Retry.MaxBackoffSeconds = ldr.getInt("MAX_BACKOFF_SECONDS", 120, false)
	cfg.Retry.BackoffStrategy = ldr.getString("BACKOFF_STRATEGY", "exponential", false)
	cfg.Retry.BackoffJitter = ldr.getString("BACKOFF_JITTER", "full", false)
	cfg.Retry.WorkerConcurrency = ldr.getInt("WORKER_CONCURRENCY", 10, false)
	cfg.Retry.CommitOnSuccessOnly = ldr.getBool("COMMIT_ON_SUCCESS_ONLY", true, false)

	cfg.Validation.MsgMaxBytes = ldr.getInt("MSG_MAX_BYTES", 200000, false)
	cfg.Validation.RecipientsMax = ldr.getInt("RECIPIENTS_MAX", 50, false)
	cfg.Validation.SubjectMaxLen = ldr.getInt("SUBJECT_MAX_LEN", 255, false)
	cfg.Validation.BodyMaxBytes = ldr.getInt("BODY_MAX_BYTES", 100000, false)
	cfg.Validation.SMSRecipientsMax = ldr.getInt("SMS_RECIPIENTS_MAX", 10, false)
	cfg.Validation.SMSBodyMax = ldr.getInt("SMS_BODY_MAX", 1600, false)
	cfg.Validation.WABodyMax = ldr.getInt("WA_BODY_MAX", 4096, false)
	cfg.Validation.MetaMaxEntries = ldr.getInt("META_MAX_ENTRIES", 20, false)
	cfg.Validation.MetaMaxKeyLen = ldr.getInt("META_MAX_KEY_LEN", 64, false)
	cfg.Validation.MetaMaxValueLen = ldr.getInt("META_MAX_VALUE_LEN", 256, false)

	cfg.Providers.SMTP.Host = ldr.getString("SMTP_HOST", "", true)
	cfg.Providers.SMTP.Port = ldr.getInt("SMTP_PORT", 0, true)
	cfg.Providers.SMTP.User = ldr.getString("SMTP_USER", "", true)
	cfg.Providers.SMTP.Pass = ldr.getString("SMTP_PASS", "", true)
	cfg.Providers.SMTP.From = ldr.getString("SMTP_FROM", "", true)

	cfg.Providers.Twilio.AccountSID = ldr.getString("TWILIO_ACCOUNT_SID", "", true)
	cfg.Providers.Twilio.AuthToken = ldr.getString("TWILIO_AUTH_TOKEN", "", true)
	cfg.Providers.Twilio.PhoneNumber = ldr.getString("TWILIO_PHONE_NUMBER", "", true)

	cfg.Timeouts.ProviderTimeoutSeconds = ldr.getInt("PROVIDER_TIMEOUT_SECONDS", 30, false)

	cfg.Health.PollIntervalSeconds = ldr.getInt("HEALTH_POLL_INTERVAL_SECONDS", 15, false)
	cfg.Health.CheckTimeoutMs = ldr.getInt("HEALTH_CHECK_TIMEOUT_MS", 200, false)
	cfg.Health.HandlerTimeoutMs = ldr.getInt("HEALTH_HANDLER_TIMEOUT_MS", 500, false)
	cfg.Health.EnableProviderProbe = ldr.getBool("HEALTH_ENABLE_PROVIDER_PROBE", false, false)

	if err := ldr.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

type envLoader struct {
	errs []string
}

func (l *envLoader) validate() error {
	if len(l.errs) == 0 {
		return nil
	}
	return fmt.Errorf("config validation failed: %s", strings.Join(l.errs, "; "))
}

func (l *envLoader) getString(key, def string, required bool) string {
	if val, ok := os.LookupEnv(key); ok {
		val = strings.TrimSpace(val)
		if val == "" {
			if required {
				l.addError(fmt.Sprintf("%s is required", key))
			}
			return def
		}
		return val
	}
	if required {
		l.addError(fmt.Sprintf("%s is required", key))
	}
	return def
}

func (l *envLoader) getInt(key string, def int, required bool) int {
	if val, ok := os.LookupEnv(key); ok {
		val = strings.TrimSpace(val)
		if val == "" {
			if required {
				l.addError(fmt.Sprintf("%s is required", key))
			}
			return def
		}
		i, err := strconv.Atoi(val)
		if err != nil {
			l.addError(fmt.Sprintf("%s must be a valid integer", key))
			return def
		}
		return i
	}
	if required {
		l.addError(fmt.Sprintf("%s is required", key))
	}
	return def
}

func (l *envLoader) getBool(key string, def bool, required bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		val = strings.TrimSpace(val)
		if val == "" {
			if required {
				l.addError(fmt.Sprintf("%s is required", key))
			}
			return def
		}
		parsed, err := strconv.ParseBool(val)
		if err != nil {
			l.addError(fmt.Sprintf("%s must be a valid boolean", key))
			return def
		}
		return parsed
	}
	if required {
		l.addError(fmt.Sprintf("%s is required", key))
	}
	return def
}

func (l *envLoader) getStringSlice(key string, required bool) []string {
	raw := l.getString(key, "", required)
	if raw == "" {
		if required {
			return nil
		}
		return []string{}
	}
	parts := strings.Split(raw, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if required && len(out) == 0 {
		l.addError(fmt.Sprintf("%s must contain at least one entry", key))
	}
	return out
}

func (l *envLoader) addError(err string) {
	l.errs = append(l.errs, err)
}
