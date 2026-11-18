package domain

import "time"

// NotificationID identifies a Notification.
type NotificationID string

// NewNotificationID returns a fresh NotificationID.
func NewNotificationID() NotificationID { return NotificationID(NewUUID()) }

// NotificationLevel says how much a Notification asks of the person.
type NotificationLevel string

const (
	// NotificationInfo reports something; it can wait.
	NotificationInfo NotificationLevel = "INFO"
	// NotificationWarning needs the person's attention.
	NotificationWarning NotificationLevel = "WARNING"
)

// Valid reports whether l is one of the two levels.
func (l NotificationLevel) Valid() bool { return l == NotificationInfo || l == NotificationWarning }

// Notification is something an agent tells the person without asking for an
// answer. It belongs to a Session and stays unread until the person dismisses
// it or joins the conversation.
type Notification struct {
	ID    NotificationID    `json:"id"`
	Scope Scope             `json:"scope"`
	Level NotificationLevel `json:"level"`
	Title string            `json:"title"`
	// Message is the body of the notification.
	Message string `json:"message"`
	// Count is how many times the same unread notification was raised.
	Count     int        `json:"count"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
}
