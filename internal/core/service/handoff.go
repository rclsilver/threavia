package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

type Handoff struct {
	Run           domain.Run `json:"run"`
	MissingSkills []string   `json:"missingSkills,omitempty"`
}

// HandoffPreview describes the destination before an explicit move. Paths and
// skills are metadata only; Core neither reads nor copies the source workspace.
type HandoffPreview struct {
	FromRunID                domain.RunID           `json:"fromRunId"`
	Backend                  domain.BackendInstance `json:"backend"`
	CanMove                  bool                   `json:"canMove"`
	Reasons                  []string               `json:"reasons"`
	Warnings                 []string               `json:"warnings"`
	MissingSkills            []string               `json:"missingSkills"`
	WorkingDirectory         HandoffDirectory       `json:"workingDirectory"`
	NativeSessionTransferred bool                   `json:"nativeSessionTransferred"`
}

type HandoffDirectory struct {
	KnownDirectoryID domain.KnownDirectoryID `json:"knownDirectoryId,omitempty"`
	Name             string                  `json:"name,omitempty"`
	Path             string                  `json:"path,omitempty"`
	Resolution       string                  `json:"resolution"`
}

func (s *Service) PreviewHandoff(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, instanceID domain.BackendInstanceID) (HandoffPreview, error) {
	p, err := s.previewHandoff(ctx, s.store, identity, sessionID, instanceID, nil, nil)
	return p, translate(err)
}

// model and effort, when set, are the values the session will have after the
// move, so the destination is judged against them rather than the old ones.
func (s *Service) previewHandoff(ctx context.Context, store *postgres.Store, identity auth.Identity, sessionID domain.SessionID, instanceID domain.BackendInstanceID, model, effort *string) (HandoffPreview, error) {
	p := HandoffPreview{Reasons: []string{}, Warnings: []string{}, MissingSkills: []string{}, WorkingDirectory: HandoffDirectory{Resolution: "backend_default"}}
	session, err := store.GetSession(ctx, identity.UserID, sessionID)
	if err != nil {
		return p, err
	}
	backend, err := store.GetBackendInstance(ctx, identity.UserID, instanceID)
	if err != nil {
		return p, err
	}
	current, err := store.LatestRun(ctx, sessionID)
	if err != nil {
		return p, err
	}
	p.FromRunID, p.Backend = current.ID, backend
	if session.Status != domain.SessionActive {
		p.Reasons = append(p.Reasons, "The session is archived.")
	}
	if current.BackendInstanceID == instanceID {
		p.Reasons = append(p.Reasons, "The session already uses this backend.")
	}
	if backend.OwnershipStatus == domain.BackendRevoked {
		p.Reasons = append(p.Reasons, "The backend is revoked.")
	}
	if !backend.HasCapability(domain.CapabilityCode) {
		p.Reasons = append(p.Reasons, "The backend does not support CODE work.")
	}
	if backend.Capacity.MaxConcurrentRuns <= 0 {
		p.Reasons = append(p.Reasons, "The backend is draining and accepts no new work.")
	} else if backend.Capacity.ActiveRuns >= backend.Capacity.MaxConcurrentRuns {
		p.Reasons = append(p.Reasons, "The backend is full.")
	}
	selected, reasoning := session.Model, session.Effort
	if model != nil {
		selected = *model
	}
	if effort != nil {
		reasoning = *effort
	}
	if _, _, err := backend.ResolveModel(selected, reasoning); err != nil {
		p.Reasons = append(p.Reasons, "Session model or effort is incompatible: "+err.Error())
	}
	pending, err := store.SessionHasPendingJobs(ctx, sessionID)
	if err != nil {
		return p, err
	}
	if pending {
		p.Reasons = append(p.Reasons, "Finish or cancel all running and queued jobs before moving.")
	}
	if _, connected := s.backends.Lookup(instanceID); !connected {
		p.Backend.OperationalStatus = domain.BackendOffline
	}
	if p.Backend.OperationalStatus != domain.BackendReady {
		p.Warnings = append(p.Warnings, "The backend is "+strings.ToLower(string(p.Backend.OperationalStatus))+"; the next job may wait for it.")
	}
	if backend.ProviderAuthState == domain.ProviderAuthenticationRequired {
		p.Warnings = append(p.Warnings, "Provider sign-in is required on the destination backend.")
	}
	if session.WorkingDirectoryID != nil {
		dir, err := store.GetKnownDirectory(ctx, identity.UserID, domain.KnownDirectoryID(*session.WorkingDirectoryID))
		if err != nil {
			return p, err
		}
		p.WorkingDirectory = HandoffDirectory{KnownDirectoryID: dir.ID, Name: dir.Name, Resolution: "resolve"}
		bindings, err := store.ListBindings(ctx, identity.UserID, dir.ID)
		if err != nil {
			return p, err
		}
		for _, binding := range bindings {
			if binding.BackendInstanceID == instanceID {
				p.WorkingDirectory.Path, p.WorkingDirectory.Resolution = binding.Path, "bound"
				break
			}
		}
	}
	p.MissingSkills, err = skillsLostInHandoff(ctx, store, current.BackendInstanceID, instanceID)
	if err != nil {
		return p, err
	}
	p.CanMove = len(p.Reasons) == 0
	return p, nil
}

