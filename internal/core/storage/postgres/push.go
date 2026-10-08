package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// VAPIDKey returns the stored push identity, or ErrNotFound before the first
// one was made.
func (s *Store) VAPIDKey(ctx context.Context) ([]byte, error) {
	var key []byte
	err := s.q.QueryRow(ctx, `SELECT private_key FROM push_vapid WHERE id = 1`).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return key, classify(err, "read the push identity")
}

// SaveVAPIDKey keeps a new push identity, unless another Core replica saved
// one first, in which case that one is returned and used.
func (s *Store) SaveVAPIDKey(ctx context.Context, key []byte) ([]byte, error) {
	if _, err := s.q.Exec(ctx,
		`INSERT INTO push_vapid (id, private_key) VALUES (1, $1) ON CONFLICT (id) DO NOTHING`, key); err != nil {
		return nil, classify(err, "save the push identity")
	}
	return s.VAPIDKey(ctx)
}

const pushColumns = `id, owner_id, endpoint, p256dh, auth, subject, client_id, label, created_at, last_used_at, last_error`

// SavePushSubscription records a browser's subscription. The same endpoint
// subscribing again replaces its row, under whoever subscribed it last.
func (s *Store) SavePushSubscription(ctx context.Context, sub *domain.PushSubscription) error {
	return classify(s.q.QueryRow(ctx, `
		INSERT INTO push_subscriptions (id, owner_id, endpoint, p256dh, auth, subject, client_id, label)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (endpoint) DO UPDATE
		SET owner_id = EXCLUDED.owner_id, p256dh = EXCLUDED.p256dh, auth = EXCLUDED.auth,
		    subject = EXCLUDED.subject, client_id = EXCLUDED.client_id, label = EXCLUDED.label, last_error = ''
		RETURNING id, created_at`,
		sub.ID, sub.OwnerID, sub.Endpoint, sub.P256DH, sub.Auth, sub.Subject, sub.ClientID, sub.Label,
	).Scan(&sub.ID, &sub.CreatedAt), "save push subscription")
}

// PushSubscriptions returns every browser a user subscribed.
func (s *Store) PushSubscriptions(ctx context.Context, ownerID domain.UserID) ([]domain.PushSubscription, error) {
	rows, err := s.q.Query(ctx, `SELECT `+pushColumns+` FROM push_subscriptions
		WHERE owner_id = $1 ORDER BY created_at`, ownerID)
	if err != nil {
		return nil, classify(err, "list push subscriptions")
	}
	defer rows.Close()
	var out []domain.PushSubscription
	for rows.Next() {
		var sub domain.PushSubscription
		if err := rows.Scan(&sub.ID, &sub.OwnerID, &sub.Endpoint, &sub.P256DH, &sub.Auth, &sub.Subject, &sub.ClientID,
			&sub.Label, &sub.CreatedAt, &sub.LastUsedAt, &sub.LastError); err != nil {
			return nil, classify(err, "read push subscription")
		}
		out = append(out, sub)
	}
	return out, classify(rows.Err(), "list push subscriptions")
}

// DeletePushSubscription removes a subscription of the user.
func (s *Store) DeletePushSubscription(ctx context.Context, ownerID domain.UserID, id domain.PushSubscriptionID) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM push_subscriptions WHERE id = $1 AND owner_id = $2`, id, ownerID)
	if err != nil {
		return classify(err, "delete push subscription")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ForgetPushEndpoint drops a subscription the push service said is gone.
func (s *Store) ForgetPushEndpoint(ctx context.Context, endpoint string) error {
	_, err := s.q.Exec(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint)
	return classify(err, "forget push subscription")
}

// RecordPushDelivery notes how the last delivery to a subscription went.
func (s *Store) RecordPushDelivery(ctx context.Context, id domain.PushSubscriptionID, failure string) error {
	_, err := s.q.Exec(ctx, `
		UPDATE push_subscriptions SET last_used_at = now(), last_error = $2 WHERE id = $1`, id, failure)
	return classify(err, "record push delivery")
}
