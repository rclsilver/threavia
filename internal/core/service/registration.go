package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/backendconn"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// Registration defaults.
const (
	// claimCodeTTL bounds how long a one-time claim code stays usable.
	claimCodeTTL = 15 * time.Minute
	// defaultTokenTTL bounds a one-shot registration token.
	defaultTokenTTL = 24 * time.Hour
	// claimCodeAlphabet excludes characters a human confuses when typing.
	claimCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	claimCodeLength   = 10
)

// ErrRegistrationRejected is returned when neither a valid one-shot token nor
// the shared registration key was presented.
var ErrRegistrationRejected = errors.New("registration rejected")

// RegistrationConfig holds the Core-side registration settings.
type RegistrationConfig struct {
	// SharedKey enables the shared-key flow when set. A backend registering
	// with it is created UNCLAIMED and must be claimed by a user.
	SharedKey string
}

// SetRegistration installs the registration settings.
func (s *Service) SetRegistration(cfg RegistrationConfig) { s.registration = cfg }

// RegisterBackendInput is what a backend presents when it first registers.
type RegisterBackendInput struct {
	// Name is the instance name the backend proposes.
	Name string
	// Capabilities are what this backend can do. They are declared here rather
	// than only on connection, so Core can queue work for a backend that is
	// currently offline. Every Hello refreshes them.
	Capabilities []domain.Capability
	// Token is a one-shot user token, which makes the instance owned
	// immediately with no claim step.
	Token string
	// SharedKey is the shared registration key, which creates an UNCLAIMED
	// instance plus a one-time claim code.
	SharedKey string
}

// RegisterBackendResult is what the backend stores locally and, for the shared
// key flow, displays to its user.
type RegisterBackendResult struct {
	BackendInstanceID domain.BackendInstanceID `json:"backendInstanceId"`
	// Credential is the persistent backend credential. It is returned once and
	// never again: Core only keeps its hash.
	Credential string `json:"credential"`
	// ClaimCode is set by the shared-key flow. The backend displays it locally
	// and the user enters it once.
	ClaimCode string `json:"claimCode,omitempty"`
	Claimed   bool   `json:"claimed"`
}

// RegisterBackend creates a BackendInstance and issues its persistent
// credential (spec section 8).
//
// Both flows end with the backend holding a credential it presents on every
// connection, so registration material is not required at each reboot.
func (s *Service) RegisterBackend(ctx context.Context, in RegisterBackendInput) (RegisterBackendResult, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return RegisterBackendResult{}, fmt.Errorf("%w: an instance name is required", ErrInvalid)
	}
	if len(in.Capabilities) == 0 {
		return RegisterBackendResult{}, fmt.Errorf("%w: a backend must declare at least one capability", ErrInvalid)
	}
	for _, capability := range in.Capabilities {
		if !capability.Valid() {
			return RegisterBackendResult{}, fmt.Errorf("%w: unknown capability %q", ErrInvalid, capability)
		}
	}

	credential, err := randomToken()
	if err != nil {
		return RegisterBackendResult{}, err
	}

	instance := domain.BackendInstance{
		ID:                domain.NewBackendInstanceID(),
		Name:              name,
		OperationalStatus: domain.BackendOffline,
		Capabilities:      in.Capabilities,
		Capacity:          domain.Capacity{MaxConcurrentRuns: 1},
	}
	result := RegisterBackendResult{Credential: credential}

	switch {
	case in.Token != "":
		// One-shot user token: the instance is owned immediately.
		instance.OwnershipStatus = domain.BackendClaimed
		err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
			ownerID, err := tx.ConsumeRegistrationToken(ctx, hashToken(in.Token), instance.ID)
			if err != nil {
				if errors.Is(err, postgres.ErrNotFound) {
					return fmt.Errorf("%w: unknown, expired or already used token", ErrRegistrationRejected)
				}
				return err
			}
			instance.OwnerID = &ownerID
			return tx.CreateBackendInstance(ctx, &instance, hashToken(credential), nil, nil)
		})
		if err != nil {
			return RegisterBackendResult{}, translate(err)
		}
		result.Claimed = true

	case in.SharedKey != "":
		if s.registration.SharedKey == "" {
			return RegisterBackendResult{}, fmt.Errorf("%w: shared key registration is disabled", ErrRegistrationRejected)
		}
		if subtle.ConstantTimeCompare([]byte(in.SharedKey), []byte(s.registration.SharedKey)) != 1 {
			return RegisterBackendResult{}, fmt.Errorf("%w: invalid shared key", ErrRegistrationRejected)
		}

		claimCode, err := randomClaimCode()
		if err != nil {
			return RegisterBackendResult{}, err
		}
		claimHash := hashToken(claimCode)
		expiry := s.now().Add(claimCodeTTL)
		instance.OwnershipStatus = domain.BackendUnclaimed

		if err := s.store.CreateBackendInstance(ctx, &instance, hashToken(credential), &claimHash, &expiry); err != nil {
			return RegisterBackendResult{}, translate(err)
		}
		result.ClaimCode = claimCode

	default:
		return RegisterBackendResult{}, fmt.Errorf("%w: a registration token or the shared key is required", ErrRegistrationRejected)
	}

	result.BackendInstanceID = instance.ID
	s.logger.Info("backend registered",
		"backendInstanceId", instance.ID, "name", name, "claimed", result.Claimed)
	return result, nil
}

