package sms

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

// Scenario enumerates the mock behaviours supported by the SMS provider.
type Scenario string

const (
	ScenarioSuccess   Scenario = "success"
	ScenarioTransient Scenario = "transient"
	ScenarioPermanent Scenario = "permanent"
	ScenarioTimeout   Scenario = "timeout"
)

// Option customises the mock provider.
type Option func(*MockProvider)

// WithScenario sets the default scenario used when a payload does not specify one.
func WithScenario(s Scenario) Option {
	return func(p *MockProvider) {
		p.defaultScenario = s
	}
}

// WithLatency configures the artificial latency injected before sending.
func WithLatency(d time.Duration) Option {
	return func(p *MockProvider) {
		if d < 0 {
			d = 0
		}
		p.latency = d
	}
}

// WithClock overrides the clock used to timestamp responses (useful for tests).
func WithClock(now func() time.Time) Option {
	return func(p *MockProvider) {
		if now != nil {
			p.now = now
		}
	}
}

// MockProvider is a deterministic SMS provider used for tests.
type MockProvider struct {
	logger          zerolog.Logger
	defaultScenario Scenario
	latency         time.Duration
	now             func() time.Time

	mu  sync.Mutex
	rnd *rand.Rand
}

// NewMockProvider constructs a mock SMS provider.
func NewMockProvider(logger zerolog.Logger, opts ...Option) *MockProvider {
	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}
	p := &MockProvider{
		logger:          logger,
		defaultScenario: ScenarioSuccess,
		latency:         25 * time.Millisecond,
		now:             time.Now,
		rnd:             rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec G404 -- predictable in tests.
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// Send simulates sending an SMS payload according to the configured scenario.
func (p *MockProvider) Send(ctx context.Context, payload *Payload) (*RawResponse, error) {
	if payload == nil {
		return nil, errors.New("sms mock: payload is required")
	}
	if len(payload.To) == 0 {
		return nil, errors.New("sms mock: at least one recipient is required")
	}

	// honour context cancellation before work begins
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

	response := &RawResponse{
		ID:        p.generateID(payload.MessageID),
		Code:      200,
		Status:    "accepted",
		Body:      "mock: message accepted",
		Timestamp: p.now(),
	}

	switch scenario {
	case ScenarioSuccess:
		return response, nil
	case ScenarioTransient:
		response.Code = 429
		response.Status = "transient_failure"
		response.Body = "mock: transient failure"
		return response, fmt.Errorf("sms mock transient error: rate limited")
	case ScenarioPermanent:
		response.Code = 550
		response.Status = "permanent_failure"
		response.Body = "mock: permanent failure"
		return response, fmt.Errorf("sms mock permanent error: invalid recipient")
	case ScenarioTimeout:
		// Simulate a timeout by waiting until the context expires.
		timer := time.NewTimer(p.latency)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return response, ctx.Err()
		case <-timer.C:
			return response, fmt.Errorf("sms mock timeout")
		}
	default:
		response.Status = "unknown"
		response.Body = "mock: unknown scenario"
		return response, fmt.Errorf("sms mock unknown scenario: %s", scenario)
	}
}

func (p *MockProvider) generateID(suggested string) string {
	if strings.TrimSpace(suggested) != "" {
		return suggested
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return fmt.Sprintf("sms-%d", p.rnd.Int63())
}
