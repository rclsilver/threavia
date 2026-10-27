package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/rclsilver/threavia/internal/execution/bridge"
	"github.com/rclsilver/threavia/internal/execution/protocol"
)

type lease struct {
	mu      sync.Mutex
	expires time.Time
}

func newLease() *lease { return &lease{expires: time.Now().Add(30 * time.Second)} }
func (l *lease) renew(raw json.RawMessage) error {
	var input protocol.Lease
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.expires.After(now) || !input.ExpiresAt.After(now) || input.ExpiresAt.After(now.Add(30*time.Second)) {
		return errors.New("invalid worker lease")
	}
	if input.ExpiresAt.After(l.expires) {
		l.expires = input.ExpiresAt
	}
	return nil
}
func (l *lease) watch(ctx context.Context, peer *bridge.Peer) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-peer.Done():
			return
		case <-ticker.C:
			l.mu.Lock()
			expired := !l.expires.After(time.Now())
			l.mu.Unlock()
			if expired {
				_ = peer.Close()
				return
			}
		}
	}
}
