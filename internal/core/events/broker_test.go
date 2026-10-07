package events

import (
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestBrokerDeliversToTheOwnerOnly(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	mine, cancelMine := broker.Subscribe("thomas", domain.ChannelWeb)
	defer cancelMine()
	theirs, cancelTheirs := broker.Subscribe("someone-else", domain.ChannelWeb)
	defer cancelTheirs()

	broker.Publish("thomas", Envelope{Sequence: 1, Type: TypeAgentMessage})

	select {
	case got := <-mine.Events():
		if got.Sequence != 1 {
			t.Fatalf("sequence = %d, want 1", got.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("the owner must receive the event")
	}

	select {
	case got := <-theirs.Events():
		t.Fatalf("another user received %v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestBrokerNeverBlocksOnASlowClient pins that a stalled client degrades to a
// catch-up from the database instead of stalling the writer.
func TestBrokerNeverBlocksOnASlowClient(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	sub, cancel := broker.Subscribe("thomas", domain.ChannelWeb)
	defer cancel()

	for i := range subscriberBuffer + 10 {
		broker.Publish("thomas", Envelope{Sequence: domain.Sequence(i)})
	}

	if !sub.Lagged() {
		t.Fatal("a client that fell behind must be marked lagged")
	}
	sub.ClearLagged()
	if sub.Lagged() {
		t.Fatal("clearing the lag must be observable")
	}
}

func TestBrokerUnsubscribe(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	_, cancel := broker.Subscribe("thomas", domain.ChannelWeb)
	if broker.Subscribers() != 1 {
		t.Fatalf("subscribers = %d, want 1", broker.Subscribers())
	}
	cancel()
	if broker.Subscribers() != 0 {
		t.Fatalf("subscribers = %d after cancelling, want 0", broker.Subscribers())
	}
	// Publishing to nobody must not panic on the closed channel.
	broker.Publish("thomas", Envelope{Sequence: 1})
}

// TestWatchingReportsLiveChannels pins the notification relevance of
// specification section 6: a client that is already receiving live events must
// not also be told to ring.
func TestWatchingReportsLiveChannels(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	_, cancel := broker.Subscribe("thomas", domain.ChannelWeb)

	if !broker.Watching("thomas", domain.ChannelWeb) {
		t.Fatal("a connected channel must be reported as watching")
	}
	if broker.Watching("thomas", domain.ChannelAndroid) {
		t.Fatal("a channel with no stream must not be reported as watching")
	}
	if broker.Watching("someone-else", domain.ChannelWeb) {
		t.Fatal("another user's stream must not count")
	}
	if broker.Watching("thomas", "") {
		t.Fatal("an unknown origin is never watching")
	}

	cancel()
	if broker.Watching("thomas", domain.ChannelWeb) {
		t.Fatal("a closed stream must stop counting")
	}
}
