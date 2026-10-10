package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/push"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// PushConfig is what a client needs to subscribe a browser, and the browsers
// already subscribed.
type PushConfig struct {
	PublicKey     string                    `json:"publicKey"`
	Subscriptions []domain.PushSubscription `json:"subscriptions"`
}

// PushSubscriptionInput is PushSubscription.toJSON() as a browser produces
// it, with a label the client picks to tell devices apart.
type PushSubscriptionInput struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256DH string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	Label string `json:"label"`
}

// vapidKeys returns this Core's push identity, made on first use and then
// kept for good: every subscription was made against its public half.
func (s *Service) vapidKeys(ctx context.Context) (push.Keys, error) {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	if s.pushKeys != nil {
		return *s.pushKeys, nil
	}

	raw, err := s.store.VAPIDKey(ctx)
	if errors.Is(err, postgres.ErrNotFound) {
		fresh, genErr := push.GenerateKeys()
		if genErr != nil {
			return push.Keys{}, genErr
		}
		raw, err = s.store.SaveVAPIDKey(ctx, fresh.Private())
	}
	if err != nil {
		return push.Keys{}, err
	}
	keys, err := push.ParseKeys(raw)
	if err != nil {
		return push.Keys{}, err
	}
	s.pushKeys = &keys
	return keys, nil
}

// PushConfig returns the public key a browser subscribes with and the
// browsers the user already subscribed.
func (s *Service) PushConfig(ctx context.Context, identity auth.Identity) (PushConfig, error) {
	keys, err := s.vapidKeys(ctx)
	if err != nil {
		return PushConfig{}, err
	}
	subs, err := s.store.PushSubscriptions(ctx, identity.UserID)
	if err != nil {
		return PushConfig{}, translate(err)
	}
	if subs == nil {
		subs = []domain.PushSubscription{}
	}
	return PushConfig{PublicKey: keys.Public(), Subscriptions: subs}, nil
}

// SubscribePush records a browser. The origin it subscribed from becomes the
// contact push services are given for this sender, which is the site itself.
func (s *Service) SubscribePush(ctx context.Context, identity auth.Identity, in PushSubscriptionInput, origin string) (domain.PushSubscription, error) {
	endpoint, err := url.Parse(in.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" {
		return domain.PushSubscription{}, fmt.Errorf("%w: the push endpoint must be an https URL", ErrInvalid)
	}
	p256dh, err := push.DecodeKey(in.Keys.P256DH)
	if err != nil || len(p256dh) != 65 {
		return domain.PushSubscription{}, fmt.Errorf("%w: the p256dh key is not a P-256 public key", ErrInvalid)
	}
	secret, err := push.DecodeKey(in.Keys.Auth)
	if err != nil || len(secret) != 16 {
		return domain.PushSubscription{}, fmt.Errorf("%w: the auth secret is not 16 bytes", ErrInvalid)
	}
	subject := origin
	if parsed, err := url.Parse(origin); err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		// Push services want a way to reach the sender; without an https
		// origin (a local setup) the host is the best there is.
		subject = "mailto:threavia@" + strings.Split(endpointHost(origin), ":")[0]
	}
	// The device name the client goes by, unless the request names one.
	client := domain.ClientFrom(ctx)
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = client.Name
	}
	if runes := []rune(label); len(runes) > 120 {
		label = string(runes[:120])
	}

	sub := domain.PushSubscription{
		ID:       domain.PushSubscriptionID(domain.NewUUID()),
		OwnerID:  identity.UserID,
		Endpoint: in.Endpoint,
		P256DH:   p256dh,
		Auth:     secret,
		Subject:  subject,
		ClientID: client.ID,
		Label:    label,
	}
	if err := s.store.SavePushSubscription(ctx, &sub); err != nil {
		return domain.PushSubscription{}, translate(err)
	}
	return sub, nil
}

func endpointHost(origin string) string {
	if parsed, err := url.Parse(origin); err == nil && parsed.Host != "" {
		return parsed.Host
	}
	return "localhost"
}

// UnsubscribePush forgets a browser.
func (s *Service) UnsubscribePush(ctx context.Context, identity auth.Identity, id domain.PushSubscriptionID) error {
	return translate(s.store.DeletePushSubscription(ctx, identity.UserID, id))
}

// TestPush sends a notification to every browser of the user, so a person
// setting it up sees it work rather than waiting for the agent to ask.
func (s *Service) TestPush(ctx context.Context, identity auth.Identity) (int, error) {
	subs, err := s.store.PushSubscriptions(ctx, identity.UserID)
	if err != nil {
		return 0, translate(err)
	}
	if len(subs) == 0 {
		return 0, fmt.Errorf("%w: no browser is subscribed", ErrConflict)
	}
	s.deliver(ctx, subs, push.Message{
		Title: "Threavia",
		Body:  "Notifications reach this device.",
		URL:   "/",
		Tag:   "test",
	})
	return len(subs), nil
}

