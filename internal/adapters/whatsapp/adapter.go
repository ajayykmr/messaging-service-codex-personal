package whatsapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	common "github.com/example/messaging-microservice/internal/adapters/common"
	"github.com/example/messaging-microservice/internal/models"
	waprovider "github.com/example/messaging-microservice/internal/providers/whatsapp"
)

// Option customises adapter behaviour.
type Option func(*Adapter)

// WithRawBodyLimit overrides the maximum number of characters retained from the provider body.
func WithRawBodyLimit(limit int) Option {
	return func(a *Adapter) {
		if limit > 0 {
			a.maxRawChars = limit
		}
	}
}

// Adapter implements common.Adapter for WhatsApp messages.
type Adapter struct {
	logger      zerolog.Logger
	provider    waprovider.Provider
	maxRawChars int
}

// NewAdapter constructs a WhatsApp adapter.
func NewAdapter(provider waprovider.Provider, logger zerolog.Logger, opts ...Option) (*Adapter, error) {
	if provider == nil {
		return nil, errors.New("whatsapp adapter: provider dependency is required")
	}
	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}

	a := &Adapter{
		logger:      logger,
		provider:    provider,
		maxRawChars: common.DefaultRawBodyLimit,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return a, nil
}

// Send converts the validated message into a provider payload and delegates to the provider.
func (a *Adapter) Send(ctx context.Context, msg *common.ValidatedMessage) (*common.ProviderResponse, error) {
	if msg == nil || msg.Request == nil {
		return nil, common.WrapPermanent(errors.New("whatsapp adapter: message request is nil"))
	}

	req, ok := msg.Request.(*models.WhatsAppRequest)
	if !ok {
		return nil, common.WrapPermanent(fmt.Errorf("whatsapp adapter: expected *models.WhatsAppRequest, got %T", msg.Request))
	}

	payload := a.buildPayload(req, msg)

	rawResp, err := a.provider.Send(ctx, payload)
	if err != nil {
		status := classifyWhatsAppError(rawResp, err)
		resp := a.buildErrorResponse(rawResp, err, status)
		a.logger.Warn().
			Str("message_id", req.MessageID).
			Str("channel", models.ChannelWhatsApp).
			Str("provider_status", resp.Status).
			Str("provider_id", resp.Meta["provider_id"]).
			Err(err).
			Msg("whatsapp adapter send failed")
		return resp, wrapWhatsAppError(rawResp, err)
	}

	resp := a.buildSuccessResponse(rawResp)
	a.logger.Debug().
		Str("message_id", req.MessageID).
		Str("channel", models.ChannelWhatsApp).
		Str("provider_status", resp.Status).
		Str("provider_id", resp.Meta["provider_id"]).
		Msg("whatsapp adapter send succeeded")
	return resp, nil
}

func (a *Adapter) buildPayload(req *models.WhatsAppRequest, msg *common.ValidatedMessage) *waprovider.Payload {
	meta := map[string]string{}
	if req.MessageID != "" {
		meta["message_id"] = req.MessageID
	}
	if strings.TrimSpace(req.TraceID) != "" {
		meta["trace_id"] = req.TraceID
	}
	if strings.TrimSpace(req.TenantID) != "" {
		meta["tenant_id"] = req.TenantID
	}
	if msg != nil {
		for key, value := range msg.Metadata {
			if strVal, ok := value.(string); ok && strings.TrimSpace(strVal) != "" {
				meta[key] = strVal
			}
		}
		for key, val := range msg.KafkaHeaders {
			if len(val) > 0 {
				meta[key] = string(val)
			}
		}
	}
	if len(meta) == 0 {
		meta = nil
	}

	return &waprovider.Payload{
		MessageID: req.MessageID,
		From:      req.From,
		To:        append([]string(nil), req.To...),
		BodyType:  req.Body.Type,
		Body:      req.Body.Content,
		Meta:      meta,
	}
}

