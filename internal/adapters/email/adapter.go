package email

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	common "github.com/example/messaging-microservice/internal/adapters/common"
	"github.com/example/messaging-microservice/internal/models"
	emailprovider "github.com/example/messaging-microservice/internal/providers/email"
)

var smtpErrPattern = regexp.MustCompile(`smtp\s+(\d{3})`)

// Option customises adapter behaviour.
type Option func(*Adapter)

// WithRawBodyLimit overrides the maximum number of characters retained from the
// provider raw response.
func WithRawBodyLimit(limit int) Option {
	return func(a *Adapter) {
		if limit > 0 {
			a.maxRawChars = limit
		}
	}
}

// Adapter implements worker.Adapter for the email channel, translating
// validated requests into provider payloads and classifying responses.
type Adapter struct {
	logger      zerolog.Logger
	provider    emailprovider.Provider
	maxRawChars int
}

// NewAdapter constructs an email adapter using the provided dependencies.
func NewAdapter(provider emailprovider.Provider, logger zerolog.Logger, opts ...Option) (*Adapter, error) {
	if provider == nil {
		return nil, errors.New("email adapter: provider dependency is required")
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

// Send converts the validated message to a provider payload and delegates the
// send operation to the configured provider. Errors are wrapped with sentinel
// markers so the worker can distinguish between transient and permanent
// failures.
func (a *Adapter) Send(ctx context.Context, msg *common.ValidatedMessage) (*common.ProviderResponse, error) {
	if msg == nil || msg.Request == nil {
		return nil, common.WrapPermanent(errors.New("email adapter: message request is nil"))
	}

	req, ok := msg.Request.(*models.EmailRequest)
	if !ok {
		return nil, common.WrapPermanent(fmt.Errorf("email adapter: expected *models.EmailRequest, got %T", msg.Request))
	}

	payload := a.buildPayload(req, msg)

	rawResp, err := a.provider.Send(ctx, payload)
	if err != nil {
		resp := a.buildErrorResponse(rawResp, err)
		a.logger.Info().
			Str("message_id", req.MessageID).
			Str("channel", models.ChannelEmail).
			Str("provider_status", resp.Status).
			Str("provider_id", resp.Meta["provider_id"]).
			Err(err).
			Msg("email adapter send failed")
		return resp, a.wrapError(err, rawResp)
	}

	resp := a.buildSuccessResponse(rawResp)
	a.logger.Debug().
		Str("message_id", req.MessageID).
		Str("channel", models.ChannelEmail).
		Str("provider_status", resp.Status).
		Str("provider_id", resp.Meta["provider_id"]).
		Msg("email adapter send succeeded")
	return resp, nil
}

func (a *Adapter) buildPayload(req *models.EmailRequest, msg *common.ValidatedMessage) *emailprovider.Payload {
	headers := map[string]string{
		"Message-ID": req.MessageID,
	}
	if req.TraceID != "" {
		headers["X-Trace-ID"] = req.TraceID
	}
	if req.TenantID != "" {
		headers["X-Tenant-ID"] = req.TenantID
	}
	if msg != nil && len(msg.KafkaHeaders) > 0 {
		for key, val := range msg.KafkaHeaders {
			headers[key] = string(val)
		}
	}

	return &emailprovider.Payload{
		MessageID: req.MessageID,
		From:      req.From,
		To:        append([]string(nil), req.To...),
		CC:        append([]string(nil), req.CC...),
		BCC:       append([]string(nil), req.BCC...),
		Subject:   req.Subject,
		BodyType:  req.Body.Type,
		Body:      req.Body.Content,
		Headers:   headers,
	}
}

func (a *Adapter) buildSuccessResponse(raw *emailprovider.RawResponse) *common.ProviderResponse {
	meta := make(map[string]string)
	var codePtr *int
	if raw != nil {
		if raw.ID != "" {
			meta["provider_id"] = raw.ID
		}
		if !raw.Timestamp.IsZero() {
			meta["provider_timestamp"] = raw.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		codePtr = new(int)
		*codePtr = raw.Code
	}
	if len(meta) == 0 {
		meta = nil
	}

	return &common.ProviderResponse{
		Status:  "ok",
		Code:    codePtr,
		Message: "sent",
		Raw:     a.truncateRaw(raw),
		Meta:    meta,
	}
}

func (a *Adapter) buildErrorResponse(raw *emailprovider.RawResponse, err error) *common.ProviderResponse {
	meta := make(map[string]string)
	var rawCode *int
	if raw != nil {
		if raw.ID != "" {
			meta["provider_id"] = raw.ID
		}
		if !raw.Timestamp.IsZero() {
			meta["provider_timestamp"] = raw.Timestamp.UTC().Format(time.RFC3339Nano)
		}
		rawCode = new(int)
		*rawCode = raw.Code
	}

	codeFromErr, ok := extractSMTPCode(err)
	if rawCode == nil && ok {
		rawCode = &codeFromErr
	}

	status := a.classifyStatus(err, rawCode)

	if len(meta) == 0 {
		meta = nil
	}

	return &common.ProviderResponse{
		Status:  status,
		Code:    rawCode,
		Message: err.Error(),
		Raw:     a.truncateRaw(raw),
		Meta:    meta,
	}
}

func (a *Adapter) wrapError(err error, raw *emailprovider.RawResponse) error {
	code, ok := extractSMTPCode(err)
	if !ok && raw != nil {
		code = raw.Code
		ok = true
	}

	switch {
	case ok && isPermanentCode(code):
		return common.WrapPermanent(err)
	case isTimeout(err):
		return common.WrapTransient(err)
	default:
		return common.WrapTransient(err)
	}
}

func (a *Adapter) classifyStatus(err error, code *int) string {
	if code != nil {
		switch {
		case isPermanentCode(*code):
			return "rejected"
		case *code >= 400:
			return "rate_limited"
		}
	}

	if isTimeout(err) {
		return "rate_limited"
	}

	return "unknown"
}

func (a *Adapter) truncateRaw(raw *emailprovider.RawResponse) string {
	if raw == nil || raw.Body == "" {
		return ""
	}
	return common.TruncateRaw(raw.Body, a.maxRawChars)
}

func extractSMTPCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	matches := smtpErrPattern.FindStringSubmatch(err.Error())
	if len(matches) != 2 {
		return 0, false
	}
	code, convErr := strconv.Atoi(matches[1])
	if convErr != nil {
		return 0, false
	}
	return code, true
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	return strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func isPermanentCode(code int) bool {
	switch code {
	case 530, 535, 550, 551, 553:
		return true
	default:
		return false
	}
}
