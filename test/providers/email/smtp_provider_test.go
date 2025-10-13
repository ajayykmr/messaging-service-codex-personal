package email_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/example/messaging-microservice/internal/config"
	emailprovider "github.com/example/messaging-microservice/internal/providers/email"
)

func TestNewSMTPProviderValidation(t *testing.T) {
	logger := zerolog.New(io.Discard)

	tests := []struct {
		name string
		cfg  config.SMTPConfig
	}{
		{
			name: "missing host",
			cfg: config.SMTPConfig{
				Host: "",
				Port: 25,
				From: "noreply@example.com",
			},
		},
		{
			name: "invalid port",
			cfg: config.SMTPConfig{
				Host: "smtp.example.com",
				Port: 0,
				From: "noreply@example.com",
			},
		},
		{
			name: "missing from",
			cfg: config.SMTPConfig{
				Host: "smtp.example.com",
				Port: 25,
				From: "",
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if _, err := emailprovider.NewSMTPProvider(tc.cfg, logger); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestSendNilPayload(t *testing.T) {
	logger := zerolog.New(io.Discard)
	cfg := config.SMTPConfig{
		Host: "smtp.example.com",
		Port: 2525,
		From: "noreply@example.com",
	}

	provider, err := emailprovider.NewSMTPProvider(cfg, logger, emailprovider.WithSMTPTLSConfig(nil))
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	if _, err := provider.Send(context.Background(), nil); err == nil {
		t.Fatalf("expected error when payload is nil")
	}
}

func TestSMTPProviderSendNormalizesMessage(t *testing.T) {
	logger := zerolog.New(io.Discard)
	cfg := config.SMTPConfig{
		Host: "smtp.example.com",
		Port: 2525,
		From: "noreply@example.com",
	}

	var (
		waitFn     func()
		transcript *smtpTranscript
	)
	defer func() {
		if waitFn != nil {
			waitFn()
		}
	}()

	dialer := dialerFunc(func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, tr, wait := startFakeSMTPServer(t, cfg.From, []string{"recipient@example.com"})
		transcript = tr
		waitFn = wait
		return conn, nil
	})

	provider, err := emailprovider.NewSMTPProvider(cfg, logger,
		emailprovider.WithSMTPTLSConfig(nil),
		emailprovider.WithSMTPDialer(dialer),
	)
	if err != nil {
		t.Fatalf("unexpected error creating provider: %v", err)
	}

	payload := &emailprovider.Payload{
		MessageID: "msg-1",
		To:        []string{"recipient@example.com", "recipient@example.com"},
		CC:        []string{"recipient@example.com"}, // duplicate to exercise uniqueAddresses.
		BCC:       []string{"bcc@example.com"},
		Subject:   "Greetings",
		BodyType:  "html",
		Body:      "Line 1\nLine 2\r\nLine 3",
		Headers: map[string]string{
			"From": "spoof@example.com",
			"Cc":   "cc-header@example.com",
			"Bcc":  "bcc-header@example.com",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := provider.Send(ctx, payload)
	if err != nil {
		t.Fatalf("unexpected send error: %v", err)
	}

	if resp == nil || resp.Code != 250 {
		t.Fatalf("expected response code 250, got %#v", resp)
	}
	if resp.Body != "smtp: message accepted" {
		t.Fatalf("unexpected response body: %q", resp.Body)
	}

	if transcript == nil {
		t.Fatalf("expected transcript to be captured")
	}
	if transcript.mailFrom != cfg.From {
		t.Fatalf("expected MAIL FROM %q, got %q", cfg.From, transcript.mailFrom)
	}

	wantRecipients := []string{"recipient@example.com", "bcc@example.com"}
	if !reflect.DeepEqual(transcript.rcpts, wantRecipients) {
		t.Fatalf("unexpected rcpt to list: got %v, want %v", transcript.rcpts, wantRecipients)
	}

	data := transcript.data
	if !strings.Contains(data, "From: noreply@example.com") {
		t.Fatalf("expected From header to use configured from, got %q", data)
	}
	if strings.Contains(data, "spoof@example.com") {
		t.Fatalf("expected spoof From header to be overridden, got %q", data)
	}
	if strings.Contains(data, "cc-header@example.com") {
		t.Fatalf("expected custom Cc header to be removed when payload.CC empty in message, got %q", data)
	}
	if strings.Contains(data, "bcc-header@example.com") {
		t.Fatalf("expected Bcc header to be stripped, got %q", data)
	}
	if !strings.Contains(data, "Content-Type: text/html; charset=UTF-8") {
		t.Fatalf("expected html content type, got %q", data)
	}
	if !strings.Contains(data, "Line 1\r\nLine 2\r\nLine 3") {
		t.Fatalf("expected body with CRLF normalization, got %q", data)
	}
}

// Helpers.

type dialerFunc func(ctx context.Context, network, address string) (net.Conn, error)

func (d dialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d(ctx, network, address)
}

type smtpTranscript struct {
	mailFrom string
	rcpts    []string
	data     string
}

func startFakeSMTPServer(t *testing.T, expectedFrom string, expectedRecipients []string) (net.Conn, *smtpTranscript, func()) {
	t.Helper()

	server, client := net.Pipe()
	transcript := &smtpTranscript{}
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		defer server.Close()
		if err := runFakeSMTPConversation(t, server, expectedFrom, expectedRecipients, transcript); err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("fake smtp server: %v", err)
		}
	}()

	wait := func() {
		wg.Wait()
	}

	return client, transcript, wait
}

func runFakeSMTPConversation(t *testing.T, conn net.Conn, expectedFrom string, expectedRecipients []string, transcript *smtpTranscript) error {
	writer := bufio.NewWriter(conn)
	reader := bufio.NewReader(conn)

	writeLine := func(format string, args ...interface{}) error {
		if _, err := fmt.Fprintf(writer, format+"\r\n", args...); err != nil {
			return err
		}
		return writer.Flush()
	}

	if err := writeLine("220 fake smtp ready"); err != nil {
		return err
	}

	rcptIndex := 0

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO ") || strings.HasPrefix(upper, "HELO "):
			if err := writeLine("250-fake"); err != nil {
				return err
			}
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			addr := extractSMTPAddress(line)
			transcript.mailFrom = addr
			if expectedFrom != "" && addr != expectedFrom {
				t.Errorf("unexpected MAIL FROM: got %s, want %s", addr, expectedFrom)
			}
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case strings.HasPrefix(upper, "RCPT TO:"):
			addr := extractSMTPAddress(line)
			if rcptIndex < len(expectedRecipients) && addr != expectedRecipients[rcptIndex] {
				t.Errorf("unexpected RCPT TO: got %s, want %s", addr, expectedRecipients[rcptIndex])
			}
			transcript.rcpts = append(transcript.rcpts, addr)
			rcptIndex++
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "DATA":
			if err := writeLine("354 Start mail input; end with <CRLF>.<CRLF>"); err != nil {
				return err
			}
			var data strings.Builder
			for {
				msgLine, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				if msgLine == ".\r\n" {
					break
				}
				data.WriteString(msgLine)
			}
			transcript.data = data.String()
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		case upper == "QUIT":
			if err := writeLine("221 Bye"); err != nil {
				return err
			}
			return nil
		default:
			if err := writeLine("250 OK"); err != nil {
				return err
			}
		}
	}
}

func extractSMTPAddress(line string) string {
	start := strings.Index(line, "<")
	end := strings.Index(line, ">")
	if start != -1 && end != -1 && end > start+1 {
		return strings.TrimSpace(line[start+1 : end])
	}
	if idx := strings.Index(line, ":"); idx != -1 && idx+1 < len(line) {
		return strings.TrimSpace(line[idx+1:])
	}
	return strings.TrimSpace(line)
}
