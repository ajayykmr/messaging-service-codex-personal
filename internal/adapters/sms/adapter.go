package sms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	common "github.com/ajayykmr/messaging-service-go/internal/adapters/common"
	"github.com/ajayykmr/messaging-service-go/internal/models"
	smsprovider "github.com/ajayykmr/messaging-service-go/internal/providers/sms"
)

// Option modifies adapter behaviour.
type Option func(*Adapter)

// WithRawBodyLimit overrides how much of the provider body to keep in responses.
func WithRawBodyLimit(limit int) Option {
	return func(a *Adapter) {
		if limit > 0 {
			a.maxRawChars = limit
		}
	}
}

// Adapter implements common.Adapter for the SMS channel.
type Adapter struct {
	logger      zerolog.Logger
	provider    smsprovider.Provider
	maxRawChars int
}

// NewAdapter constructs an SMS adapter using the supplied provider.
func NewAdapter(provider smsprovider.Provider, logger zerolog.Logger, opts ...Option) (*Adapter, error) {
	if provider == nil {
		return nil, errors.New("sms adapter: provider dependency is required")
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
		return nil, common.WrapPermanent(errors.New("sms adapter: message request is nil"))
	}

	req, ok := msg.Request.(*models.SMSRequest)
	if !ok {
		return nil, common.WrapPermanent(fmt.Errorf("sms adapter: expected *models.SMSRequest, got %T", msg.Request))
	}

	payload := a.buildPayload(req, msg)

	rawResp, err := a.provider.Send(ctx, payload)
	if err != nil {
		status := classifySMSError(rawResp, err)
		resp := a.buildErrorResponse(rawResp, err, status)
		a.logger.Warn().
			Str("message_id", req.MessageID).
			Str("channel", models.ChannelSMS).
			Str("provider_status", resp.Status).
			Str("provider_id", resp.Meta["provider_id"]).
			Err(err).
			Msg("sms adapter send failed")
		return resp, wrapSMSError(rawResp, err)
	}

	resp := a.buildSuccessResponse(rawResp)
	a.logger.Debug().
		Str("message_id", req.MessageID).
		Str("channel", models.ChannelSMS).
		Str("provider_status", resp.Status).
		Str("provider_id", resp.Meta["provider_id"]).
		Msg("sms adapter send succeeded")
	return resp, nil
}

func (a *Adapter) buildPayload(req *models.SMSRequest, msg *common.ValidatedMessage) *smsprovider.Payload {
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

	return &smsprovider.Payload{
		MessageID: req.MessageID,
		From:      req.From,
		To:        append([]string(nil), req.To...),
		Body:      req.Body.Content,
		Meta:      meta,
	}
}

func (a *Adapter) buildSuccessResponse(raw *smsprovider.RawResponse) *common.ProviderResponse {
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

func (a *Adapter) buildErrorResponse(raw *smsprovider.RawResponse, err error, status string) *common.ProviderResponse {
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

func (a *Adapter) truncateRaw(raw *smsprovider.RawResponse) string {
	if raw == nil || raw.Body == "" {
		return ""
	}
	return common.TruncateRaw(raw.Body, a.maxRawChars)
}

func classifySMSError(raw *smsprovider.RawResponse, err error) string {
	if raw != nil {
		if code, ok := extractTwilioErrorCode(raw.Body); ok {
			switch code {
			case 21610, 21612, 21614, 21211:
				return "rejected"
			case 30001, 30002, 30003, 30005:
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
		case raw.Code >= http.StatusInternalServerError:
			return "rate_limited"
		case raw.Code == http.StatusTooManyRequests:
			return "rate_limited"
		case raw.Code >= http.StatusBadRequest:
			return "rejected"
		}
	}
	var httpErr interface{ StatusCode() int }
	if errors.As(err, &httpErr) {
		code := httpErr.StatusCode()
		switch {
		case code >= http.StatusInternalServerError:
			return "rate_limited"
		case code >= http.StatusBadRequest:
			return "rejected"
		}
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return "rate_limited"
	}
	return "unknown"
}

func wrapSMSError(raw *smsprovider.RawResponse, err error) error {
	if raw != nil {
		if code, ok := extractTwilioErrorCode(raw.Body); ok {
			switch code {
			case 21610, 21612, 21614, 21211:
				return common.WrapPermanent(err)
			case 30001, 30002, 30003, 30005:
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
		case raw.Code >= http.StatusInternalServerError:
			return common.WrapTransient(err)
		case raw.Code == http.StatusTooManyRequests:
			return common.WrapTransient(err)
		case raw.Code >= http.StatusBadRequest:
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
