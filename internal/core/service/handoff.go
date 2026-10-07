package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// Handoff is what moving a Session to another BackendInstance produced.
type Handoff struct {
	Run domain.Run `json:"run"`
	// MissingSkills are the backend-local Skills the previous backend had and
	// the new one does not. Core knows their names and never their content, so
	// this is the most it can say — and saying it is the point of recording that
	// metadata at all (spec section 18).
	MissingSkills []string `json:"missingSkills,omitempty"`
}

// MoveSessionToBackend starts a new Run of a Session on another
// BackendInstance.
//
// A backend change is explicit and visible (spec section 34): the timeline
// stays continuous, the native provider session does not travel — it belongs to
// the machine that holds it — and the new Run starts fresh. Core refuses while
// work is in flight rather than leaving a Job behind on a backend nobody is
// watching any more.
func (s *Service) MoveSessionToBackend(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, instanceID domain.BackendInstanceID) (Handoff, error) {
	session, err := s.store.GetSession(ctx, identity.UserID, sessionID)
	if err != nil {
		return Handoff{}, translate(err)
	}
	if session.Status != domain.SessionActive {
		return Handoff{}, fmt.Errorf("%w: the session is archived", ErrConflict)
	}
	if err := s.checkBackendUsable(ctx, identity, instanceID); err != nil {
		return Handoff{}, err
	}

	current, err := s.store.LatestRun(ctx, sessionID)
	if err != nil {
		return Handoff{}, translate(err)
	}
	if current.BackendInstanceID == instanceID {
		return Handoff{}, fmt.Errorf("%w: the session already runs on this backend", ErrConflict)
	}
	if active, err := s.store.ActiveJob(ctx, current.ID); err == nil {
		return Handoff{}, fmt.Errorf("%w: job %s is still running, cancel it first", ErrConflict, active.ID)
	} else if !errors.Is(err, postgres.ErrNotFound) {
		return Handoff{}, translate(err)
	}

	handoff := Handoff{
		Run: domain.Run{
			ID:                domain.NewRunID(),
			SessionID:         sessionID,
			BackendInstanceID: instanceID,
			// A native provider session belongs to the machine that holds it, so
			// the new Run has none until the new backend binds one.
			ResumeStatus: domain.ResumeUnknown,
		},
	}
	handoff.MissingSkills = s.skillsLostInHandoff(ctx, current.BackendInstanceID, instanceID)

	scope := domain.Scope{ProjectID: session.ProjectID, SessionID: sessionID, RunID: handoff.Run.ID}
	b := &batch{ownerID: identity.UserID}

	err = s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if err := tx.CreateRun(ctx, &handoff.Run); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b, record{
			events.TypeRunCreated, scope,
			RunCreatedPayload{BackendInstanceID: string(instanceID)},
		})
	})
	if err != nil {
		return Handoff{}, translate(err)
	}

	s.flush(b)
	s.audit(ctx, identity, postgres.AuditEntry{
		Action:    "session.backend_changed",
		ProjectID: string(session.ProjectID),
		SessionID: string(sessionID),
		SubjectID: string(instanceID),
		Detail: mustJSON(map[string]any{
			"from":          string(current.BackendInstanceID),
			"to":            string(instanceID),
			"missingSkills": handoff.MissingSkills,
		}),
	})
	return handoff, nil
}

// skillsLostInHandoff names the backend-local Skills the previous backend had
// and the new one does not.
//
// Core compares metadata only: it never held the content of either, which is
// exactly what lets a backend expose a Skill that only exists behind a corporate
// network and still have Core warn that moving the work loses it.
func (s *Service) skillsLostInHandoff(ctx context.Context, from, to domain.BackendInstanceID) []string {
	before, err := s.store.ListBackendSkills(ctx, from)
	if err != nil || len(before) == 0 {
		return nil
	}
	after, err := s.store.ListBackendSkills(ctx, to)
	if err != nil {
		return nil
	}

	available := make([]string, 0, len(after))
	for _, skill := range after {
		if skill.Available {
			available = append(available, skill.Name)
		}
	}

	var missing []string
	for _, skill := range before {
		if skill.Available && !slices.Contains(available, skill.Name) {
			missing = append(missing, skill.Name)
		}
	}
	return missing
}
