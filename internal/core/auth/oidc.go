package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// ErrOIDCUnavailable is returned when the issuer cannot be reached at startup.
// It is a distinct error because the fix is a deployment one: the provider, the
// network or the issuer url, never the request.
var ErrOIDCUnavailable = errors.New("the oidc issuer is unreachable")

// discoveryTimeout bounds the issuer discovery at startup. A provider that is
// slow to answer is a configuration problem an operator must see, not something
// Core waits on forever.
const discoveryTimeout = 15 * time.Second

// oidcAuthenticator verifies a bearer token issued by an OIDC provider such as
// Keycloak (spec section 20).
//
// Core never runs a login flow: a client obtains its token from the provider
// and presents it here. That keeps every Threavia client — web, Android, the
// VS Code extension, the voice-facing services — on the same mechanism, and
// keeps Core out of the business of holding anyone's password.
type oidcAuthenticator struct {
	verifier *oidc.IDTokenVerifier
	// userClaim names the claim that identifies the user. The subject is the
	// only claim an issuer guarantees to be stable and unique, so it is the
	// default; a deployment that prefers a readable identifier says so.
	userClaim string
	// public is what a client is told so it can obtain a token at all.
	public Public
}

// claims is what Core reads from a verified token. Everything else the provider
// sends is ignored: Core models no profile.
type claims struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
}

// newOIDCAuthenticator discovers the issuer and builds the token verifier.
//
// Discovery happens once, at startup, so a misconfigured issuer fails where an
// operator can still see it rather than on the first request. The key set is
// then refreshed by the library as the provider rotates it.
func newOIDCAuthenticator(cfg Config) (Authenticator, error) {
	ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
	defer cancel()

	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrOIDCUnavailable, cfg.OIDCIssuer, err)
	}

	// The audience a token must carry. A deployment where the API is a separate
	// resource from the client application sets it explicitly; otherwise the
	// client id is the audience, which is what a plain OIDC flow produces.
	audience := cfg.OIDCAudience
	if audience == "" {
		audience = cfg.OIDCClientID
	}

	userClaim := cfg.OIDCUserClaim
	if userClaim == "" {
		userClaim = "sub"
	}

	return &oidcAuthenticator{
		verifier:  provider.Verifier(&oidc.Config{ClientID: audience}),
		userClaim: userClaim,
		public: Public{
			Mode:     ModeOIDC,
			Issuer:   cfg.OIDCIssuer,
			ClientID: cfg.OIDCClientID,
			Audience: audience,
		},
	}, nil
}

func (*oidcAuthenticator) Mode() Mode { return ModeOIDC }

func (a *oidcAuthenticator) Public() Public { return a.public }

// Authenticate verifies the bearer token and returns who it names.
func (a *oidcAuthenticator) Authenticate(r *http.Request) (Identity, error) {
	raw := bearerToken(r)
	if raw == "" {
		return Identity{}, ErrUnauthenticated
	}

	token, err := a.verifier.Verify(r.Context(), raw)
	if err != nil {
		// The reason is deliberately not returned to the caller: an expired
		// token and a forged one get the same answer.
		return Identity{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}

	var parsed claims
	if err := token.Claims(&parsed); err != nil {
		return Identity{}, fmt.Errorf("%w: unreadable claims", ErrUnauthenticated)
	}

	userID := a.identify(parsed)
	if userID == "" {
		return Identity{}, fmt.Errorf("%w: the token carries no %s claim", ErrUnauthenticated, a.userClaim)
	}

	return Identity{
		UserID:  domain.UserID(userID),
		Subject: parsed.Subject,
		Email:   parsed.Email,
		Name:    displayName(parsed),
	}, nil
}

// identify reads the claim a deployment chose as the user identity.
func (a *oidcAuthenticator) identify(parsed claims) string {
	switch a.userClaim {
	case "email":
		return parsed.Email
	case "preferred_username":
		return parsed.PreferredUsername
	default:
		return parsed.Subject
	}
}

func displayName(parsed claims) string {
	for _, candidate := range []string{parsed.Name, parsed.PreferredUsername, parsed.Email, parsed.Subject} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

// bearerToken extracts the credential from the Authorization header.
func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	scheme, value, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}
