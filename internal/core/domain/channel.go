package domain

import (
	"context"
	"strings"
)

// Channel names the kind of client that initiated or answered work
// (spec section 6).
//
// It is deliberately a loose vocabulary rather than a closed enumeration: a new
// client must not require a Core release, and Core never behaves differently per
// channel. What it does with a Channel is remember which client started a piece
// of work, so a device that is already watching is not also made to ring.
type Channel string

// The channels V1 expects. Anything else is recorded as given.
const (
	ChannelWeb     Channel = "web"
	ChannelAndroid Channel = "android"
	ChannelVSCode  Channel = "vscode"
	ChannelVoice   Channel = "voice"
	// ChannelAPI is the fallback for a caller that names no channel, such as a
	// script or a test.
	ChannelAPI Channel = "api"
	// ChannelSchedule marks work Core started itself, on a Schedule.
	ChannelSchedule Channel = "schedule"
)

// NormaliseChannel turns what a client declared into a stored Channel. An empty
// or oversized value becomes ChannelAPI rather than being rejected: a missing
// header must never fail a command.
func NormaliseChannel(raw string) Channel {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || len(raw) > 32 {
		return ChannelAPI
	}
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return ChannelAPI
		}
	}
	return Channel(raw)
}

func (c Channel) String() string { return string(c) }

// channelKey is the context key carrying the calling Channel.
type channelKey struct{}

// WithChannel marks a context with the kind of client behind the request.
//
// A context value rather than a parameter on every operation: this is
// request-scoped metadata about who is calling, which the audit trail and the
// notification relevance both want and which no business operation reasons
// about.
func WithChannel(ctx context.Context, channel Channel) context.Context {
	return context.WithValue(ctx, channelKey{}, channel)
}

// ChannelFrom returns the Channel behind the request, or ChannelAPI when none
// was declared.
func ChannelFrom(ctx context.Context) Channel {
	if channel, ok := ctx.Value(channelKey{}).(Channel); ok && channel != "" {
		return channel
	}
	return ChannelAPI
}

// Client is one browser or app instance, as opposed to a Channel, which is
// only its kind: two web clients — a desktop and a phone — share the channel
// "web" and are told apart by their ID. The name is what the person calls the
// device.
type Client struct {
	ID   string
	Name string
}

// NormaliseClient keeps an identifier that is safe to store and show, and
// drops one that is not rather than failing the request.
func NormaliseClient(id, name string) Client {
	id = strings.TrimSpace(id)
	if len(id) > 64 {
		id = ""
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			id = ""
		}
	}
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > 80 {
		name = string(runes[:80])
	}
	return Client{ID: id, Name: name}
}

type clientKey struct{}

// WithClient marks a context with the client instance behind the request.
func WithClient(ctx context.Context, client Client) context.Context {
	return context.WithValue(ctx, clientKey{}, client)
}

// ClientFrom returns the client instance behind the request, empty when it
// did not say.
func ClientFrom(ctx context.Context) Client {
	client, _ := ctx.Value(clientKey{}).(Client)
	return client
}
