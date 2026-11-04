package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// OAuth lifetimes. A code is exchanged by the client right after the consent;
// an access token is short so that revocation is felt without delay even by
// a client that caches it, and the refresh token keeps the connection alive
// as long as the client keeps using it.
const (
	oauthCodeLifetime    = 5 * time.Minute
	oauthAccessLifetime  = time.Hour
	oauthRefreshLifetime = 60 * 24 * time.Hour
)

// OAuthError is an error the token endpoint reports with its RFC 6749 code.
type OAuthError struct {
	Code        string
	Description string
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }

func oauthError(code, description string) error {
	return &OAuthError{Code: code, Description: description}
}

// OAuthTokens is a token response.
type OAuthTokens struct {
	AccessToken  string   `json:"access_token"`
	TokenType    string   `json:"token_type"`
	ExpiresIn    int      `json:"expires_in"`
	RefreshToken string   `json:"refresh_token"`
	Scopes       []string `json:"-"`
}

// OAuthAuthorization is what a client asks for, as the consent page shows it.
type OAuthAuthorization struct {
	ClientID            string
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Scopes              []string
	Resource            string
}

// RegisterOAuthClient registers a public client (RFC 7591). Anyone can
// register; a registration grants nothing until a person consents.
func (s *Service) RegisterOAuthClient(ctx context.Context, name string, redirectURIs []string) (domain.OAuthClient, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "MCP client"
	}
	if len(name) > 120 {
		return domain.OAuthClient{}, oauthError("invalid_client_metadata", "client_name must be at most 120 bytes")
	}
	if len(redirectURIs) == 0 || len(redirectURIs) > 10 {
		return domain.OAuthClient{}, oauthError("invalid_redirect_uri", "register 1 to 10 redirect_uris")
	}
	for _, uri := range redirectURIs {
		if err := validRedirectURI(uri); err != nil {
			return domain.OAuthClient{}, oauthError("invalid_redirect_uri", err.Error())
		}
	}
	client, err := s.store.CreateOAuthClient(ctx, domain.OAuthClient{ID: domain.NewUUID(), Name: name, RedirectURIs: redirectURIs})
	return client, translate(err)
}

// validRedirectURI accepts HTTPS, and plain HTTP only back to the same
// machine, as OAuth 2.1 allows for native clients.
func validRedirectURI(raw string) error {
	if len(raw) > 2000 {
		return fmt.Errorf("redirect_uri is too long")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("redirect_uri must be an absolute URL without fragment or credentials")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("redirect_uri must use https, or http to a loopback address")
}

// OAuthClient is what the consent page says about a client.
func (s *Service) OAuthClient(ctx context.Context, id string) (domain.OAuthClient, error) {
	if !isUUID(id) {
		return domain.OAuthClient{}, ErrNotFound
	}
	client, err := s.store.OAuthClient(ctx, id)
	return client, translate(err)
}

// CheckOAuthAuthorization validates a request before anyone is asked to
// consent to it. Its error says whether the client may be sent back to its
// redirect URI with it: never when the client or that URI is not known.
func (s *Service) CheckOAuthAuthorization(ctx context.Context, in OAuthAuthorization) (domain.OAuthClient, error) {
	client, err := s.OAuthClient(ctx, in.ClientID)
	if err != nil {
		return client, fmt.Errorf("%w: unknown client", ErrInvalid)
	}
	registered := false
	for _, uri := range client.RedirectURIs {
		registered = registered || uri == in.RedirectURI
	}
	if !registered {
		return client, fmt.Errorf("%w: redirect_uri is not registered for this client", ErrInvalid)
	}
	if in.CodeChallengeMethod != "S256" {
		return client, oauthError("invalid_request", "PKCE with code_challenge_method S256 is required")
	}
	if n := len(in.CodeChallenge); n < 43 || n > 128 {
		return client, oauthError("invalid_request", "code_challenge is required")
	}
	return client, nil
}

// AuthorizeOAuth records the consent of the signed-in person and returns the
// code the client exchanges for its tokens. scopes are those the person
// approved, whatever the client asked for.
func (s *Service) AuthorizeOAuth(ctx context.Context, i auth.Identity, in OAuthAuthorization) (string, error) {
	if _, err := s.CheckOAuthAuthorization(ctx, in); err != nil {
		var oauth *OAuthError
		if errors.As(err, &oauth) {
			return "", fmt.Errorf("%w: %s", ErrInvalid, oauth.Description)
		}
		return "", err
	}
	if err := domain.ValidateControlScopes(in.Scopes); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	code, err := oauthToken("threavia_ac_")
	if err != nil {
		return "", err
	}
	err = s.store.CreateOAuthCode(ctx, controlHash(code), domain.OAuthCode{
		ClientID: in.ClientID, OwnerID: i.UserID, RedirectURI: in.RedirectURI, CodeChallenge: in.CodeChallenge,
		Scopes: in.Scopes, Resource: in.Resource, ExpiresAt: s.now().Add(oauthCodeLifetime),
	})
	if err != nil {
		return "", translate(err)
	}
	return code, nil
}

// ExchangeOAuthCode turns a code into a grant and its first tokens. The grant
// is a control access, listed and revoked in Settings like any other.
func (s *Service) ExchangeOAuthCode(ctx context.Context, code, clientID, redirectURI, verifier, resource string) (OAuthTokens, error) {
	var tokens OAuthTokens
	var ownerID domain.UserID
	b := &batch{}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		grant, err := tx.UseOAuthCode(ctx, controlHash(code))
		if err != nil {
			if errors.Is(err, postgres.ErrNotFound) {
				return oauthError("invalid_grant", "the code is unknown, expired or already used")
			}
			return err
		}
		if grant.ClientID != clientID || grant.RedirectURI != redirectURI {
			return oauthError("invalid_grant", "the code was issued to another client or redirect_uri")
		}
		if resource != "" && grant.Resource != "" && resource != grant.Resource {
			return oauthError("invalid_target", "the resource differs from the authorization request")
		}
		if n := len(verifier); n < 43 || n > 128 {
			return oauthError("invalid_grant", "code_verifier is required")
		}
		sum := sha256.Sum256([]byte(verifier))
		if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(grant.CodeChallenge)) != 1 {
			return oauthError("invalid_grant", "code_verifier does not match the code_challenge")
		}
		client, err := tx.OAuthClient(ctx, clientID)
		if err != nil {
			return err
		}
		ownerID = grant.OwnerID
		b.ownerID = ownerID
		access, err := tx.CreateOAuthAccess(ctx, domain.ControlAccess{ID: domain.NewUUID(), OwnerID: ownerID, Label: client.Name, Scopes: grant.Scopes}, clientID)
		if err != nil {
			return err
		}
		if err := tx.RecordAudit(ctx, postgres.AuditEntry{OwnerID: ownerID, ActorID: string(ownerID), Action: "control_access.created", SubjectID: access.ID, Channel: "web", Detail: mustJSON(map[string]any{"label": access.Label, "scopes": access.Scopes, "oauthClientId": clientID})}); err != nil {
			return err
		}
		if err := s.appendAll(ctx, tx, b, record{eventType: events.TypeControlAccessChanged, payload: map[string]any{"accessId": access.ID, "action": "created"}}); err != nil {
			return err
		}
		tokens, err = s.issueOAuthTokens(ctx, tx, access)
		return err
	})
	if err != nil {
		return OAuthTokens{}, oauthTranslate(err)
	}
	s.flush(b)
	return tokens, nil
}

