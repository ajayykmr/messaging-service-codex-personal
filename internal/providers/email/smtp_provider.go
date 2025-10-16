package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/ajayykmr/messaging-service-go/internal/config"
)

// SMTPOption configures the behaviour of the SMTP provider.
type SMTPOption func(*SMTPProvider)

// WithSMTPTLSConfig overrides the TLS configuration used when negotiating STARTTLS.
func WithSMTPTLSConfig(cfg *tls.Config) SMTPOption {
	return func(p *SMTPProvider) {
		p.tlsConfig = cfg
	}
}

// WithSMTPDialer swaps the network dialer used to establish SMTP connections.
func WithSMTPDialer(d Dialer) SMTPOption {
	return func(p *SMTPProvider) {
		if d != nil {
			p.dialer = d
		}
	}
}

// WithSMTPAuth supplies a custom SMTP auth strategy. When omitted the provider uses
// the credentials from the supplied configuration.
func WithSMTPAuth(auth smtp.Auth) SMTPOption {
	return func(p *SMTPProvider) {
		p.auth = auth
	}
}

// WithSMTPClock replaces the clock used for timestamps.
func WithSMTPClock(now func() time.Time) SMTPOption {
	return func(p *SMTPProvider) {
		if now != nil {
			p.now = now
		}
	}
}

// WithSMTPHelloName customises the EHLO/HELO identity presented to the server.
func WithSMTPHelloName(name string) SMTPOption {
	return func(p *SMTPProvider) {
		if strings.TrimSpace(name) != "" {
			p.helloName = strings.TrimSpace(name)
		}
	}
}

// Dialer abstracts net.Dialer to simplify testing.
type Dialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

// SMTPProvider implements the Provider interface using a real SMTP backend.
type SMTPProvider struct {
	logger    zerolog.Logger
	host      string
	port      int
	from      string
	auth      smtp.Auth
	tlsConfig *tls.Config
	dialer    Dialer
	now       func() time.Time
	helloName string
}

// NewSMTPProvider constructs a Provider backed by an SMTP server.
func NewSMTPProvider(cfg config.SMTPConfig, logger zerolog.Logger, opts ...SMTPOption) (*SMTPProvider, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, errors.New("smtp provider: host is required")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return nil, fmt.Errorf("smtp provider: invalid port %d", cfg.Port)
	}
	if strings.TrimSpace(cfg.From) == "" {
		return nil, errors.New("smtp provider: from address is required")
	}

	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}

	p := &SMTPProvider{
		logger:    logger,
		host:      cfg.Host,
		port:      cfg.Port,
		from:      strings.TrimSpace(cfg.From),
		dialer:    &net.Dialer{Timeout: 30 * time.Second},
		now:       time.Now,
		helloName: "localhost",
	}

	if strings.TrimSpace(cfg.User) != "" {
		p.auth = smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
	}

	p.tlsConfig = &tls.Config{
		ServerName: cfg.Host,
		MinVersion: tls.VersionTLS12,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}

	return p, nil
}

// Send delivers the supplied payload using the configured SMTP backend.
func (p *SMTPProvider) Send(ctx context.Context, payload *Payload) (*RawResponse, error) {
	if payload == nil {
		return nil, errors.New("smtp provider: payload is required")
	}

	recipients := uniqueAddresses(payload.To, payload.CC, payload.BCC)
	if len(recipients) == 0 {
		return nil, errors.New("smtp provider: at least one recipient is required")
	}

	from := strings.TrimSpace(payload.From)
	if from == "" {
		from = p.from
	}

	envelopeFrom, err := normalizeEnvelopeAddress(from)
	if err != nil {
		return nil, fmt.Errorf("smtp provider: invalid from address: %w", err)
	}

	message, err := p.buildMessage(payload, from)
	if err != nil {
		return nil, err
	}

	envelopeRecipients, err := normalizeEnvelopeList(recipients)
	if err != nil {
		return nil, fmt.Errorf("smtp provider: invalid recipient: %w", err)
	}

	resp := &RawResponse{
		ID:        payload.MessageID,
		Timestamp: p.now(),
	}

	if err := p.deliver(ctx, envelopeFrom, envelopeRecipients, message); err != nil {
		code, body := classifySMTPError(err)
		resp.Code = code
		resp.Body = body
		if resp.Body == "" {
			resp.Body = err.Error()
		}
		return resp, err
	}

	resp.Code = 250
	if resp.Body == "" {
		resp.Body = "smtp: message accepted"
	}

	return resp, nil
}

