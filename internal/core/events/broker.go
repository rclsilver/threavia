package events

import (
	"sync"
	"sync/atomic"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// subscriberBuffer is how many events a slow client may fall behind before it is
// marked lagged and told to catch up from the database instead.
const subscriberBuffer = 256

// Broker fans persisted events out to the connected clients of one Core
// process.
//
// It is a fast path, never a source of truth: PostgreSQL holds the timeline and
// the global sequence. A subscriber that cannot keep up is marked lagged rather
// than blocking the writer, and catches up by replaying from its cursor. That is
// why no message bus is required in V1.
type Broker struct {
	mu   sync.RWMutex
	next int64
	subs map[int64]*Subscription
}

// NewBroker returns an empty Broker.
func NewBroker() *Broker {
	return &Broker{subs: make(map[int64]*Subscription)}
}

// Subscription is one client stream.
type Subscription struct {
	id      int64
	ownerID domain.UserID
	ch      chan Envelope
	lagged  atomic.Bool
}

// Events is the channel the subscriber reads.
func (s *Subscription) Events() <-chan Envelope { return s.ch }

// Lagged reports whether events were dropped for this subscriber. The handler
// must then replay from its cursor rather than trust the live stream.
func (s *Subscription) Lagged() bool { return s.lagged.Load() }

// ClearLagged acknowledges that the subscriber has caught up.
func (s *Subscription) ClearLagged() { s.lagged.Store(false) }

// Subscribe registers a client stream for one user. The returned function
// cancels it and must always be called.
func (b *Broker) Subscribe(ownerID domain.UserID) (*Subscription, func()) {
	b.mu.Lock()
	b.next++
	sub := &Subscription{id: b.next, ownerID: ownerID, ch: make(chan Envelope, subscriberBuffer)}
	b.subs[sub.id] = sub
	b.mu.Unlock()

	return sub, func() {
		b.mu.Lock()
		delete(b.subs, sub.id)
		b.mu.Unlock()
		close(sub.ch)
	}
}

// Publish delivers an event to every subscriber of its owner. It never blocks.
func (b *Broker) Publish(ownerID domain.UserID, envelope Envelope) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subs {
		if sub.ownerID != ownerID {
			continue
		}
		select {
		case sub.ch <- envelope:
		default:
			sub.lagged.Store(true)
		}
	}
}

// Subscribers returns the number of live client streams.
func (b *Broker) Subscribers() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
