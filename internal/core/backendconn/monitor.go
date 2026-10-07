package backendconn

import (
	"context"
	"log/slog"
	"time"
)

// Monitor infers OFFLINE from a missing heartbeat, the half of specification
// section 7 that a dropped connection cannot cover.
//
// Three things can go wrong with a backend, and they are not caught by the same
// mechanism. A clean disconnection ends the stream. A dead peer — host gone,
// network black hole, process frozen — stops acknowledging the gRPC keepalive
// pings, and the transport tears the connection down on its own, usually faster
// than any deadline here. What is left is a backend whose transport is perfectly
// healthy, answering pings, while the application behind it has stopped: a
// wedged event loop, or an implementation that simply never heartbeats. Nothing
// below the application layer can see that, and without this the instance would
// stay READY forever while Core kept dispatching work into a hole.
//
// Closing the connection is all this does. The ordinary disconnection path then
// marks the instance OFFLINE and parks its Jobs, and the backend reconnects and
// reconciles, so a connection has one way to end rather than two.
//
// It returns when ctx is done.
func (s *Server) Monitor(ctx context.Context) {
	if s.opts.OfflineAfter <= 0 {
		s.logger.Warn("backend liveness monitoring is disabled")
		return
	}

	// Sweeping at the heartbeat interval detects a silent backend within one
	// interval of the deadline, without polling faster than backends speak.
	ticker := time.NewTicker(s.opts.HeartbeatInterval)
	defer ticker.Stop()

	s.logger.Info("watching backend liveness",
		slog.Duration("every", s.opts.HeartbeatInterval),
		slog.Duration("offlineAfter", s.opts.OfflineAfter))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep()
		}
	}
}

// sweep closes every connection that stopped heartbeating.
func (s *Server) sweep() {
	for _, conn := range s.registry.Stale(s.opts.OfflineAfter) {
		s.logger.Warn("backend stopped heartbeating, closing its connection",
			slog.String("backendInstanceId", string(conn.BackendInstanceID())),
			slog.String("connectionId", conn.ID()),
			slog.Duration("silentFor", time.Since(conn.lastSignal())))
		conn.close(ReasonStale)
	}
}
