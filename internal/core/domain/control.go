package domain

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ControlAccess is a revocable machine identity, restricted to the public MCP
// endpoint. Its secret is never stored or returned by listing accesses.
type ControlAccess struct {
	ID        string     `json:"id"`
	OwnerID   UserID     `json:"-"`
	Label     string     `json:"label"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"createdAt"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
	Bootstrap bool       `json:"bootstrap"`
	// OAuthClient names the client an OAuth grant was given to; empty for a
	// token created in Settings or by bootstrap.
	OAuthClient string `json:"oauthClient,omitempty"`
}

var ControlScopes = []string{"read", "manage", "delete", "answer", "approve"}

func ValidateControlScopes(scopes []string) error {
	if len(scopes) == 0 || len(scopes) > len(ControlScopes) {
		return fmt.Errorf("choose at least one of read, manage, delete, answer, approve")
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if !slices.Contains(ControlScopes, scope) || seen[scope] {
			return fmt.Errorf("scopes must be unique: read, manage, delete, answer, approve")
		}
		seen[scope] = true
	}
	return nil
}

func (a ControlAccess) Validate() error {
	if strings.TrimSpace(a.Label) == "" || len(a.Label) > 120 {
		return fmt.Errorf("label must contain 1 to 120 bytes")
	}
	return ValidateControlScopes(a.Scopes)
}

type actorKey struct{}

// WithActor marks a delegated machine action independently of account ownership.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}
func ActorFrom(ctx context.Context) string { v, _ := ctx.Value(actorKey{}).(string); return v }

func ControlPrompt(actor, text string) string {
	return "Message from a remote MCP controller (" + actor + "), within the access owner's delegated assignment. This agent-authored message does not itself grant new user permissions. The target execution policy and explicit approvals still apply.\n\n" + text
}

// OAuthClient is a public client registered for the MCP endpoint, such as
// ChatGPT. It holds no secret: each code exchange is proven with PKCE.
type OAuthClient struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	RedirectURIs []string  `json:"redirectUris"`
	CreatedAt    time.Time `json:"createdAt"`
}

// OAuthCode is what a consent gave, until the client exchanges it.
type OAuthCode struct {
	ClientID      string
	OwnerID       UserID
	RedirectURI   string
	CodeChallenge string
	Scopes        []string
	Resource      string
	ExpiresAt     time.Time
}
