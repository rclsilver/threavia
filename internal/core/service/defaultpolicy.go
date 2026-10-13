package service

import (
	"context"
	"fmt"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// PolicyView distinguishes inherited values from what this level actually sets.
type PolicyView struct {
	Effective domain.ExecutionPolicy  `json:"effective"`
	Parent    domain.ExecutionPolicy  `json:"parent"`
	Own       *domain.ExecutionPolicy `json:"own,omitempty"`
	Inherited bool                    `json:"inherited"`
}

func policyView(parent, own *domain.ExecutionPolicy) PolicyView {
	return PolicyView{Effective: domain.Effective(parent, own), Parent: domain.Effective(parent), Own: own, Inherited: own == nil}
}

// InstancePolicyOrigin returns the caller's defaults across this installation.
// Accounts are isolated, as they are for projects and backend ownership.
func (s *Service) InstancePolicyOrigin(ctx context.Context, identity auth.Identity) (PolicyView, error) {
	own, err := s.store.InstancePolicy(ctx, identity.UserID)
	return policyView(nil, own), translate(err)
}

func (s *Service) BackendPolicyOrigin(ctx context.Context, identity auth.Identity, id domain.BackendInstanceID) (PolicyView, error) {
	own, err := s.store.BackendPolicy(ctx, identity.UserID, id)
	if err != nil {
		return PolicyView{}, translate(err)
	}
	parent, err := s.store.InstancePolicy(ctx, identity.UserID)
	return policyView(parent, own), translate(err)
}

// A project has no backend until work is dispatched. Its view falls back to the
// instance; a session's view additionally includes its current backend.
func (s *Service) ProjectPolicyOrigin(ctx context.Context, identity auth.Identity, id domain.ProjectID) (PolicyView, error) {
	if _, err := s.store.GetProject(ctx, identity.UserID, id); err != nil {
		return PolicyView{}, translate(err)
	}
	own, err := s.store.ProjectPolicy(ctx, id)
	if err != nil {
		return PolicyView{}, translate(err)
	}
	parent, err := s.store.InstancePolicy(ctx, identity.UserID)
	return policyView(parent, own), translate(err)
}

func (s *Service) SetInstanceExecutionPolicy(ctx context.Context, identity auth.Identity, policy *domain.ExecutionPolicy) (PolicyView, error) {
	if policy != nil {
		if err := policy.Validate(); err != nil {
			return PolicyView{}, fmt.Errorf("%w: %s", ErrInvalid, err)
		}
	}
	if err := s.store.SetInstanceExecutionPolicy(ctx, identity.UserID, policy); err != nil {
		return PolicyView{}, translate(err)
	}
	view, err := s.InstancePolicyOrigin(ctx, identity)
	if err != nil {
		return PolicyView{}, err
	}
	s.audit(ctx, identity, postgres.AuditEntry{Action: "execution_policy.instance_set", SubjectID: string(identity.UserID), Detail: mustJSON(view)})
	s.pushDefaultPolicies(ctx, identity)
	return view, nil
}

func (s *Service) SetBackendExecutionPolicy(ctx context.Context, identity auth.Identity, id domain.BackendInstanceID, policy *domain.ExecutionPolicy) (PolicyView, error) {
	if policy != nil {
		if err := policy.Validate(); err != nil {
			return PolicyView{}, fmt.Errorf("%w: %s", ErrInvalid, err)
		}
	}
	if err := s.store.SetBackendExecutionPolicy(ctx, identity.UserID, id, policy); err != nil {
		return PolicyView{}, translate(err)
	}
	view, err := s.BackendPolicyOrigin(ctx, identity, id)
	if err != nil {
		return PolicyView{}, err
	}
	s.audit(ctx, identity, postgres.AuditEntry{Action: "execution_policy.backend_set", SubjectID: string(id), Detail: mustJSON(view)})
	s.pushDefaultPolicies(ctx, identity)
	return view, nil
}

func (s *Service) pushDefaultPolicies(ctx context.Context, identity auth.Identity) {
	projects, err := s.store.ListProjects(ctx, identity.UserID, true)
	if err != nil {
		s.logger.Error("cannot list projects for changed defaults", "error", err)
		return
	}
	for _, project := range projects {
		sessions, err := s.store.ListSessions(ctx, identity.UserID, project.ID, true)
		if err != nil {
			s.logger.Error("cannot list sessions for changed defaults", "error", err)
			continue
		}
		for _, session := range sessions {
			s.pushPolicy(ctx, session)
		}
	}
}
