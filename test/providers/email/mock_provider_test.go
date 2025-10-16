package email_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	emailprovider "github.com/ajayykmr/messaging-service-go/internal/providers/email"
)

func TestMockProviderSuccess(t *testing.T) {
	fixed := time.Date(2025, time.October, 11, 10, 0, 0, 0, time.UTC)
	provider := emailprovider.NewMockProvider(
		zerolog.Nop(),
		emailprovider.WithLatencyRange(0, 0),
		emailprovider.WithClock(func() time.Time { return fixed }),
	)

	payload := &emailprovider.Payload{
		MessageID: "message-123",
		From:      "noreply@example.com",
		To:        []string{"user@example.com"},
		Headers:   map[string]string{},
	}

	resp, err := provider.Send(context.Background(), payload)
	if err != nil {
		t.Fatalf("expected success, got error %v", err)
	}
	if resp == nil {
		t.Fatalf("expected response, got nil")
	}
	if resp.Code != 250 {
		t.Fatalf("expected SMTP 250, got %d", resp.Code)
	}
	if resp.Body != "mock: message queued" {
		t.Fatalf("unexpected body %q", resp.Body)
	}
	if resp.Timestamp != fixed {
		t.Fatalf("expected fixed timestamp, got %v", resp.Timestamp)
	}
	if resp.ID != payload.MessageID {
		t.Fatalf("expected response id to match message id, got %q", resp.ID)
	}
}

func TestMockProviderScenarioOverrides(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop(), emailprovider.WithLatencyRange(0, 0))

	payload := &emailprovider.Payload{
		MessageID: "message-123",
		From:      "noreply@example.com",
		To:        []string{"user@example.com"},
		Headers: map[string]string{
			"X-Mock-Provider-Scenario": string(emailprovider.ScenarioPermanent),
		},
	}

	resp, err := provider.Send(context.Background(), payload)
	if err == nil {
		t.Fatalf("expected error for permanent scenario")
	}
	if resp == nil {
		t.Fatalf("expected response even when error occurs")
	}
	if resp.Code != 550 {
		t.Fatalf("expected SMTP 550, got %d", resp.Code)
	}
	if !strings.Contains(err.Error(), "smtp 550") {
		t.Fatalf("expected error to contain smtp code, got %v", err)
	}
}

func TestMockProviderTransientScenario(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop(), emailprovider.WithLatencyRange(0, 0))
	payload := &emailprovider.Payload{
		MessageID: "message-123",
		From:      "noreply@example.com",
		To:        []string{"user@example.com"},
		Headers: map[string]string{
			"X-Mock-Provider-Scenario": string(emailprovider.ScenarioTransient),
		},
	}

	resp, err := provider.Send(context.Background(), payload)
	if err == nil {
		t.Fatalf("expected error for transient scenario")
	}
	if resp == nil || resp.Code != 451 {
		t.Fatalf("expected SMTP 451 response, got %+v", resp)
	}
}

func TestMockProviderFailureAttemptsBeforeSuccess(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop(), emailprovider.WithLatencyRange(0, 0))
	payload := func() *emailprovider.Payload {
		return &emailprovider.Payload{
			MessageID: "retry-message",
			From:      "noreply@example.com",
			To:        []string{"user@example.com"},
			Headers: map[string]string{
				"X-Mock-Provider-Failure-Attempts": "2",
			},
		}
	}

	ctx := context.Background()

	if _, err := provider.Send(ctx, payload()); err == nil {
		t.Fatalf("expected first attempt to fail")
	}
	if _, err := provider.Send(ctx, payload()); err == nil {
		t.Fatalf("expected second attempt to fail")
	}

	resp, err := provider.Send(ctx, payload())
	if err != nil {
		t.Fatalf("expected third attempt to succeed, got error %v", err)
	}
	if resp == nil || resp.Code != 250 {
		t.Fatalf("expected SMTP 250 response after retries, got %+v", resp)
	}
}

func TestMockProviderTimeoutScenario(t *testing.T) {
	provider := emailprovider.NewMockProvider(
		zerolog.Nop(),
		emailprovider.WithLatencyRange(10*time.Millisecond, 10*time.Millisecond),
	)

	payload := &emailprovider.Payload{
		MessageID: "message-123",
		From:      "noreply@example.com",
		To:        []string{"user@example.com"},
		Headers: map[string]string{
			"X-Mock-Provider-Scenario": string(emailprovider.ScenarioTimeout),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := provider.Send(ctx, payload)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

func TestMockProviderLatencyHeaderOverride(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop(), emailprovider.WithLatencyRange(0, 0))

	payload := &emailprovider.Payload{
		MessageID: "message-123",
		From:      "noreply@example.com",
		To:        []string{"user@example.com"},
		Headers: map[string]string{
			"X-Mock-Provider-Latency": "15ms",
		},
	}

	start := time.Now()
	if _, err := provider.Send(context.Background(), payload); err != nil {
		t.Fatalf("unexpected send error %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 15*time.Millisecond {
		t.Fatalf("expected latency override of at least 15ms, got %v", elapsed)
	}
}

func TestMockProviderDefaultScenarioOption(t *testing.T) {
	provider := emailprovider.NewMockProvider(
		zerolog.Nop(),
		emailprovider.WithLatencyRange(0, 0),
		emailprovider.WithDefaultScenario(emailprovider.ScenarioPermanent),
	)

	payload := &emailprovider.Payload{
		MessageID: "message-123",
		From:      "noreply@example.com",
		To:        []string{"user@example.com"},
	}

	_, err := provider.Send(context.Background(), payload)
	if !strings.Contains(err.Error(), "smtp 550") {
		t.Fatalf("expected default permanent scenario to trigger 550, got %v", err)
	}
}

func TestMockProviderRandomSeedDeterminism(t *testing.T) {
	provider := emailprovider.NewMockProvider(
		zerolog.Nop(),
		emailprovider.WithLatencyRange(0, 0),
		emailprovider.WithRandomSeed(42),
	)

	payload := &emailprovider.Payload{
		From: "noreply@example.com",
		To:   []string{"user@example.com"},
	}

	resp1, err := provider.Send(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	resp2, err := provider.Send(context.Background(), payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp1.ID == resp2.ID {
		t.Fatalf("expected unique ids between calls, got %q and %q", resp1.ID, resp2.ID)
	}
}

func TestMockProviderPayloadValidation(t *testing.T) {
	provider := emailprovider.NewMockProvider(zerolog.Nop())

	if _, err := provider.Send(context.Background(), nil); err == nil {
		t.Fatalf("expected error for nil payload")
	}

	payload := &emailprovider.Payload{
		MessageID: "message-123",
		From:      "noreply@example.com",
	}
	if _, err := provider.Send(context.Background(), payload); err == nil {
		t.Fatalf("expected error when recipients are missing")
	}
}