func (p *SMTPProvider) deliver(ctx context.Context, from string, recipients []string, message []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	addr := net.JoinHostPort(p.host, strconv.Itoa(p.port))
	conn, err := p.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp provider: dial: %w", err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer close(done)

	client, err := smtp.NewClient(conn, p.host)
	if err != nil {
		return fmt.Errorf("smtp provider: new client: %w", err)
	}
	defer client.Close()

	if err := client.Hello(p.helloName); err != nil {
		return fmt.Errorf("smtp provider: hello: %w", err)
	}

	if cfg := p.sessionTLSConfig(); cfg != nil {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(cfg); err != nil {
				return fmt.Errorf("smtp provider: starttls: %w", err)
			}
		}
	}

	if p.auth != nil {
		if ok, _ := client.Extension("AUTH"); ok {
			if err := client.Auth(p.auth); err != nil {
				return fmt.Errorf("smtp provider: auth: %w", err)
			}
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp provider: mail from: %w", err)
	}

	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp provider: rcpt to %s: %w", rcpt, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp provider: data: %w", err)
	}

	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("smtp provider: data write: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp provider: data close: %w", err)
	}

	if err := client.Quit(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("smtp provider: quit: %w", err)
	}

	return ctx.Err()
}

func (p *SMTPProvider) buildMessage(payload *Payload, from string) ([]byte, error) {
	headers := make(map[string]string, len(payload.Headers)+6)
	for key, value := range payload.Headers {
		canonical := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(key))
		if canonical == "" || strings.TrimSpace(value) == "" {
			continue
		}
		headers[canonical] = sanitizeHeaderValue(value)
	}

	headers["From"] = from
	if len(payload.To) > 0 {
		headers["To"] = strings.Join(payload.To, ", ")
	}
	if len(payload.CC) > 0 {
		headers["Cc"] = strings.Join(payload.CC, ", ")
	} else {
		delete(headers, "Cc")
	}
	delete(headers, "Bcc")

	if _, ok := headers["Date"]; !ok {
		headers["Date"] = p.now().UTC().Format(time.RFC1123Z)
	}

	if payload.Subject != "" {
		headers["Subject"] = sanitizeHeaderValue(payload.Subject)
	}

	if payload.MessageID != "" {
		if _, exists := headers["Message-Id"]; !exists {
			headers["Message-Id"] = sanitizeHeaderValue(payload.MessageID)
		}
	}

	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = contentTypeFor(payload.BodyType)

	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, key := range keys {
		value := headers[key]
		if value == "" {
			continue
		}
		buf.WriteString(key)
		buf.WriteString(": ")
		buf.WriteString(value)
		buf.WriteString("\r\n")
	}
	buf.WriteString("\r\n")
	buf.WriteString(normalizeBody(payload.Body))

	return buf.Bytes(), nil
}

func (p *SMTPProvider) sessionTLSConfig() *tls.Config {
	if p.tlsConfig == nil {
		return nil
	}
	cfg := p.tlsConfig.Clone()
	if cfg.ServerName == "" {
		cfg.ServerName = p.host
	}
	return cfg
}

func contentTypeFor(bodyType string) string {
	switch strings.ToLower(strings.TrimSpace(bodyType)) {
	case "html":
		return "text/html; charset=UTF-8"
	case "text":
		return "text/plain; charset=UTF-8"
	default:
		return "text/plain; charset=UTF-8"
	}
}

func normalizeBody(body string) string {
	if body == "" {
		return ""
	}
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.ReplaceAll(normalized, "\n", "\r\n")
}

func sanitizeHeaderValue(value string) string {
	clean := strings.ReplaceAll(value, "\r", " ")
	clean = strings.ReplaceAll(clean, "\n", " ")
	return strings.TrimSpace(clean)
}

func uniqueAddresses(list ...[]string) []string {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for _, group := range list {
		for _, raw := range group {
			addr := strings.TrimSpace(raw)
			if addr == "" {
				continue
			}
			if _, ok := seen[addr]; ok {
				continue
			}
			seen[addr] = struct{}{}
			result = append(result, addr)
		}
	}
	return result
}

func normalizeEnvelopeList(addresses []string) ([]string, error) {
	result := make([]string, 0, len(addresses))
	for _, addr := range addresses {
		parsed, err := normalizeEnvelopeAddress(addr)
		if err != nil {
			return nil, err
		}
		result = append(result, parsed)
	}
	return result, nil
}

func normalizeEnvelopeAddress(value string) (string, error) {
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return "", err
	}
	if addr.Address == "" {
		return "", errors.New("empty address")
	}
	return addr.Address, nil
}

func classifySMTPError(err error) (int, string) {
	var tpErr *textproto.Error
	if errors.As(err, &tpErr) {
		return tpErr.Code, strings.TrimSpace(tpErr.Msg)
	}

	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return 0, "smtp: timeout"
	}

	return 0, ""
}
