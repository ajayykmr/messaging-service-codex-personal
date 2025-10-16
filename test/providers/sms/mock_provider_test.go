package sms_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	smsprovider "github.com/ajayykmr/messaging-service-go/internal/providers/sms"
)

func TestMockProviderSuccess(t *testing.T) {
	fixed := time.Date(2025, time.January, 1, 10, 0, 0, 0, time.UTC)
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithClock(func() time.Time { return fixed }), smsprovider.WithLatency(0))

	payload := &smsprovider.Payload{
		MessageID: "msg-1",
		From:      "+10000000000",
		To:        []string{"+10000000001"},
		Body:      "hello",
	}

	resp, err := provider.Send(context.Background(), payload)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if resp.Code != 200 || resp.Status != "accepted" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Timestamp != fixed {
		t.Fatalf("expected fixed timestamp, got %v", resp.Timestamp)
	}
}

func TestMockProviderTransientFailure(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithLatency(0))

	payload := &smsprovider.Payload{
		MessageID: "msg-2",
		From:      "+10000000000",
		To:        []string{"+10000000001"},
		Body:      "hello",
		Meta:      map[string]string{"scenario": string(smsprovider.ScenarioTransient)},
	}

	resp, err := provider.Send(context.Background(), payload)
	if err == nil {
		t.Fatalf("expected error for transient scenario")
	}
	if resp.Code != 429 || resp.Status != "transient_failure" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestMockProviderPermanentFailure(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithLatency(0))

	payload := &smsprovider.Payload{
		MessageID: "msg-3",
		From:      "+10000000000",
		To:        []string{"+10000000001"},
		Body:      "hello",
		Meta:      map[string]string{"scenario": string(smsprovider.ScenarioPermanent)},
	}

	resp, err := provider.Send(context.Background(), payload)
	if err == nil {
		t.Fatalf("expected error for permanent scenario")
	}
	if resp.Code != 550 || resp.Status != "permanent_failure" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !strings.Contains(err.Error(), "permanent") {
		t.Fatalf("expected permanent error message, got %v", err)
	}
}

func TestMockProviderTimeoutScenario(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithLatency(25*time.Millisecond))

	payload := &smsprovider.Payload{
		MessageID: "msg-4",
		From:      "+10000000000",
		To:        []string{"+10000000001"},
		Body:      "hello",
		Meta:      map[string]string{"scenario": string(smsprovider.ScenarioTimeout)},
	}

	start := time.Now()
	_, err := provider.Send(context.Background(), payload)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatalf("expected delay for timeout scenario")
	}
}

func TestMockProviderRespectsContextCancellation(t *testing.T) {
	provider := smsprovider.NewMockProvider(zerolog.Nop(), smsprovider.WithLatency(50*time.Millisecond))

	payload := &smsprovider.Payload{
		MessageID: "msg-5",
		From:      "+10000000000",
		To:        []string{"+10000000001"},
		Body:      "hello",
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := provider.Send(ctx, payload); err == nil {
		t.Fatalf("expected context cancellation error")
	}
}
