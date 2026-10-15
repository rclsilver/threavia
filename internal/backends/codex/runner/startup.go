package runner

import (
	"context"
	"fmt"
)

// Prefer an observed handshake when the deadline and hook become ready
// together. PreToolUse independently keeps refusing tools until confirmation.
func waitPolicyHook(ctx context.Context, ready <-chan struct{}, r *rpc) error {
	select {
	case <-ready:
		return nil
	default:
	}
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
		select {
		case <-ready:
			return nil
		default:
		}
		return fmt.Errorf("Codex did not confirm the Threavia policy hook; no tools were allowed before confirmation: %w", ctx.Err())
	case <-r.done:
		return fmt.Errorf("Codex exited before confirming the Threavia policy hook: %w", r.err)
	}
}
