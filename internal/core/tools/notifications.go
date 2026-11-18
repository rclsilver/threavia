package tools

// The Core Tools that let an agent tell the person something without asking
// for an answer, and read or clear what was raised.
const (
	NameNotify               Name = "notify"
	NameNotificationList     Name = "notification_list"
	NameNotificationMarkRead Name = "notification_mark_read"
)

func notificationSpecs() []Spec {
	return []Spec{
		{
			Name:        NameNotify,
			Description: "Raise a notification for the person, attached to this session: INFO to report something, WARNING when it needs their attention. It does not wait for an answer and never blocks this job; use ask_user to ask a question. The same unread notification raised again is counted, not repeated.",
			InputSchema: object(map[string]any{
				"level":   map[string]any{"type": "string", "enum": []string{"INFO", "WARNING"}, "description": "INFO or WARNING."},
				"title":   str("Short headline, at most 120 characters."),
				"message": str("What happened and what, if anything, to do. At most 2000 characters."),
			}, "level", "title", "message"),
		},
		{
			Name:        NameNotificationList,
			Description: "List the person's notifications, WARNING first then the most recent. Unread ones only, unless unread is false.",
			InputSchema: object(map[string]any{
				"unread":    map[string]any{"type": "boolean", "description": "Only unread notifications. Default true."},
				"sessionId": str("Only this session."),
			}),
		},
		{
			Name:        NameNotificationMarkRead,
			Description: "Mark one notification read, or all those of a session. Give notificationId or sessionId.",
			InputSchema: object(map[string]any{
				"notificationId": str("Identifier from notification_list."),
				"sessionId":      str("Mark every unread notification of this session."),
			}),
		},
	}
}