// ClaimBackend associates an UNCLAIMED instance with the calling user. The claim
// code is single use and expires.
func (s *Service) ClaimBackend(ctx context.Context, identity auth.Identity, claimCode string) (domain.BackendInstance, error) {
	claimCode = normaliseClaimCode(claimCode)
	if claimCode == "" {
		return domain.BackendInstance{}, fmt.Errorf("%w: a claim code is required", ErrInvalid)
	}

	instance, err := s.store.ClaimBackendInstance(ctx, identity.UserID, hashToken(claimCode))
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return domain.BackendInstance{}, fmt.Errorf("%w: unknown or expired claim code", ErrNotFound)
		}
		return domain.BackendInstance{}, translate(err)
	}

	s.emit(ctx, identity.UserID, events.TypeBackendRegistered, domain.Scope{}, map[string]string{
		"backendInstanceId": string(instance.ID),
		"name":              instance.Name,
	})
	return instance, nil
}

// CreateRegistrationToken issues a one-shot token a user hands to a backend. The
// plaintext is returned once; Core keeps only its hash.
func (s *Service) CreateRegistrationToken(ctx context.Context, identity auth.Identity, label string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = defaultTokenTTL
	}
	token, err := randomToken()
	if err != nil {
		return "", time.Time{}, err
	}

	expiresAt := s.now().Add(ttl)
	if err := s.store.CreateRegistrationToken(ctx, domain.NewUUID(), identity.UserID,
		hashToken(token), strings.TrimSpace(label), expiresAt); err != nil {
		return "", time.Time{}, translate(err)
	}
	return token, expiresAt, nil
}

// ListBackendInstances returns the BackendInstances of the caller.
func (s *Service) ListBackendInstances(ctx context.Context, identity auth.Identity) ([]domain.BackendInstance, error) {
	instances, err := s.store.ListBackendInstances(ctx, identity.UserID)
	if err != nil {
		return nil, translate(err)
	}
	// A backend is OFFLINE unless this Core process is terminating its stream.
	for i := range instances {
		if _, connected := s.backends.Lookup(instances[i].ID); !connected {
			instances[i].OperationalStatus = domain.BackendOffline
		}
	}
	return instances, nil
}

// GetBackendInstance returns one BackendInstance of the caller.
func (s *Service) GetBackendInstance(ctx context.Context, identity auth.Identity, id domain.BackendInstanceID) (domain.BackendInstance, error) {
	instance, err := s.store.GetBackendInstance(ctx, identity.UserID, id)
	return instance, translate(err)
}

// RevokeBackendInstance invalidates the persistent credential. The record stays
// for historical references, and a returning backend must register anew.
func (s *Service) RevokeBackendInstance(ctx context.Context, identity auth.Identity, id domain.BackendInstanceID) error {
	if err := s.store.RevokeBackendInstance(ctx, identity.UserID, id); err != nil {
		return translate(err)
	}
	if conn, ok := s.backends.Lookup(id); ok {
		s.backends.Release(conn)
	}
	s.emit(ctx, identity.UserID, events.TypeBackendRevoked, domain.Scope{}, map[string]string{
		"backendInstanceId": string(id),
	})
	return nil
}

// Resolve implements backendconn.TokenResolver: it turns the credential a
// backend presents on Connect into its identity.
func (s *Service) Resolve(ctx context.Context, token string) (domain.BackendInstanceID, error) {
	instance, err := s.store.BackendInstanceByCredential(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, postgres.ErrNotFound) {
			return "", backendconn.ErrUnknownBackendToken
		}
		return "", err
	}
	return instance.ID, nil
}

// hashToken is the one-way function behind every stored credential: Core can
// verify what a backend presents but can never reproduce it.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// randomToken returns 32 bytes from the system CSPRNG, URL-safe encoded.
func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// randomClaimCode returns a short code a human can read out and type.
func randomClaimCode() (string, error) {
	buf := make([]byte, claimCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate claim code: %w", err)
	}

	var b strings.Builder
	for i, v := range buf {
		if i == claimCodeLength/2 {
			b.WriteByte('-')
		}
		b.WriteByte(claimCodeAlphabet[int(v)%len(claimCodeAlphabet)])
	}
	return b.String(), nil
}

// normaliseClaimCode accepts the code however the user typed it.
func normaliseClaimCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
}
