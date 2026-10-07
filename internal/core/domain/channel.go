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
