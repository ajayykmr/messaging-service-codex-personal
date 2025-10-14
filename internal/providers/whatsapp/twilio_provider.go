package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rs/zerolog"

	"github.com/example/messaging-microservice/internal/config"
	"github.com/example/messaging-microservice/internal/models"
)

// HTTPClient abstracts the http.Client Do method for easier testing.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// TwilioOption customises the behaviour of the WhatsApp Twilio provider.
type TwilioOption func(*TwilioProvider)

// WithTwilioHTTPClient overrides the HTTP client used to talk to Twilio.
func WithTwilioHTTPClient(client HTTPClient) TwilioOption {
	return func(p *TwilioProvider) {
		if client != nil {
			p.httpClient = client
		}
	}
}

// WithTwilioBaseURL sets the base Twilio API URL. Useful for tests.
func WithTwilioBaseURL(baseURL string) TwilioOption {
	return func(p *TwilioProvider) {
		p.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// WithTwilioClock overrides the clock used for timestamps.
func WithTwilioClock(now func() time.Time) TwilioOption {
	return func(p *TwilioProvider) {
		if now != nil {
			p.now = now
		}
	}
}

// WithTwilioBodyLimit adjusts how many bytes are retained from the HTTP response body.
func WithTwilioBodyLimit(limit int64) TwilioOption {
	return func(p *TwilioProvider) {
		if limit > 0 {
			p.maxBodyBytes = limit
		}
	}
}

// TwilioProvider implements the Provider interface for WhatsApp using Twilio's API.
type TwilioProvider struct {
	logger       zerolog.Logger
	accountSID   string
	authToken    string
	defaultFrom  string
	httpClient   HTTPClient
	baseURL      string
	now          func() time.Time
	maxBodyBytes int64
}

// NewTwilioProvider constructs a Twilio-backed WhatsApp provider.
func NewTwilioProvider(cfg config.TwilioConfig, logger zerolog.Logger, opts ...TwilioOption) (*TwilioProvider, error) {
	if strings.TrimSpace(cfg.AccountSID) == "" {
		return nil, errors.New("twilio whatsapp provider: account SID is required")
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, errors.New("twilio whatsapp provider: auth token is required")
	}
	if strings.TrimSpace(cfg.PhoneNumber) == "" {
		return nil, errors.New("twilio whatsapp provider: phone number is required")
	}
	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}

	provider := &TwilioProvider{
		logger:       logger,
		accountSID:   strings.TrimSpace(cfg.AccountSID),
		authToken:    strings.TrimSpace(cfg.AuthToken),
		defaultFrom:  formatWhatsAppAddress(cfg.PhoneNumber),
		baseURL:      "https://api.twilio.com/2010-04-01",
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		now:          time.Now,
		maxBodyBytes: 16 * 1024,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(provider)
		}
	}

	if provider.httpClient == nil {
		provider.httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if provider.maxBodyBytes <= 0 {
		provider.maxBodyBytes = 16 * 1024
	}
	if provider.baseURL == "" {
		provider.baseURL = "https://api.twilio.com/2010-04-01"
	}
	if provider.defaultFrom == "" {
		provider.defaultFrom = formatWhatsAppAddress(cfg.PhoneNumber)
	}

	return provider, nil
}

// Send delivers the WhatsApp payload via Twilio.
func (p *TwilioProvider) Send(ctx context.Context, payload *Payload) (*RawResponse, error) {
	if payload == nil {
		return nil, errors.New("twilio whatsapp provider: payload is required")
	}
	if len(payload.To) == 0 {
		return nil, errors.New("twilio whatsapp provider: at least one recipient is required")
	}

	from := formatWhatsAppAddress(payload.From)
	if from == "" {
		from = p.defaultFrom
	}
	if from == "" {
		return nil, errors.New("twilio whatsapp provider: from number is required")
	}

	endpoint := fmt.Sprintf("%s/Accounts/%s/Messages.json", p.baseURL, url.PathEscape(p.accountSID))
	var lastStatus string
	var lastBody string
	var lastCode int
	var ids []string

	for _, recipient := range payload.To {
		result, body, err := p.sendSingle(ctx, endpoint, from, recipient, payload)
		if result != nil {
			if result.SID != "" {
				ids = append(ids, result.SID)
			}
			if result.Status != "" {
				lastStatus = result.Status
			}
			lastCode = result.HTTPStatus
		}
		lastBody = body
		if err != nil {
			raw := &RawResponse{
				ID:        strings.Join(ids, ","),
				Code:      lastCode,
				Status:    lastStatus,
				Body:      body,
				Timestamp: p.now(),
			}
			return raw, err
		}
	}

	return &RawResponse{
		ID:        strings.Join(ids, ","),
		Code:      lastCode,
		Status:    lastStatus,
		Body:      lastBody,
		Timestamp: p.now(),
	}, nil
}

type twilioResult struct {
	SID        string
	Status     string
	HTTPStatus int
}

func (p *TwilioProvider) sendSingle(ctx context.Context, endpoint, from, to string, payload *Payload) (*twilioResult, string, error) {
	params := url.Values{}
	params.Set("To", formatWhatsAppAddress(to))
	params.Set("From", from)

	switch strings.ToLower(payload.BodyType) {
	case models.BodyTypeMedia:
		if strings.TrimSpace(payload.Body) != "" {
			params.Set("MediaUrl", payload.Body)
		}
	default:
		if strings.TrimSpace(payload.Body) != "" {
			params.Set("Body", payload.Body)
		}
	}

	for key, value := range payload.Meta {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		if strings.EqualFold(key, "scenario") {
			continue
		}
		if equalsIgnoreCase(key, "to") || equalsIgnoreCase(key, "from") || equalsIgnoreCase(key, "body") || equalsIgnoreCase(key, "mediaurl") {
			continue
		}
		params.Set(normalizeTwilioParam(key), value)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, "", fmt.Errorf("twilio whatsapp provider: new request: %w", err)
	}
	req.SetBasicAuth(p.accountSID, p.authToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("twilio whatsapp provider: http do: %w", err)
	}
	defer resp.Body.Close()

	body, err := p.readBody(resp.Body)
	if err != nil {
		return nil, "", err
	}

	parsed := parseTwilioBody(body)
	result := &twilioResult{
		SID:        parsed.SID,
		Status:     parsed.Status,
		HTTPStatus: resp.StatusCode,
	}
	if result.Status == "" {
		result.Status = http.StatusText(resp.StatusCode)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if result.SID == "" {
			result.SID = payload.MessageID
		}
		return result, body, nil
	}

	message := parsed.Message
	if message == "" {
		message = strings.TrimSpace(body)
	}
	if message == "" {
		message = http.StatusText(resp.StatusCode)
	}

	if parsed.ErrorCode > 0 {
		return result, body, fmt.Errorf("twilio whatsapp provider: error %d: %s", parsed.ErrorCode, message)
	}

	return result, body, fmt.Errorf("twilio whatsapp provider: http %d: %s", resp.StatusCode, message)
}

func (p *TwilioProvider) readBody(rc io.ReadCloser) (string, error) {
	if rc == nil {
		return "", nil
	}

	limit := p.maxBodyBytes
	if limit <= 0 {
		limit = 16 * 1024
	}

	reader := io.LimitReader(rc, limit)
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("twilio whatsapp provider: read body: %w", err)
	}
	return string(data), nil
}