// notifyOwner sends a notification to every browser of a user, in the
// background: a slow push service must never hold the event path.
func (s *Service) notifyOwner(ctx context.Context, ownerID domain.UserID, msg push.Message) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		subs, err := s.store.PushSubscriptions(ctx, ownerID)
		if err != nil || len(subs) == 0 {
			return
		}
		s.deliver(ctx, subs, msg)
	}()
}

func (s *Service) deliver(ctx context.Context, subs []domain.PushSubscription, msg push.Message) {
	keys, err := s.vapidKeys(ctx)
	if err != nil {
		s.logger.Error("cannot load the push identity", slog.String("error", err.Error()))
		return
	}
	for _, sub := range subs {
		sender := push.Sender{Keys: keys, Subject: sub.Subject, Client: s.pushClient}
		sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := sender.Send(sendCtx, push.Subscription{Endpoint: sub.Endpoint, P256DH: sub.P256DH, Auth: sub.Auth}, msg)
		cancel()
		switch {
		case errors.Is(err, push.ErrGone):
			// The browser unsubscribed or its data was cleared: there is
			// nobody at this address any more.
			_ = s.store.ForgetPushEndpoint(ctx, sub.Endpoint)
		case err != nil:
			s.logger.Warn("push delivery failed", slog.String("subscription", string(sub.ID)), slog.String("error", err.Error()))
			_ = s.store.RecordPushDelivery(ctx, sub.ID, err.Error())
		default:
			_ = s.store.RecordPushDelivery(ctx, sub.ID, "")
		}
	}
}

// notifyAttention decides whether something a Job just did is worth a
// notification, and sends it.
//
// What waits for the person, and the end of work they are not watching:
// never every event. Whether they are watching is decided per device, not per
// kind of client: a desktop tab and a phone are both "web", and a tab left
// open on a desk nobody sits at must not keep the phone silent. So the rule
// is presence: if the person is active on one of their clients — visible,
// focused, used in the last minutes — that screen already shows it and
// nothing rings; otherwise every subscribed device does.
func (s *Service) notifyAttention(ctx context.Context, jc postgres.JobContext, event *backendv1.JobEvent) {
	if session, err := s.store.GetSession(ctx, jc.OwnerID, jc.SessionID); err == nil && session.ManagerSessionID != nil {
		// The manager handles worker questions and reports their results. Its
		// own input/validation requests still notify the person as usual.
		if manager, err := s.store.GetSession(ctx, jc.OwnerID, *session.ManagerSessionID); err == nil && manager.Status == domain.SessionActive {
			return
		}
	}
	var title, body string
	switch payload := event.GetBody().(type) {
	case *backendv1.JobEvent_ValidationRequested:
		title, body = "Approval needed", payload.ValidationRequested.GetTitle()
	case *backendv1.JobEvent_UserInputRequested:
		title, body = "Question", payload.UserInputRequested.GetPrompt()
	case *backendv1.JobEvent_JobCompleted:
		title, body = "Done", payload.JobCompleted.GetSummary()
	case *backendv1.JobEvent_JobFailed:
		title, body = "Failed", payload.JobFailed.GetError().GetMessage()
	default:
		return
	}
	if s.broker.AnyActive(jc.OwnerID) {
		return
	}

	if session, err := s.store.GetSession(ctx, jc.OwnerID, jc.SessionID); err == nil && session.Title != "" {
		title += " · " + session.Title
	}
	s.notifyOwner(ctx, jc.OwnerID, push.Message{
		Title: clip(title, 120),
		Body:  clip(strings.Join(strings.Fields(body), " "), 240),
		URL:   "/sessions/" + string(jc.SessionID),
		// One notification per Session: a newer one replaces the older.
		Tag: string(jc.SessionID),
	})
}

func clip(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit-1]) + "…"
}

// SetPresence records whether the person is looking at the client behind the
// request. A client that named no id cannot be told apart, so it is refused.
func (s *Service) SetPresence(ctx context.Context, identity auth.Identity, active bool) error {
	client := domain.ClientFrom(ctx)
	if client.ID == "" {
		return fmt.Errorf("%w: presence needs the X-Threavia-Client header", ErrInvalid)
	}
	s.broker.SetPresence(identity.UserID, client.ID, events.Presence{Active: active})
	return nil
}

// ConnectedClients lists the user's client instances with a live stream.
func (s *Service) ConnectedClients(identity auth.Identity) []events.ConnectedClient {
	clients := s.broker.Clients(identity.UserID)
	if clients == nil {
		return []events.ConnectedClient{}
	}
	return clients
}
