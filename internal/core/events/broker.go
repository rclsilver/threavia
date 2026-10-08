package events

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

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
	id int64
	// channel is the kind of client holding this stream. It is what lets Core
	// tell a watching device from an absent one (spec section 6).
	channel domain.Channel
	// client is the instance holding it: what tells a desktop from a phone
	// when both are "web".
	client  domain.Client
	since   time.Time
	ownerID domain.UserID
	ch      chan Envelope
	lagged  atomic.Bool

	// Presence, guarded by the broker lock. A client that never reported is
	// taken as active: a stream someone opened is presumably being looked at,
	// and assuming otherwise would ring a phone for what a screen already
	// shows.
	reported bool
	active   bool
}

// Presence says whether the person is looking at a client right now.
type Presence struct {
	// Active is false when the client said it is hidden, unfocused or idle.
	Active bool
}

func (s *Subscription) isActive() bool { return !s.reported || s.active }

// ConnectedClient is one client instance with at least one live stream.
type ConnectedClient struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Channel domain.Channel `json:"channel"`
	Active  bool           `json:"active"`
	Since   time.Time      `json:"since"`
}

// Events is the channel the subscriber reads.
func (s *Subscription) Events() <-chan Envelope { return s.ch }

// Lagged reports whether events were dropped for this subscriber. The handler
// must then replay from its cursor rather than trust the live stream.
func (s *Subscription) Lagged() bool { return s.lagged.Load() }

// ClearLagged acknowledges that the subscriber has caught up.
func (s *Subscription) ClearLagged() { s.lagged.Store(false) }

// Subscribe registers a client stream for one user, declaring which kind of
// client holds it, which instance, and — when it said — whether the person is
// looking at it. The returned function cancels it and must always be called.
func (b *Broker) Subscribe(ownerID domain.UserID, channel domain.Channel, client domain.Client, presence *Presence) (*Subscription, func()) {
	b.mu.Lock()
	b.next++
	sub := &Subscription{
		id: b.next, ownerID: ownerID, channel: channel, client: client,
		since: time.Now().UTC(),
		ch:    make(chan Envelope, subscriberBuffer),
	}
	if presence != nil {
		sub.reported, sub.active = true, presence.Active
	}
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

// Watching reports whether the user currently holds a live stream on a channel.
//
// It answers the question of specification section 6: a client that is already
// receiving live events does not also need an OS notification, while a device
// that initiated the work and is now away does.
func (b *Broker) Watching(ownerID domain.UserID, channel domain.Channel) bool {
	if channel == "" {
		return false
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, sub := range b.subs {
		if sub.ownerID == ownerID && sub.channel == channel && sub.isActive() {
			return true
		}
	}
	return false
}

// SetPresence records whether the person is looking at a client instance,
// on every stream it holds. It reports whether any was found.
func (b *Broker) SetPresence(ownerID domain.UserID, clientID string, presence Presence) bool {
	if clientID == "" {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	found := false
	for _, sub := range b.subs {
		if sub.ownerID == ownerID && sub.client.ID == clientID {
			sub.reported, sub.active = true, presence.Active
			found = true
		}
	}
	return found
}

// AnyActive reports whether the person is looking at one of their clients
// right now. A notification would only repeat what that screen shows.
func (b *Broker) AnyActive(ownerID domain.UserID) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs {
		if sub.ownerID == ownerID && sub.isActive() {
			return true
		}
	}
	return false
}

// Clients lists the client instances of a user with a live stream, one entry
// per instance however many streams it holds.
func (b *Broker) Clients(ownerID domain.UserID) []ConnectedClient {
	b.mu.RLock()
	defer b.mu.RUnlock()
	byID := make(map[string]*ConnectedClient)
	var out []*ConnectedClient
	for _, sub := range b.subs {
		if sub.ownerID != ownerID {
			continue
		}
		key := sub.client.ID
		if key == "" {
			// An instance that did not say who it is stays one entry per
			// stream: merging them would invent a device.
			key = fmt.Sprintf("stream-%d", sub.id)
		}
		existing, ok := byID[key]
		if !ok {
			existing = &ConnectedClient{ID: sub.client.ID, Name: sub.client.Name, Channel: sub.channel, Since: sub.since}
			byID[key] = existing
			out = append(out, existing)
		}
		existing.Active = existing.Active || sub.isActive()
		if sub.since.Before(existing.Since) {
			existing.Since = sub.since
		}
	}
	clients := make([]ConnectedClient, 0, len(out))
	for _, c := range out {
		clients = append(clients, *c)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].Since.Before(clients[j].Since) })
	return clients
}
