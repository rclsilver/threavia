// Package auth implements the Core authentication modes described in
// THREAVIA_SPEC_V1.md section 20: none, basic and oidc.
//
// Only the identity of the human user is handled here. BackendInstance identity
// is a separate concern handled by the backend control connection, and provider
// credentials never reach Core at all.
package auth

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"

	"github.com/rclsilver/threavia/internal/core/domain"
)

// Mode selects how Core authenticates client requests.
type Mode string

const (
	// ModeNone disables authentication and attributes every request to a single
	// local user. Suitable for local development only.
	ModeNone Mode = "none"
	// ModeBasic uses HTTP basic authentication against a configured credential.
	ModeBasic Mode = "basic"
	// ModeOIDC delegates authentication to an OIDC provider such as Keycloak.
	ModeOIDC Mode = "oidc"
)

func (m Mode) String() string { return string(m) }

// Valid reports whether m is a known Mode.
func (m Mode) Valid() bool {
	switch m {
	case ModeNone, ModeBasic, ModeOIDC:
		return true
	default:
		return false
	}
}

// ErrUnauthenticated means the request carried no usable credential or an
// invalid one. It is the single answer to every authentication failure: an
// expired token and a forged one are not told apart.
var ErrUnauthenticated = errors.New("unauthenticated")

// Identity is the authenticated caller. Every user-scoped query and mutation in
// Core is checked against Identity.UserID (spec sections 20 and 28).
type Identity struct {
	UserID  domain.UserID
	Subject string
	Email   string
	Name    string
}

// Authenticator resolves the Identity behind an HTTP request.
type Authenticator interface {
	// Mode reports the configured authentication mode.
	Mode() Mode
	// Authenticate returns the caller identity, or an error wrapping
	// ErrUnauthenticated.
	Authenticate(r *http.Request) (Identity, error)
}

// Config holds the authentication settings loaded from the environment.
type Config struct {
	Mode Mode

	// DevUserID is the user every request is attributed to in ModeNone.
	DevUserID string

	// BasicUsername and BasicPassword are the ModeBasic credential.
	BasicUsername string
	BasicPassword string

	// OIDC settings, used by ModeOIDC.
	OIDCIssuer   string
	OIDCClientID string
	OIDCAudience string
	// OIDCUserClaim names the claim that identifies the user. The subject is the
	// only claim an issuer guarantees to be stable and unique, so it is the
	// default; a deployment that prefers a readable identifier says so.
	OIDCUserClaim string
}

// Validate checks the configuration consistency for the selected mode.
func (c Config) Validate() error {
	if !c.Mode.Valid() {
		return fmt.Errorf("unknown authentication mode %q", c.Mode)
	}
	switch c.Mode {
	case ModeNone:
		if c.DevUserID == "" {
			return errors.New("mode none requires a development user id")
		}
	case ModeBasic:
		if c.BasicUsername == "" || c.BasicPassword == "" {
			return errors.New("mode basic requires a username and a password")
		}
	case ModeOIDC:
		if c.OIDCIssuer == "" || c.OIDCClientID == "" {
			return errors.New("mode oidc requires an issuer and a client id")
		}
	}
	return nil
}

// New builds the Authenticator for the configured mode.
func New(cfg Config) (Authenticator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Mode {
	case ModeNone:
		return noneAuthenticator{userID: domain.UserID(cfg.DevUserID)}, nil
	case ModeBasic:
		return basicAuthenticator{username: cfg.BasicUsername, password: cfg.BasicPassword}, nil
	case ModeOIDC:
		return newOIDCAuthenticator(cfg)
	default:
		return nil, fmt.Errorf("unknown authentication mode %q", cfg.Mode)
	}
}

type noneAuthenticator struct {
	userID domain.UserID
}

func (noneAuthenticator) Mode() Mode { return ModeNone }

func (a noneAuthenticator) Authenticate(*http.Request) (Identity, error) {
	return Identity{UserID: a.userID, Subject: string(a.userID), Name: string(a.userID)}, nil
}

type basicAuthenticator struct {
	username string
	password string
}

func (basicAuthenticator) Mode() Mode { return ModeBasic }

func (a basicAuthenticator) Authenticate(r *http.Request) (Identity, error) {
	username, password, ok := r.BasicAuth()
	if !ok {
		return Identity{}, ErrUnauthenticated
	}
	userMatch := subtle.ConstantTimeCompare([]byte(username), []byte(a.username))
	passMatch := subtle.ConstantTimeCompare([]byte(password), []byte(a.password))
	if userMatch&passMatch != 1 {
		return Identity{}, ErrUnauthenticated
	}
	return Identity{UserID: domain.UserID(username), Subject: username, Name: username}, nil
}
