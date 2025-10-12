package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/example/messaging-microservice/internal/models"
)

const (
	ChannelEmail    = "email"
	ChannelSMS      = "sms"
	ChannelWhatsApp = "whatsapp"

	RecipientsMax    = 50
	SubjectMaxLen    = 255
	BodyMaxBytes     = 100000
	SMSRecipientsMax = 10
	SMSBodyMax       = 1600
	WhatsAppBodyMax  = 4096

	MetaMaxEntries  = 20
	MetaMaxKeyLen   = 64
	MetaMaxValueLen = 256
)

var (
	e164Pattern       = regexp.MustCompile(`^\+[1-9]\d{1,14}$`)
	templateIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)
)

// ParseAndValidate decodes the raw Kafka payload and ensures it satisfies the
// validation rules for the provided channel.
func ParseAndValidate(raw []byte, expectedChannel string) (models.MessageRequest, error) {
	expectedChannel = strings.ToLower(expectedChannel)

	var envelope struct {
		Channel string `json:"channel"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode message envelope: %w", err)
	}

	channel := strings.ToLower(envelope.Channel)
	if channel == "" {
		return nil, errors.New("channel is required")
	}
	if expectedChannel != "" && channel != expectedChannel {
		return nil, fmt.Errorf("channel mismatch: expected %s, got %s", expectedChannel, channel)
	}

	switch channel {
	case ChannelEmail:
		var req models.EmailRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("decode email request: %w", err)
		}
		if err := validateCommon(&req.BaseRequest, ChannelEmail); err != nil {
			return nil, err
		}
		if err := validateEmail(&req); err != nil {
			return nil, err
		}
		return &req, nil
	case ChannelSMS:
		var req models.SMSRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("decode sms request: %w", err)
		}
		if err := validateCommon(&req.BaseRequest, ChannelSMS); err != nil {
			return nil, err
		}
		if err := validateSMS(&req); err != nil {
			return nil, err
		}
		return &req, nil
	case ChannelWhatsApp:
		var req models.WhatsAppRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			return nil, fmt.Errorf("decode whatsapp request: %w", err)
		}
		if err := validateCommon(&req.BaseRequest, ChannelWhatsApp); err != nil {
			return nil, err
		}
		if err := validateWhatsApp(&req); err != nil {
			return nil, err
		}
		return &req, nil
	default:
		return nil, fmt.Errorf("unsupported channel %q", channel)
	}
}

func validateCommon(base *models.BaseRequest, expectedChannel string) error {
	if base == nil {
		return errors.New("base request is required")
	}

	base.Channel = strings.ToLower(base.Channel)
	if base.Channel == "" {
		return errors.New("channel is required")
	}
	if expectedChannel != "" && base.Channel != expectedChannel {
		return fmt.Errorf("channel mismatch: expected %s, got %s", expectedChannel, base.Channel)
	}

	if base.MessageID == "" {
		return errors.New("message_id is required")
	}
	if _, err := uuid.Parse(base.MessageID); err != nil {
		return fmt.Errorf("invalid message_id: %w", err)
	}

	if base.CreatedAt.IsZero() {
		return errors.New("created_at is required")
	}

	if err := validateMeta(base.Meta); err != nil {
		return err
	}

	return nil
}

func validateMeta(meta map[string]string) error {
	if len(meta) == 0 {
		return nil
	}

	if len(meta) > MetaMaxEntries {
		return fmt.Errorf("meta has %d entries, exceeds limit %d", len(meta), MetaMaxEntries)
	}

	for k, v := range meta {
		trimmedKey := strings.TrimSpace(k)
		if trimmedKey == "" {
			return errors.New("meta keys must be non-empty")
		}
		if utf8.RuneCountInString(trimmedKey) > MetaMaxKeyLen {
			return fmt.Errorf("meta key %q exceeds max length %d", trimmedKey, MetaMaxKeyLen)
		}
		if utf8.RuneCountInString(v) > MetaMaxValueLen {
			return fmt.Errorf("meta value for key %q exceeds max length %d", trimmedKey, MetaMaxValueLen)
		}
	}

	return nil
}

func validateEmail(req *models.EmailRequest) error {
	if err := validateEmailAddress(req.From); err != nil {
		return fmt.Errorf("invalid from address: %w", err)
	}

	if err := validateEmailList("to", req.To, 1, RecipientsMax); err != nil {
		return err
	}
	if err := validateEmailList("cc", req.CC, 0, RecipientsMax); err != nil {
		return err
	}
	if err := validateEmailList("bcc", req.BCC, 0, RecipientsMax); err != nil {
		return err
	}

	subjectLen := utf8.RuneCountInString(strings.TrimSpace(req.Subject))
	if subjectLen == 0 {
		return errors.New("subject is required")
	}
	if subjectLen > SubjectMaxLen {
		return fmt.Errorf("subject exceeds max length %d", SubjectMaxLen)
	}

	if req.Body.Type == "" {
		return errors.New("body.type is required")
	}
	if len(req.Body.Content) > BodyMaxBytes {
		return fmt.Errorf("body content exceeds max size %d bytes", BodyMaxBytes)
	}

	return nil
}

func validateEmailList(field string, addrs []string, min, max int) error {
	if len(addrs) < min {
		if min == 1 {
			return fmt.Errorf("%s must contain at least one recipient", field)
		}
		return nil
	}
	if len(addrs) > max {
		return fmt.Errorf("%s has %d recipients, exceeds max %d", field, len(addrs), max)
	}
	for _, addr := range addrs {
		if err := validateEmailAddress(addr); err != nil {
			return fmt.Errorf("invalid %s address %q: %w", field, addr, err)
		}
	}
	return nil
}

func validateEmailAddress(addr string) error {
	if strings.TrimSpace(addr) == "" {
		return errors.New("address is empty")
	}
	if _, err := mail.ParseAddress(addr); err != nil {
		return err
	}
	return nil
}

func validateSMS(req *models.SMSRequest) error {
	if err := validateE164(req.From); err != nil {
		return fmt.Errorf("invalid from number: %w", err)
	}

	if len(req.To) == 0 {
		return errors.New("sms.to must contain at least one recipient")
	}
	if len(req.To) > SMSRecipientsMax {
		return fmt.Errorf("sms.to has %d recipients, exceeds max %d", len(req.To), SMSRecipientsMax)
	}
	for _, dest := range req.To {
		if err := validateE164(dest); err != nil {
			return fmt.Errorf("invalid destination number %q: %w", dest, err)
		}
	}

	content := strings.TrimSpace(req.Body.Content)
	if content == "" {
		return errors.New("sms body content is required")
	}
	if len(req.Body.Content) > SMSBodyMax {
		return fmt.Errorf("sms body exceeds max length %d", SMSBodyMax)
	}

	return nil
}

func validateWhatsApp(req *models.WhatsAppRequest) error {
	if err := validateE164(req.From); err != nil {
		return fmt.Errorf("invalid from number: %w", err)
	}

	if len(req.To) == 0 {
		return errors.New("whatsapp.to must contain at least one recipient")
	}
	if len(req.To) > RecipientsMax {
		return fmt.Errorf("whatsapp.to has %d recipients, exceeds max %d", len(req.To), RecipientsMax)
	}
	for _, dest := range req.To {
		if err := validateE164(dest); err != nil {
			return fmt.Errorf("invalid destination number %q: %w", dest, err)
		}
	}

	if req.Body.Type == "" {
		return errors.New("body.type is required")
	}
	if len(req.Body.Content) > WhatsAppBodyMax {
		return fmt.Errorf("body content exceeds max length %d", WhatsAppBodyMax)
	}

	switch strings.ToLower(req.Body.Type) {
	case "text":
		if strings.TrimSpace(req.Body.Content) == "" {
			return errors.New("body.content is required for text messages")
		}
	case "template":
		if !templateIDPattern.MatchString(strings.TrimSpace(req.Body.Content)) {
			return fmt.Errorf("body.content must match template id pattern %s", templateIDPattern.String())
		}
	case "media":
		if err := validateURL(req.Body.Content); err != nil {
			return fmt.Errorf("body.content must be a valid media URL: %w", err)
		}
	default:
		return fmt.Errorf("unsupported body.type %q for whatsapp", req.Body.Type)
	}

	return nil
}

func validateE164(number string) error {
	if strings.TrimSpace(number) == "" {
		return errors.New("number is empty")
	}
	if !e164Pattern.MatchString(number) {
		return fmt.Errorf("%q is not a valid E.164 number", number)
	}
	return nil
}

func validateURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("url is empty")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("unsupported url scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return errors.New("url host is required")
	}
	return nil
}