// The expected Run guards a reviewed destination against a move from another
// client. Empty is retained for older API clients; all state is still rechecked.
func (s *Service) MoveSessionToBackend(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, instanceID domain.BackendInstanceID, expectedRunID domain.RunID) (Handoff, error) {
	return s.moveSessionToBackend(ctx, identity, sessionID, instanceID, expectedRunID, nil, nil)
}

// moveSessionToBackend can change the session model and effort in the same
// transaction, for a destination whose catalog differs from the source.
func (s *Service) moveSessionToBackend(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, instanceID domain.BackendInstanceID, expectedRunID domain.RunID, model, effort *string) (Handoff, error) {
	var result Handoff
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		session, err := tx.LockSession(ctx, identity.UserID, sessionID)
		if err != nil {
			return err
		}
		if _, err := tx.LockBackendInstance(ctx, instanceID); err != nil {
			return err
		}
		preview, err := s.previewHandoff(ctx, tx, identity, sessionID, instanceID, model, effort)
		if err != nil {
			return err
		}
		if expectedRunID != "" && expectedRunID != preview.FromRunID {
			return fmt.Errorf("%w: the session moved since this preview; review the destination again", ErrConflict)
		}
		if !preview.CanMove {
			return fmt.Errorf("%w: %s", ErrConflict, strings.Join(preview.Reasons, " "))
		}
		current, err := tx.LatestRun(ctx, sessionID)
		if err != nil {
			return err
		}
		result = Handoff{Run: domain.Run{ID: domain.NewRunID(), SessionID: sessionID, BackendInstanceID: instanceID, ResumeStatus: domain.ResumeUnknown}, MissingSkills: preview.MissingSkills}
		if err := tx.CreateRun(ctx, &result.Run); err != nil {
			return err
		}
		scope := domain.Scope{ProjectID: session.ProjectID, SessionID: sessionID, RunID: result.Run.ID}
		if err := s.recordLifecycleAudit(ctx, tx, identity, events.Type("session.backend_changed"), scope, string(instanceID), map[string]any{"from": current.BackendInstanceID, "to": instanceID, "missingSkills": result.MissingSkills}); err != nil {
			return err
		}
		if err := s.appendAll(ctx, tx, b, record{events.TypeRunCreated, scope, RunCreatedPayload{BackendInstanceID: string(instanceID), FromBackendInstanceID: string(current.BackendInstanceID), BackendName: preview.Backend.Name}}); err != nil {
			return err
		}
		selected, reasoning := session.Model, session.Effort
		if model != nil {
			selected = *model
		}
		if effort != nil {
			reasoning = *effort
		}
		if selected == session.Model && reasoning == session.Effort {
			return nil
		}
		if _, err := tx.SetSessionModel(ctx, identity.UserID, sessionID, selected, reasoning); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b, record{events.TypeSessionModelChanged, domain.Scope{ProjectID: session.ProjectID, SessionID: sessionID}, ModelPayload{selected, reasoning}})
	})
	if err != nil {
		return Handoff{}, translate(err)
	}
	s.flush(b)
	return result, nil
}

func skillsLostInHandoff(ctx context.Context, store *postgres.Store, from, to domain.BackendInstanceID) ([]string, error) {
	before, err := store.ListBackendSkills(ctx, from)
	if err != nil {
		return nil, err
	}
	after, err := store.ListBackendSkills(ctx, to)
	if err != nil {
		return nil, err
	}
	available := map[string]bool{}
	for _, skill := range after {
		if skill.Available {
			available[skill.Name] = true
		}
	}
	missing := []string{}
	for _, skill := range before {
		if skill.Available && !available[skill.Name] {
			missing = append(missing, skill.Name)
		}
	}
	slices.Sort(missing)
	return slices.Compact(missing), nil
}
