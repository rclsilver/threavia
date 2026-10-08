package domain

import (
	"encoding/json"
	"time"
)

// Identifiers of the persistent actionable objects (spec section 6).
type (
	// ValidationRequestID identifies a ValidationRequest.
	ValidationRequestID string
	// UserInputRequestID identifies a UserInputRequest.
	UserInputRequestID string
)

// NewValidationRequestID returns a fresh ValidationRequestID.
func NewValidationRequestID() ValidationRequestID { return ValidationRequestID(NewUUID()) }

// NewUserInputRequestID returns a fresh UserInputRequestID.
func NewUserInputRequestID() UserInputRequestID { return UserInputRequestID(NewUUID()) }

// Scope locates an attention item in the Project hierarchy.
type Scope struct {
	ProjectID ProjectID `json:"projectId"`
	SessionID SessionID `json:"sessionId"`
	RunID     RunID     `json:"runId"`
	JobID     JobID     `json:"jobId"`
}

// ValidationRequest is a provider-independent permission or approval request
// (spec section 16). It is a persistent Core object, not a transient event: it
// waits indefinitely and the first valid resolution wins.
type ValidationRequest struct {
	ID    ValidationRequestID `json:"id"`
	Scope Scope               `json:"scope"`
	// BackendRequestID is the backend-scoped id, so a replayed event resolves
	// to the same request.
	BackendRequestID string          `json:"-"`
	Status           AttentionStatus `json:"status"`
	Title            string          `json:"title"`
	Summary          string          `json:"summary,omitempty"`
	// RequestPayload is the canonical technical request. A technical client
	// renders it; an INTERACTION backend humanizes it.
	RequestPayload json.RawMessage `json:"requestPayload"`
	// PayloadSHA256 is the security reference of the validation receipt.
	PayloadSHA256 string `json:"payloadSha256"`
	// Humanized is an optional presentation kept for audit convenience only.
	Humanized string `json:"humanized,omitempty"`

	// OriginChannel and Notify are the notification relevance of specification
	// section 6: which client started this work, and whether a push is worth
	// sending. They are derived at read time, never stored on the request.
	OriginChannel Channel `json:"originChannel,omitempty"`
	Notify        bool    `json:"notify"`
	// Context is filled when the request is read for a client.
	Context *AttentionContext `json:"context,omitempty"`

	Approved         *bool      `json:"approved,omitempty"`
	ResolvedByUserID *UserID    `json:"resolvedByUserId,omitempty"`
	ResolvedChannel  *string    `json:"resolvedChannel,omitempty"`
	Note             *string    `json:"note,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
}

// UserInputRequest asks the user for an answer, a choice or information. It is
// distinct from a ValidationRequest, which asks for permission.
type UserInputRequest struct {
	ID               UserInputRequestID `json:"id"`
	Scope            Scope              `json:"scope"`
	BackendRequestID string             `json:"-"`
	Status           AttentionStatus    `json:"status"`
	Prompt           string             `json:"prompt"`
	Choices          []string           `json:"choices,omitempty"`
	FreeText         bool               `json:"freeText"`

	// OriginChannel and Notify carry the notification relevance of section 6.
	// They are derived at read time, never stored on the request.
	OriginChannel Channel `json:"originChannel,omitempty"`
	Notify        bool    `json:"notify"`
	// Context is filled when the request is read for a client.
	Context *AttentionContext `json:"context,omitempty"`

	Value            *string    `json:"value,omitempty"`
	ResolvedByUserID *UserID    `json:"resolvedByUserId,omitempty"`
	ResolvedChannel  *string    `json:"resolvedChannel,omitempty"`
	CreatedAt        time.Time  `json:"createdAt"`
	ResolvedAt       *time.Time `json:"resolvedAt,omitempty"`
}

// Attention is the set of pending actionable items a user currently faces. It
// is current state, never a count of unread events: once an item is resolved it
// disappears from every client.
type Attention struct {
	Validations []ValidationRequest `json:"validations"`
	UserInputs  []UserInputRequest  `json:"userInputs"`
}

// Empty reports whether nothing is waiting for the user.
func (a Attention) Empty() bool { return len(a.Validations) == 0 && len(a.UserInputs) == 0 }

// AttentionContext says where a request comes from, in the words a person
// deciding it needs: which session of which project, which machine, which
// directory. Derived at read time, never stored on the request.
type AttentionContext struct {
	SessionTitle string `json:"sessionTitle"`
	ProjectName  string `json:"projectName"`
	BackendName  string `json:"backendName"`
	// Directory is the session's KnownDirectory, empty when it has none.
	Directory string `json:"directory,omitempty"`
}
