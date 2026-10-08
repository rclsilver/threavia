package events

import (
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestBrokerDeliversToTheOwnerOnly(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	mine, cancelMine := broker.Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
	defer cancelMine()
	theirs, cancelTheirs := broker.Subscribe("someone-else", domain.ChannelWeb, domain.Client{}, nil)
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
	sub, cancel := broker.Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
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
	_, cancel := broker.Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
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
	_, cancel := broker.Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)

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

// TestPresenceIsPerDevice pins what decides whether a phone rings: whether
// the person is looking at one of their clients, device by device, not
// whether some client of the same kind is connected.
func TestPresenceIsPerDevice(t *testing.T) {
	t.Parallel()

	broker := NewBroker()
	desk := domain.Client{ID: "desk", Name: "workstation"}
	phone := domain.Client{ID: "phone", Name: "Android — Chrome"}

	_, closeDesk := broker.Subscribe("thomas", domain.ChannelWeb, desk, &Presence{Active: true})
	defer closeDesk()
	_, closePhone := broker.Subscribe("thomas", domain.ChannelWeb, phone, &Presence{Active: false})
	defer closePhone()
	// A second tab of the same desktop is the same device.
	_, closeTab := broker.Subscribe("thomas", domain.ChannelWeb, desk, nil)
	defer closeTab()

	if !broker.AnyActive("thomas") {
		t.Fatal("the person is at the desk")
	}
	if clients := broker.Clients("thomas"); len(clients) != 2 || clients[0].Name != "workstation" {
		t.Fatalf("clients = %+v, want the desk and the phone, once each", clients)
	}

	// Leaving the desk: every stream of that device goes idle together.
	if !broker.SetPresence("thomas", "desk", Presence{Active: false}) {
		t.Fatal("the desk holds streams")
	}
	if broker.AnyActive("thomas") {
		t.Fatal("nobody is looking any more: the phone must ring")
	}
	if broker.Watching("thomas", domain.ChannelWeb) {
		t.Fatal("an idle client is not watching")
	}

	// A stream that never said is taken as looked at.
	_, closeVSCode := broker.Subscribe("thomas", domain.ChannelVSCode, domain.Client{}, nil)
	defer closeVSCode()
	if !broker.AnyActive("thomas") {
		t.Fatal("a client that never reported presence counts as active")
	}
}
