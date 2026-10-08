package domain

import "time"

// PushSubscriptionID identifies a PushSubscription.
type PushSubscriptionID string

// PushSubscription is one browser on one device that receives notifications
// for its owner. The keys stay server-side; a client sees the label and when
// it was last used.
type PushSubscription struct {
	ID       PushSubscriptionID `json:"id"`
	OwnerID  UserID             `json:"-"`
	Endpoint string             `json:"-"`
	P256DH   []byte             `json:"-"`
	Auth     []byte             `json:"-"`
	Subject  string             `json:"-"`
	// ClientID is the client instance that subscribed, so a device can
	// recognise its own entry.
	ClientID   string     `json:"clientId,omitempty"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty"`
	LastError  string     `json:"lastError,omitempty"`
}
