// Package backendconn implements the Core side of the Backend control protocol:
// the single long-lived bidirectional gRPC stream each BackendInstance opens
// outbound to Core (THREAVIA_SPEC_V1.md sections 9 and 26).
package backendconn

import (
	"sync"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// ProtocolVersion is the wire protocol version Core speaks. It is defined once,
// in the protocol itself.
const ProtocolVersion = uint32(backendv1.ProtocolVersion_PROTOCOL_VERSION_V1)

// sendBuffer is the number of commands that may be queued towards a backend
// before the control stream is considered stalled.
const sendBuffer = 64

// CloseReason explains why a control connection was closed.
type CloseReason string

const (
	// ReasonSuperseded means a newer connection took over the lease.
	ReasonSuperseded CloseReason = "superseded"
	// ReasonReleased means the stream ended on its own.
	ReasonReleased CloseReason = "released"
	// ReasonShutdown means Core is shutting down.
	ReasonShutdown CloseReason = "shutdown"
)

// Connection is one active control stream and its lease.
//
// Only one connection is active per BackendInstance: a newly authenticated
// connection receives a new connection id and supersedes the previous one for
// new commands (spec section 9).
type Connection struct {
	id          string
	instanceID  domain.BackendInstanceID
	connectedAt time.Time

	send chan *backendv1.CoreToBackend
	done chan struct{}

	mu            sync.Mutex
	lastHeartbeat time.Time
	closed        bool
	reason        CloseReason
}

// ID returns the connection lease id.
func (c *Connection) ID() string { return c.id }

// BackendInstanceID returns the authenticated backend this connection belongs to.
func (c *Connection) BackendInstanceID() domain.BackendInstanceID { return c.instanceID }

// ConnectedAt returns when the connection was established.
func (c *Connection) ConnectedAt() time.Time { return c.connectedAt }

// Done is closed when the connection must stop serving.
func (c *Connection) Done() <-chan struct{} { return c.done }

// Reason explains why the connection was closed.
func (c *Connection) Reason() CloseReason {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason
}

// Outbound is the queue the stream writer drains.
func (c *Connection) Outbound() <-chan *backendv1.CoreToBackend { return c.send }

// Send queues a command towards the backend. It reports false when the
// connection is gone or its outbound queue is full.
func (c *Connection) Send(msg *backendv1.CoreToBackend) bool {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return false
	}
	select {
	case c.send <- msg:
		return true
	case <-c.done:
		return false
	default:
		return false
	}
}

// NoteHeartbeat records the arrival of a backend heartbeat.
func (c *Connection) NoteHeartbeat(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastHeartbeat = at
}

// LastHeartbeat returns the last heartbeat time, zero when none was received.
func (c *Connection) LastHeartbeat() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastHeartbeat
}

// close ends the connection, recording the first reason given.
func (c *Connection) close(reason CloseReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.reason = reason
	close(c.done)
}

// Registry holds the live control connections. It is in-memory on purpose: a
// connection is by definition local to the Core instance terminating it.
type Registry struct {
	mu    sync.RWMutex
	byID  map[domain.BackendInstanceID]*Connection
	nowFn func() time.Time
	newID func() string
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		byID:  make(map[domain.BackendInstanceID]*Connection),
		nowFn: time.Now,
		newID: domain.NewUUID,
	}
}

// Register installs a new connection for instanceID and returns it together with
// the connection it superseded, if any. The superseded connection receives no
// new work; its already-sent, idempotent events may still be accepted.
func (r *Registry) Register(instanceID domain.BackendInstanceID) (conn *Connection, superseded *Connection) {
	now := r.nowFn()
	conn = &Connection{
		id:          r.newID(),
		instanceID:  instanceID,
		connectedAt: now,
		send:        make(chan *backendv1.CoreToBackend, sendBuffer),
		done:        make(chan struct{}),
	}

	r.mu.Lock()
	previous := r.byID[instanceID]
	r.byID[instanceID] = conn
	r.mu.Unlock()

	if previous != nil {
		previous.close(ReasonSuperseded)
	}
	return conn, previous
}

// Release removes conn from the registry, but only when it still holds the
// lease: a superseded connection must never evict its successor.
func (r *Registry) Release(conn *Connection) {
	if conn == nil {
		return
	}
	r.mu.Lock()
	if current, ok := r.byID[conn.instanceID]; ok && current == conn {
		delete(r.byID, conn.instanceID)
	}
	r.mu.Unlock()
	conn.close(ReasonReleased)
}

// CloseAll ends every live connection, so the control streams drain instead of
// holding a graceful shutdown open for their whole lifetime.
func (r *Registry) CloseAll(reason CloseReason) {
	for _, conn := range r.Connections() {
		conn.close(reason)
	}
}

// Lookup returns the active connection of a BackendInstance.
func (r *Registry) Lookup(instanceID domain.BackendInstanceID) (*Connection, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	conn, ok := r.byID[instanceID]
	return conn, ok
}

// Connections returns every active connection.
func (r *Registry) Connections() []*Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Connection, 0, len(r.byID))
	for _, conn := range r.byID {
		out = append(out, conn)
	}
	return out
}

// Len returns the number of active connections.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}

// Stale returns the connections whose last heartbeat is older than olderThan.
// Core infers OFFLINE from them; a backend never reports OFFLINE itself.
func (r *Registry) Stale(olderThan time.Duration) []*Connection {
	cutoff := r.nowFn().Add(-olderThan)
	var out []*Connection
	for _, conn := range r.Connections() {
		last := conn.LastHeartbeat()
		if last.IsZero() {
			last = conn.ConnectedAt()
		}
		if last.Before(cutoff) {
			out = append(out, conn)
		}
	}
	return out
}
