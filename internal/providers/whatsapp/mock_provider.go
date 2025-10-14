package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Scenario enumerates supported behaviours for the mock WhatsApp provider.
type Scenario string

const (
	ScenarioSuccess   Scenario = "success"
	ScenarioTransient Scenario = "transient"
	ScenarioPermanent Scenario = "permanent"
	ScenarioTimeout   Scenario = "timeout"
)

// Option customises the mock provider at construction time.
type Option func(*MockProvider)

// WithScenario overrides the default scenario.
func WithScenario(s Scenario) Option {
	return func(p *MockProvider) {
		p.defaultScenario = s
	}
}

// WithLatency sets the artificial latency inserted before responding.
func WithLatency(d time.Duration) Option {
	return func(p *MockProvider) {
		if d < 0 {
			d = 0
		}
		p.latency = d
	}
}

// WithClock swaps out the clock for deterministic timestamps in tests.
func WithClock(now func() time.Time) Option {
	return func(p *MockProvider) {
		if now != nil {
			p.now = now
		}
	}
}

// MockProvider implements a deterministic WhatsApp provider suitable for tests.
type MockProvider struct {
	logger          zerolog.Logger
	defaultScenario Scenario
	latency         time.Duration
	now             func() time.Time

	mu  sync.Mutex
	rnd *rand.Rand
}

// NewMockProvider constructs a new mock WhatsApp provider.
func NewMockProvider(logger zerolog.Logger, opts ...Option) *MockProvider {
	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}
	p := &MockProvider{
		logger:          logger,
		defaultScenario: ScenarioSuccess,
		latency:         25 * time.Millisecond,
		now:             time.Now,
		rnd:             rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec G404
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// Send simulates sending a WhatsApp payload.
func (p *MockProvider) Send(ctx context.Context, payload *Payload) (*RawResponse, error) {
	if payload == nil {
		return nil, errors.New("whatsapp mock: payload is required")
	}
	if len(payload.To) == 0 {
		return nil, errors.New("whatsapp mock: at least one recipient is required")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if p.latency > 0 {
		timer := time.NewTimer(p.latency)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	scenario := p.defaultScenario
	if val, ok := payload.Meta["scenario"]; ok && strings.TrimSpace(val) != "" {
		scenario = Scenario(strings.ToLower(strings.TrimSpace(val)))
	}

	resp := &RawResponse{
		ID:        p.generateID(payload.MessageID),
		Code:      200,
		Status:    "accepted",
		Body:      "mock: message accepted",
		Timestamp: p.now(),
	}

	switch scenario {
	case ScenarioSuccess:
		return resp, nil
	case ScenarioTransient:
		resp.Code = 429
		resp.Status = "transient_failure"
		resp.Body = "mock: transient failure"
		return resp, fmt.Errorf("whatsapp mock transient error: rate limited")
	case ScenarioPermanent:
		resp.Code = 550
		resp.Status = "permanent_failure"
		resp.Body = "mock: permanent failure"
		return resp, fmt.Errorf("whatsapp mock permanent error: invalid recipient")
	case ScenarioTimeout:
		timer := time.NewTimer(p.latency)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-timer.C:
			return resp, fmt.Errorf("whatsapp mock timeout")
		}
	default:
		resp.Status = "unknown"
		resp.Body = "mock: unknown scenario"
		return resp, fmt.Errorf("whatsapp mock unknown scenario: %s", scenario)
	}
}

func (p *MockProvider) generateID(suggested string) string {
	if strings.TrimSpace(suggested) != "" {
		return suggested
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return fmt.Sprintf("wa-%d", p.rnd.Int63())
}
