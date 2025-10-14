package common

import "context"

// Adapter defines the behaviour required from channel adapters. Adapters are
// responsible for converting the domain model into provider specific payloads
// and returning a normalized ProviderResponse alongside error classification.
type Adapter interface {
	Send(ctx context.Context, msg *ValidatedMessage) (*ProviderResponse, error)
}
