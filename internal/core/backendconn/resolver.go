package backendconn

import (
	"context"
	"crypto/subtle"
	"errors"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// ErrUnknownBackendToken is returned when a presented backend credential does
// not identify a live BackendInstance.
var ErrUnknownBackendToken = errors.New("unknown backend credential")

// TokenResolver turns the bearer credential presented on Connect into the
// identity of a registered BackendInstance.
//
// Issuing those credentials is the job of the registration and claim flows of
// specification section 8, which are not implemented yet; this interface is the
// seam they will plug into.
type TokenResolver interface {
	Resolve(ctx context.Context, token string) (domain.BackendInstanceID, error)
}

// StaticTokenResolver maps fixed tokens to BackendInstance ids.
//
// It is a development stop-gap: real credentials are persistent, revocable and
// backed by the backend_instances table. An empty resolver rejects every
// connection, which is the default.
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
