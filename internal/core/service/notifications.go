package service

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/push"
	"github.com/rclsilver/threavia/internal/core/tools"
)

const (
	maxNotificationTitle   = 120
	maxNotificationMessage = 2000
)

// truncate cuts text to at most limit characters.
func truncate(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

// Notify records a notification an agent raised for the person. It never
// blocks the Job. A WARNING also reaches the person's devices when no client
// of theirs is in use; an INFO waits in the list.
func (s *Service) Notify(ctx context.Context, identity auth.Identity, jc jobScope, level domain.NotificationLevel, title, message string) (domain.Notification, error) {
	title, message = strings.TrimSpace(title), strings.TrimSpace(message)
	switch {
	case !level.Valid():
		return domain.Notification{}, fmt.Errorf("%w: level must be INFO or WARNING", ErrInvalid)
	case title == "":
		return domain.Notification{}, fmt.Errorf("%w: title is required", ErrInvalid)
	case message == "":
		return domain.Notification{}, fmt.Errorf("%w: message is required", ErrInvalid)
	}
	n := domain.Notification{
		Scope: domain.Scope{ProjectID: jc.ProjectID, SessionID: jc.SessionID, JobID: jc.JobID},
		Level: level, Title: truncate(title, maxNotificationTitle), Message: truncate(message, maxNotificationMessage),
	}
	if _, err := s.store.CreateNotification(ctx, &n); err != nil {
		return domain.Notification{}, translate(err)
	}
	s.emit(ctx, identity.UserID, events.TypeNotificationCreated,
		domain.Scope{ProjectID: n.Scope.ProjectID, SessionID: n.Scope.SessionID},
		NotificationPayload{NotificationID: n.ID, Level: n.Level, Title: n.Title, Message: n.Message, Count: n.Count})
	if n.Level == domain.NotificationWarning {
		s.pushWarning(ctx, identity.UserID, n)
	}
	return n, nil
}

// pushWarning sends the Web Push of a WARNING, under the same presence rule as
// every other notification, and not for a Session a manager handles.
func (s *Service) pushWarning(ctx context.Context, ownerID domain.UserID, n domain.Notification) {
	if s.delegated(ctx, ownerID, n.Scope.SessionID) || s.broker.AnyActive(ownerID) {
		return
	}
	title := "Warning · " + n.Title
	if session, err := s.store.GetSession(ctx, ownerID, n.Scope.SessionID); err == nil && session.Title != "" {
		title += " · " + session.Title
	}
	s.notifyOwner(ctx, ownerID, push.Message{
		Title: clip(title, 120),
		Body:  clip(strings.Join(strings.Fields(n.Message), " "), 240),
		URL:   "/sessions/" + string(n.Scope.SessionID),
		Tag:   string(n.Scope.SessionID),
	})
}

// Notifications lists the person's notifications, WARNING first.
func (s *Service) Notifications(ctx context.Context, identity auth.Identity, unreadOnly bool, sessionID domain.SessionID) ([]domain.Notification, error) {
	list, err := s.store.ListNotifications(ctx, identity.UserID, unreadOnly, sessionID)
	if err != nil {
		return nil, translate(err)
	}
	if list == nil {
		list = []domain.Notification{}
	}
	return list, nil
}

// MarkNotificationRead marks one notification read, on every client. Reading
// it again, from another device, changes and announces nothing.
func (s *Service) MarkNotificationRead(ctx context.Context, identity auth.Identity, id domain.NotificationID) error {
	n, changed, err := s.store.MarkNotificationRead(ctx, identity.UserID, id)
	if err != nil {
		return translate(err)
	}
	if changed {
		s.announceRead(ctx, identity, n.Scope, []domain.NotificationID{n.ID})
	}
	return nil
}

// MarkNotificationsRead marks every unread notification read, of one Session
// when sessionID is set.
func (s *Service) MarkNotificationsRead(ctx context.Context, identity auth.Identity, sessionID domain.SessionID) error {
	changed, err := s.store.MarkNotificationsRead(ctx, identity.UserID, sessionID)
	if err != nil {
		return translate(err)
	}
	if len(changed) == 0 {
		return nil
	}
	ids := make([]domain.NotificationID, len(changed))
	for i, n := range changed {
		ids[i] = n.ID
	}
	s.announceRead(ctx, identity, changed[0].Scope, ids)
	return nil
}

func (s *Service) announceRead(ctx context.Context, identity auth.Identity, scope domain.Scope, ids []domain.NotificationID) {
	s.emit(ctx, identity.UserID, events.TypeNotificationRead,
		domain.Scope{ProjectID: scope.ProjectID, SessionID: scope.SessionID},
		NotificationReadPayload{NotificationIDs: ids})
}

// notificationTool runs the notification Core Tools for the calling Job.
func (s *Service) notificationTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	text := func(key string) (string, error) {
		raw, present := input[key]
		if !present {
			return "", nil
		}
		value, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("%w: %s must be a string", ErrInvalid, key)
		}
		return value, nil
	}
	switch name {
	case tools.NameNotify:
		level, err := text("level")
		if err != nil {
			return nil, err
		}
		title, err := text("title")
		if err != nil {
			return nil, err
		}
		message, err := text("message")
		if err != nil {
			return nil, err
		}
		n, err := s.Notify(ctx, identity, jc, domain.NotificationLevel(level), title, message)
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"notification": n})
	case tools.NameNotificationList:
		unread, err := boolInput(input, "unread", true)
		if err != nil {
			return nil, err
		}
		sessionID, err := text("sessionId")
		if err != nil {
			return nil, err
		}
		list, err := s.Notifications(ctx, identity, unread, domain.SessionID(sessionID))
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"notifications": list})
	case tools.NameNotificationMarkRead:
		id, err := text("notificationId")
		if err != nil {
			return nil, err
		}
		sessionID, err := text("sessionId")
		if err != nil {
			return nil, err
		}
		switch {
		case (id == "") == (sessionID == ""):
			return nil, fmt.Errorf("%w: give either notificationId or sessionId", ErrInvalid)
		case id != "":
			err = s.MarkNotificationRead(ctx, identity, domain.NotificationID(id))
		default:
			err = s.MarkNotificationsRead(ctx, identity, domain.SessionID(sessionID))
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"read": true}, nil
	}
	return nil, fmt.Errorf("%w: unknown core tool %q", ErrInvalid, name)
}