func (a *Adapter) buildSuccessResponse(raw *waprovider.RawResponse) *common.ProviderResponse {
	meta := make(map[string]string)
	if raw != nil {
		if raw.ID != "" {
			meta["provider_id"] = raw.ID
		}
		if raw.Status != "" {
			meta["provider_status"] = raw.Status
		}
		if !raw.Timestamp.IsZero() {
			meta["provider_timestamp"] = raw.Timestamp.UTC().Format(time.RFC3339Nano)
		}
	}
	if len(meta) == 0 {
		meta = nil
	}

	var codePtr *int
	if raw != nil {
		codePtr = optionalInt(raw.Code)
	}

	return &common.ProviderResponse{
		Status:  "ok",
		Message: "sent",
		Code:    codePtr,
		Raw:     a.truncateRaw(raw),
		Meta:    meta,
	}
}

func (a *Adapter) buildErrorResponse(raw *waprovider.RawResponse, err error, status string) *common.ProviderResponse {
	meta := make(map[string]string)
	if raw != nil {
		if raw.ID != "" {
			meta["provider_id"] = raw.ID
		}
		if raw.Status != "" {
			meta["provider_status"] = raw.Status
		}
		if !raw.Timestamp.IsZero() {
			meta["provider_timestamp"] = raw.Timestamp.UTC().Format(time.RFC3339Nano)
		}
	}
	if len(meta) == 0 {
		meta = nil
	}

	var codePtr *int
	if raw != nil {
		codePtr = optionalInt(raw.Code)
	}

	return &common.ProviderResponse{
		Status:  status,
		Message: err.Error(),
		Code:    codePtr,
		Raw:     a.truncateRaw(raw),
		Meta:    meta,
	}
}

func (a *Adapter) truncateRaw(raw *waprovider.RawResponse) string {
	if raw == nil || strings.TrimSpace(raw.Body) == "" {
		return ""
	}
	return common.TruncateRaw(raw.Body, a.maxRawChars)
}

func classifyWhatsAppError(raw *waprovider.RawResponse, err error) string {
	if raw != nil {
		if code, ok := extractTwilioErrorCode(raw.Body); ok {
			switch code {
			case 21610, 21612, 21614, 21211:
				return "rejected"
			case 63018, 63016, 63015, 63002, 30001, 30003:
				return "rate_limited"
			}
		}
	}
	if raw != nil {
		lowerStatus := strings.ToLower(raw.Status)
		if strings.Contains(lowerStatus, "permanent") || strings.Contains(lowerStatus, "invalid") {
			return "rejected"
		}
		if strings.Contains(lowerStatus, "transient") {
			return "rate_limited"
		}
		switch {
		case raw.Code >= 500:
			return "rate_limited"
		case raw.Code == 429:
			return "rate_limited"
		case raw.Code >= 400:
			return "rejected"
		}
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "invalid") || strings.Contains(lower, "permanent") {
		return "rejected"
	}
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "temporary") {
		return "rate_limited"
	}
	return "unknown"
}

func wrapWhatsAppError(raw *waprovider.RawResponse, err error) error {
	if raw != nil {
		if code, ok := extractTwilioErrorCode(raw.Body); ok {
			switch code {
			case 21610, 21612, 21614, 21211:
				return common.WrapPermanent(err)
			case 63018, 63016, 63015, 63002, 30001, 30003:
				return common.WrapTransient(err)
			}
		}
	}
	if raw != nil {
		lowerStatus := strings.ToLower(raw.Status)
		if strings.Contains(lowerStatus, "permanent") || strings.Contains(lowerStatus, "invalid") {
			return common.WrapPermanent(err)
		}
		if strings.Contains(lowerStatus, "transient") {
			return common.WrapTransient(err)
		}
		switch {
		case raw.Code >= 500:
			return common.WrapTransient(err)
		case raw.Code == 429:
			return common.WrapTransient(err)
		case raw.Code >= 400:
			return common.WrapPermanent(err)
		}
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "permanent") || strings.Contains(lower, "invalid") {
		return common.WrapPermanent(err)
	}
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "temporary") {
		return common.WrapTransient(err)
	}
	return common.WrapTransient(err)
}

func optionalInt(code int) *int {
	if code == 0 {
		return nil
	}
	c := code
	return &c
}

func extractTwilioErrorCode(body string) (int, bool) {
	if strings.TrimSpace(body) == "" {
		return 0, false
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return 0, false
	}

	val, ok := payload["code"]
	if !ok {
		return 0, false
	}

	switch v := val.(type) {
	case float64:
		return int(v), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}
