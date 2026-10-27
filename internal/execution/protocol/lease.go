package protocol

import (
	"context"
	"time"

	"github.com/rclsilver/threavia/internal/execution/bridge"
)

type Lease struct{ ExpiresAt time.Time }

// KeepAlive renews the one worker attached to this stream. Absolute expiry
// prevents queued old heartbeats from keeping an orphan alive after a partition.
func KeepAlive(ctx context.Context, peer *bridge.Peer) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			callCtx, stop := context.WithTimeout(ctx, 5*time.Second)
			err := peer.Call(callCtx, "lease", Lease{ExpiresAt: time.Now().Add(20 * time.Second)}, nil)
			stop()
			if err != nil {
				_ = peer.Close()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-peer.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return cancel
}
