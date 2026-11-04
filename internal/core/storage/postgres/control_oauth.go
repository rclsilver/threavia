package postgres

import (
	"context"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func (s *Store) CreateOAuthClient(ctx context.Context, c domain.OAuthClient) (domain.OAuthClient, error) {
	err := s.q.QueryRow(ctx, `INSERT INTO control_oauth_clients(id,name,redirect_uris) VALUES($1,$2,$3) RETURNING created_at`,
		c.ID, c.Name, c.RedirectURIs).Scan(&c.CreatedAt)
	return c, classify(err, "register OAuth client")
}

func (s *Store) OAuthClient(ctx context.Context, id string) (domain.OAuthClient, error) {
	var c domain.OAuthClient
	err := s.q.QueryRow(ctx, `SELECT id,name,redirect_uris,created_at FROM control_oauth_clients WHERE id=$1`, id).
		Scan(&c.ID, &c.Name, &c.RedirectURIs, &c.CreatedAt)
	return c, classify(err, "read OAuth client")
}

func (s *Store) CreateOAuthCode(ctx context.Context, hash string, c domain.OAuthCode) error {
	_, err := s.q.Exec(ctx, `INSERT INTO control_oauth_codes(code_hash,client_id,owner_id,redirect_uri,code_challenge,scopes,resource,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		hash, c.ClientID, c.OwnerID, c.RedirectURI, c.CodeChallenge, c.Scopes, c.Resource, c.ExpiresAt)
	return classify(err, "store OAuth code")
}

// UseOAuthCode marks a code used and returns it, once: a second exchange of
// the same code finds nothing. An expired code is not returned either.
func (s *Store) UseOAuthCode(ctx context.Context, hash string) (domain.OAuthCode, error) {
	var c domain.OAuthCode
	err := s.q.QueryRow(ctx, `UPDATE control_oauth_codes SET used_at=clock_timestamp()
		WHERE code_hash=$1 AND used_at IS NULL AND expires_at>clock_timestamp()
		RETURNING client_id,owner_id,redirect_uri,code_challenge,scopes,resource,expires_at`, hash).
		Scan(&c.ClientID, &c.OwnerID, &c.RedirectURI, &c.CodeChallenge, &c.Scopes, &c.Resource, &c.ExpiresAt)
	return c, classify(err, "use OAuth code")
}

// CreateOAuthAccess records the grant a consent gave, as a control access
// with no static secret.
func (s *Store) CreateOAuthAccess(ctx context.Context, a domain.ControlAccess, clientID string) (domain.ControlAccess, error) {
	return scanControl(s.q.QueryRow(ctx, `INSERT INTO control_accesses(id,owner_id,label,scopes,oauth_client_id) VALUES($1,$2,$3,$4,$5) RETURNING `+controlColumns,
		a.ID, a.OwnerID, a.Label, a.Scopes, clientID))
}

func (s *Store) CreateOAuthToken(ctx context.Context, hash, accessID, kind string, expiresAt time.Time) error {
	_, err := s.q.Exec(ctx, `INSERT INTO control_oauth_tokens(token_hash,access_id,kind,expires_at) VALUES($1,$2,$3,$4)`, hash, accessID, kind, expiresAt)
	return classify(err, "store OAuth token")
}

// ControlAccessByOAuthToken finds the live grant behind an unexpired access
// token.
func (s *Store) ControlAccessByOAuthToken(ctx context.Context, hash string) (domain.ControlAccess, error) {
	return scanControl(s.q.QueryRow(ctx, `SELECT `+controlColumns+` FROM control_accesses
		WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp())
		AND id=(SELECT access_id FROM control_oauth_tokens WHERE token_hash=$1 AND kind='access' AND expires_at>clock_timestamp())`, hash))
}

// UseOAuthRefreshToken spends a refresh token, once, and returns its live
// grant and the client it was given to.
func (s *Store) UseOAuthRefreshToken(ctx context.Context, hash string) (domain.ControlAccess, string, error) {
	var accessID string
	err := s.q.QueryRow(ctx, `UPDATE control_oauth_tokens SET used_at=clock_timestamp()
		WHERE token_hash=$1 AND kind='refresh' AND used_at IS NULL AND expires_at>clock_timestamp()
		RETURNING access_id`, hash).Scan(&accessID)
	if err != nil {
		return domain.ControlAccess{}, "", classify(err, "use OAuth refresh token")
	}
	var clientID *string
	a, err := scanControl(s.q.QueryRow(ctx, `SELECT `+controlColumns+` FROM control_accesses
		WHERE id=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp())`, accessID))
	if err != nil {
		return a, "", err
	}
	if err := s.q.QueryRow(ctx, `SELECT oauth_client_id::text FROM control_accesses WHERE id=$1`, accessID).Scan(&clientID); err != nil {
		return a, "", classify(err, "read OAuth grant")
	}
	if clientID == nil {
		return a, "", ErrNotFound
	}
	return a, *clientID, nil
}

// PurgeOAuth drops codes and tokens that can no longer be used.
func (s *Store) PurgeOAuth(ctx context.Context) error {
	if _, err := s.q.Exec(ctx, `DELETE FROM control_oauth_codes WHERE expires_at<clock_timestamp()-interval '1 day'`); err != nil {
		return classify(err, "purge OAuth codes")
	}
	_, err := s.q.Exec(ctx, `DELETE FROM control_oauth_tokens WHERE expires_at<clock_timestamp()`)
	return classify(err, "purge OAuth tokens")
}
