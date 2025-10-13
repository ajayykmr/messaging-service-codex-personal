package email

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

// Scenario enumerates the supported mock behaviours. The default scenario is
// success unless overridden via headers or options.
type Scenario string

const (
	ScenarioSuccess   Scenario = "success"
	ScenarioTransient Scenario = "transient"
	ScenarioPermanent Scenario = "permanent"
	ScenarioTimeout   Scenario = "timeout"

	headerScenario = "X-Mock-Provider-Scenario"
	headerLatency  = "X-Mock-Provider-Latency"
)

// Payload is the canonical representation of an outbound email passed to the
// provider. Adapters are expected to normalize their inputs to this structure.
type Payload struct {
	MessageID string
	From      string
	To        []string
	CC        []string
	BCC       []string
	Subject   string
	BodyType  string
	Body      string
	Headers   map[string]string
}

// RawResponse mirrors the low level provider response that adapters inspect to
// derive higher level ProviderResponse values.
type RawResponse struct {
	ID        string
	Code      int
	Body      string
	Timestamp time.Time
}

// Provider is the contract exposed by the email provider implementation.
type Provider interface {
	Send(ctx context.Context, payload *Payload) (*RawResponse, error)
}

// Option customizes the behaviour of the mock provider at construction time.
type Option func(*MockProvider)

// WithLatencyRange overrides the default latency range used by the mock
// provider when simulating work. Negative values are clamped to zero and if
// max < min it is coerced to min to keep behaviour deterministic.
func WithLatencyRange(min, max time.Duration) Option {
	return func(p *MockProvider) {
		if min < 0 {
			min = 0
		}
		if max < 0 {
			max = 0
		}
		if max < min {
			max = min
		}
		p.minLatency = min
		p.maxLatency = max
	}
}

// WithDefaultScenario configures the default behaviour when a payload does not
// specify an explicit scenario via headers.
func WithDefaultScenario(s Scenario) Option {
	return func(p *MockProvider) {
		p.defaultScenario = s
	}
}

// WithRandomSeed swaps the RNG seed used when generating provider identifiers.
func WithRandomSeed(seed int64) Option {
	return func(p *MockProvider) {
		p.rnd = rand.New(rand.NewSource(seed)) // #nosec G404 -- deterministic seed for tests.
	}
}

// WithClock overrides the clock used for timestamps, useful for deterministic
// unit tests.
func WithClock(now func() time.Time) Option {
	return func(p *MockProvider) {
		if now != nil {
			p.now = now
		}
	}
}

// MockProvider implements a deterministic SMTP provider suitable for local
// development and automated testing. Behaviour can be controlled via options
// and per-request headers without making real network calls.
type MockProvider struct {
	logger          zerolog.Logger
	minLatency      time.Duration
	maxLatency      time.Duration
	defaultScenario Scenario
	now             func() time.Time

	mu  sync.Mutex
	rnd *rand.Rand
}

// NewMockProvider constructs a mock SMTP provider instance using sensible
// defaults. By default it emits successes with a latency between 25ms and 75ms.
func NewMockProvider(logger zerolog.Logger, opts ...Option) *MockProvider {
	if reflect.ValueOf(logger).IsZero() {
		logger = zerolog.Nop()
	}

	p := &MockProvider{
		logger:          logger,
		minLatency:      25 * time.Millisecond,
		maxLatency:      75 * time.Millisecond,
		defaultScenario: ScenarioSuccess,
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

// Send simulates delivering the supplied payload, returning a deterministic
// response. The behaviour is controllable via the X-Mock-Provider-* headers.
func (p *MockProvider) Send(ctx context.Context, payload *Payload) (*RawResponse, error) {
	if payload == nil {
		return nil, errors.New("email: payload is required")
	}
	if len(payload.To)+len(payload.CC)+len(payload.BCC) == 0 {
		return nil, errors.New("email: at least one recipient is required")
	}

	latency := p.sampleLatency(payload)
	if latency > 0 {
		if err := p.sleep(ctx, latency); err != nil {
			return nil, err
		}
	}

	scenario := p.resolveScenario(payload)
	p.logger.Debug().
		Str("provider", "mock_smtp").
		Str("scenario", string(scenario)).
		Str("message_id", payload.MessageID).
		Msg("mock email provider invoked")

	switch scenario {
	case ScenarioPermanent:
		resp := p.baseResponse(payload, 550, "mock: mailbox unavailable")
		return resp, fmt.Errorf("smtp %d: %s", resp.Code, resp.Body)
	case ScenarioTransient:
		resp := p.baseResponse(payload, 451, "mock: requested action aborted, try again later")
		return resp, fmt.Errorf("smtp %d: %s", resp.Code, resp.Body)
	case ScenarioTimeout:
		if err := p.sleep(ctx, p.maxLatency+p.minLatency); err != nil {
			return nil, err
		}
		return nil, context.DeadlineExceeded
	default:
		resp := p.baseResponse(payload, 250, "mock: message queued")
		return resp, nil
	}
}

func (p *MockProvider) resolveScenario(payload *Payload) Scenario {
	value, ok := pickHeader(payload.Headers, headerScenario)
	if !ok || value == "" {
		return p.defaultScenario
	}

	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(ScenarioPermanent):
		return ScenarioPermanent
	case string(ScenarioTransient):
		return ScenarioTransient
	case string(ScenarioTimeout):
		return ScenarioTimeout
	default:
		return ScenarioSuccess
	}
}

func (p *MockProvider) sampleLatency(payload *Payload) time.Duration {
	if value, ok := pickHeader(payload.Headers, headerLatency); ok && value != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(value)); err == nil && d >= 0 {
			return d
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.minLatency == p.maxLatency {
		return p.minLatency
	}

	min := p.minLatency
	max := p.maxLatency
	if max <= min {
		return min
	}

	delta := max - min
	return min + time.Duration(p.rnd.Int63n(int64(delta)+1))
}

func (p *MockProvider) baseResponse(payload *Payload, code int, body string) *RawResponse {
	respID := payload.MessageID
	if respID == "" {
		respID = p.nextID()
	}

	return &RawResponse{
		ID:        respID,
		Code:      code,
		Body:      body,
		Timestamp: p.now(),
	}
}

func (p *MockProvider) nextID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return fmt.Sprintf("mock-%08x", p.rnd.Uint32())
}

func (p *MockProvider) sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func pickHeader(headers map[string]string, key string) (string, bool) {
	for k, v := range headers {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}
