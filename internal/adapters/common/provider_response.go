package common

// ProviderResponse captures normalized provider information exchanged between
// adapters and the worker engine.
type ProviderResponse struct {
Status  string            `json:"status"`
Code    *int              `json:"code,omitempty"`
Message string            `json:"message,omitempty"`
Raw     string            `json:"raw,omitempty"`
Meta    map[string]string `json:"meta,omitempty"`
}
