package backendconn

import (
	"context"
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
// specification section 8, which the Core service implements. This interface is
// the seam between them, and is what keeps this package free of any knowledge of
// how a credential is stored or verified.
type TokenResolver interface {
	Resolve(ctx context.Context, token string) (domain.BackendInstanceID, error)
}