type twilioBody struct {
	SID       string `json:"sid"`
	Status    string `json:"status"`
	ErrorCode int    `json:"code"`
	Message   string `json:"message"`
}

func parseTwilioBody(body string) twilioBody {
	if strings.TrimSpace(body) == "" {
		return twilioBody{}
	}

	var parsed twilioBody
	if err := json.Unmarshal([]byte(body), &parsed); err == nil {
		return parsed
	}

	var generic map[string]any
	if err := json.Unmarshal([]byte(body), &generic); err != nil {
		return twilioBody{}
	}

	result := twilioBody{}
	if v, ok := generic["sid"].(string); ok {
		result.SID = v
	}
	if v, ok := generic["status"].(string); ok {
		result.Status = v
	}
	if v, ok := generic["code"]; ok {
		switch value := v.(type) {
		case float64:
			result.ErrorCode = int(value)
		case string:
			if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
				result.ErrorCode = n
			}
		}
	}
	if v, ok := generic["message"].(string); ok {
		result.Message = v
	}
	return result
}

func equalsIgnoreCase(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func normalizeTwilioParam(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return key
	}
	if unicode.IsUpper([]rune(key)[0]) {
		return key
	}
	parts := strings.FieldsFunc(key, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + strings.ToLower(part[1:])
	}
	return strings.Join(parts, "")
}

func formatWhatsAppAddress(number string) string {
	trimmed := strings.TrimSpace(number)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "whatsapp:") {
		suffix := strings.TrimSpace(trimmed[len("whatsapp:"):])
		return "whatsapp:" + suffix
	}
	return "whatsapp:" + trimmed
}
