package backendconn

import (
	"context"
	"crypto/subtle"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// StaticTokenResolver maps fixed tokens to BackendInstance ids.
//
// Production credentials are persistent, revocable and backed by the
// backend_instances table, which is what the service implements. This is the
// test double for the transport layer, which only cares that something turns a
// bearer token into an identity.
type StaticTokenResolver struct {
	tokens map[string]domain.BackendInstanceID
}

// NewStaticTokenResolver builds a resolver from a token -> BackendInstance id
// mapping. Entries with an empty token or an empty id are ignored.
func NewStaticTokenResolver(tokens map[string]string) *StaticTokenResolver {
	resolved := make(map[string]domain.BackendInstanceID, len(tokens))
	for token, instanceID := range tokens {
		if token == "" || instanceID == "" {
			continue
		}
		resolved[token] = domain.BackendInstanceID(instanceID)
	}
	return &StaticTokenResolver{tokens: resolved}
}

// Len returns the number of configured tokens.
func (r *StaticTokenResolver) Len() int { return len(r.tokens) }

// Resolve implements TokenResolver. Every candidate is compared in constant
// time so that a wrong token leaks no timing information.
func (r *StaticTokenResolver) Resolve(_ context.Context, token string) (domain.BackendInstanceID, error) {
	var match domain.BackendInstanceID
	found := 0
	for candidate, instanceID := range r.tokens {
		if subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
			match = instanceID
			found = 1
		}
	}
	if found == 0 {
		return "", ErrUnknownBackendToken
	}
	return match, nil
}