// RefreshOAuth rotates a grant's tokens. A refresh token works once; the
// grant must still be live, so revoking it in Settings ends the connection.
func (s *Service) RefreshOAuth(ctx context.Context, refresh, clientID string) (OAuthTokens, error) {
	var tokens OAuthTokens
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		access, owner, err := tx.UseOAuthRefreshToken(ctx, controlHash(refresh))
		if err != nil {
			if errors.Is(err, postgres.ErrNotFound) {
				return oauthError("invalid_grant", "the refresh token is unknown, expired, used or revoked")
			}
			return err
		}
		if owner != clientID {
			return oauthError("invalid_grant", "the refresh token was issued to another client")
		}
		tokens, err = s.issueOAuthTokens(ctx, tx, access)
		return err
	})
	if err != nil {
		return OAuthTokens{}, oauthTranslate(err)
	}
	return tokens, nil
}

func (s *Service) issueOAuthTokens(ctx context.Context, tx *postgres.Store, access domain.ControlAccess) (OAuthTokens, error) {
	accessToken, err := oauthToken("threavia_at_")
	if err != nil {
		return OAuthTokens{}, err
	}
	refreshToken, err := oauthToken("threavia_rt_")
	if err != nil {
		return OAuthTokens{}, err
	}
	now := s.now()
	if err := tx.CreateOAuthToken(ctx, controlHash(accessToken), access.ID, "access", now.Add(oauthAccessLifetime)); err != nil {
		return OAuthTokens{}, err
	}
	if err := tx.CreateOAuthToken(ctx, controlHash(refreshToken), access.ID, "refresh", now.Add(oauthRefreshLifetime)); err != nil {
		return OAuthTokens{}, err
	}
	return OAuthTokens{AccessToken: accessToken, TokenType: "Bearer", ExpiresIn: int(oauthAccessLifetime.Seconds()), RefreshToken: refreshToken, Scopes: access.Scopes}, nil
}

// PurgeOAuth drops codes and tokens that can no longer be used.
func (s *Service) PurgeOAuth(ctx context.Context) error {
	return translate(s.store.PurgeOAuth(ctx))
}

func oauthToken(prefix string) (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(secret), nil
}

func oauthTranslate(err error) error {
	var oauth *OAuthError
	if errors.As(err, &oauth) {
		return oauth
	}
	return translate(err)
}
