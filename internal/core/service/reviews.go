package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

var errReviewPending = errors.New("human review is pending")
var errReviewUnchanged = errors.New("review trees are unchanged")

type ReviewAction struct {
	Action          string `json:"action"`
	ExpectedVersion int    `json:"expectedVersion"`
	RevisionID      string `json:"revisionId"`
	RepositoryID    string `json:"repositoryId"`
	ThreadID        string `json:"threadId"`
	MessageID       string `json:"messageId"`
	Body            string `json:"body"`
	Path            string `json:"path"`
	Side            string `json:"side"`
	StartLine       int    `json:"startLine"`
	EndLine         int    `json:"endLine"`
	Resolved        bool   `json:"resolved"`
	Directory       string `json:"directory"`
	Name            string `json:"name"`
	BatchID         string `json:"batchId"`
}

type reviewWait struct {
	backend domain.BackendInstanceID
	answer  chan *backendv1.ReviewWorkspace
}

func (s *Service) ReviewWorkspace(_ context.Context, backend domain.BackendInstanceID, response *backendv1.ReviewWorkspace) {
	value, ok := s.reviewRequests.Load(response.GetRequestId())
	if !ok {
		return
	}
	wait := value.(reviewWait)
	if wait.backend != backend {
		return
	}
	select {
	case wait.answer <- response:
	default:
	}
}

func (s *Service) reviewRead(ctx context.Context, backend domain.BackendInstanceID, req *backendv1.ReviewWorkspaceRequest, out any) error {
	conn, ok := s.backends.Lookup(backend)
	if !ok {
		return ErrBackendUnavailable
	}
	req.RequestId = domain.NewUUID()
	wait := reviewWait{backend: backend, answer: make(chan *backendv1.ReviewWorkspace, 1)}
	s.reviewRequests.Store(req.RequestId, wait)
	defer s.reviewRequests.Delete(req.RequestId)
	if !conn.Send(&backendv1.CoreToBackend{Message: &backendv1.CoreToBackend_ReviewWorkspaceRequest{ReviewWorkspaceRequest: req}}) {
		return ErrBackendUnavailable
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case answer := <-wait.answer:
		if answer.GetError() != nil {
			return fmt.Errorf("%w: %s", ErrConflict, answer.GetError().GetMessage())
		}
		data, err := answer.GetResult().MarshalJSON()
		if err != nil {
			return err
		}
		return json.Unmarshal(data, out)
	case <-timer.C:
		return fmt.Errorf("%w: review snapshot request timed out", ErrBackendUnavailable)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) ReadReview(ctx context.Context, identity auth.Identity, session domain.SessionID) (domain.Review, error) {
	result, err := s.store.ReadReview(ctx, identity.UserID, session)
	return result, translate(err)
}

// editReview serializes publication, approval, drafts and job dispatch on the
// same session lock. No uncommitted state is visible through the event stream.
func (s *Service) editReview(ctx context.Context, identity auth.Identity, session domain.SessionID, version int, edit func(*postgres.Store, *domain.Review) error) (domain.Review, error) {
	var result domain.Review
	b := &batch{ownerID: identity.UserID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		sess, err := tx.LockSession(ctx, identity.UserID, session)
		if err != nil {
			return err
		}
		if sess.Status != domain.SessionActive {
			return fmt.Errorf("%w: restore the session before editing its review", ErrConflict)
		}
		result, err = tx.ReadReview(ctx, identity.UserID, session)
		if err != nil {
			return err
		}
		if version >= 0 && result.Version != version {
			return fmt.Errorf("%w: the review changed on another client; reload it before retrying", ErrConflict)
		}
		if err = edit(tx, &result); err != nil {
			return err
		}
		result.UpdatedAt = s.now()
		if err = tx.SaveReview(ctx, &result); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b, record{events.TypeReviewChanged, domain.Scope{ProjectID: sess.ProjectID, SessionID: session}, map[string]any{"version": result.Version, "status": result.Status, "required": result.Required}})
	})
	if err == nil {
		s.flush(b)
	}
	return result, translate(err)
}

func (s *Service) RegisterReviewRepository(ctx context.Context, identity auth.Identity, session domain.SessionID, directory, name, source string) (domain.Review, error) {
	if !filepath.IsAbs(directory) {
		return domain.Review{}, fmt.Errorf("%w: declare an absolute repository path", ErrInvalid)
	}
	review, err := s.ReadReview(ctx, identity, session)
	if err != nil {
		return review, err
	}
	run, err := s.store.LatestRun(ctx, session)
	if err != nil {
		return review, translate(err)
	}
	for _, repo := range review.Repositories {
		if repo.BackendInstanceID == run.BackendInstanceID && filepath.Clean(repo.Directory) == filepath.Clean(directory) {
			return review, nil
		}
	}
	backend, err := s.store.BackendInstanceByID(ctx, run.BackendInstanceID)
	if err != nil {
		return review, translate(err)
	}
	if !backend.HasFeature(domain.FeatureHumanReview) {
		return review, fmt.Errorf("%w: update this backend to use human reviews", ErrConflict)
	}
	id := domain.NewUUID()
	var snapshot struct{ Directory, GitDirectory, Name, Tree string }
	if err = s.reviewRead(ctx, run.BackendInstanceID, &backendv1.ReviewWorkspaceRequest{Operation: "SNAPSHOT", Directory: directory, RefId: id}, &snapshot); err != nil {
		return review, err
	}
	return s.editReview(ctx, identity, session, -1, func(tx *postgres.Store, r *domain.Review) error {
		latest, err := tx.LatestRun(ctx, session)
		if err != nil {
			return err
		}
		if latest.ID != run.ID {
			return fmt.Errorf("%w: session backend changed during capture", ErrConflict)
		}
		for _, repo := range r.Repositories {
			if repo.BackendInstanceID == run.BackendInstanceID && repo.Directory == snapshot.Directory {
				return nil
			}
		}
		if len(r.Repositories) >= 50 {
			return fmt.Errorf("%w: a review supports up to 50 repositories", ErrInvalid)
		}
		if name == "" {
			name = snapshot.Name
		}
		if len(name) > 200 {
			return fmt.Errorf("%w: repository name is too long", ErrInvalid)
		}
		r.Repositories = append(r.Repositories, domain.ReviewRepository{ID: id, BackendInstanceID: run.BackendInstanceID, Name: name, Directory: snapshot.Directory, GitDirectory: snapshot.GitDirectory, InitialTree: snapshot.Tree, ApprovedTree: snapshot.Tree, BaselineSource: source})
		return nil
	})
}

func (s *Service) captureReview(ctx context.Context, r domain.Review) (domain.ReviewRevision, error) {
	revision := domain.ReviewRevision{ID: domain.NewUUID(), CreatedAt: s.now(), Repositories: []domain.ReviewRevisionRepository{}}
	for _, repo := range r.Repositories {
		state := domain.ReviewRevisionRepository{RepositoryID: repo.ID, BaseTree: repo.ApprovedTree, Files: []domain.ReviewFile{}}
		var snapshot struct{ Tree string }
		if err := s.reviewRead(ctx, repo.BackendInstanceID, &backendv1.ReviewWorkspaceRequest{Operation: "SNAPSHOT", Directory: repo.Directory, RefId: repo.ID}, &snapshot); err != nil {
			state.Error = err.Error()
		} else {
			state.HeadTree = snapshot.Tree
			var list struct{ Files []domain.ReviewFile }
			err = s.reviewRead(ctx, repo.BackendInstanceID, &backendv1.ReviewWorkspaceRequest{Operation: "LIST", Directory: repo.GitDirectory, BaseTree: state.BaseTree, HeadTree: state.HeadTree}, &list)
			if err != nil {
				state.Error = err.Error()
			} else {
				state.Files = list.Files
			}
		}
		revision.Repositories = append(revision.Repositories, state)
	}
	return revision, nil
}

func sameReviewTrees(a, b domain.ReviewRevision) bool {
	if len(a.Repositories) != len(b.Repositories) {
		return false
	}
	for i, r := range a.Repositories {
		other := b.Repositories[i]
		if r.RepositoryID != other.RepositoryID || r.BaseTree != other.BaseTree || r.HeadTree != other.HeadTree || r.Error != other.Error {
			return false
		}
	}
	return true
}

func (s *Service) refreshReview(ctx context.Context, identity auth.Identity, r domain.Review) (domain.Review, error) {
	if len(r.Repositories) == 0 {
		return r, fmt.Errorf("%w: no repositories have been captured yet; declare a repository before modifying it", ErrConflict)
	}
	revision, err := s.captureReview(ctx, r)
	if err != nil {
		return r, err
	}
	if len(r.Revisions) > 0 && sameReviewTrees(r.Revisions[len(r.Revisions)-1], revision) {
		return s.ReadReview(ctx, identity, r.SessionID)
	}
	result, err := s.editReview(ctx, identity, r.SessionID, -1, func(_ *postgres.Store, current *domain.Review) error {
		if len(current.Repositories) != len(revision.Repositories) {
			return fmt.Errorf("%w: repository scope changed during capture", ErrConflict)
		}
		for _, repo := range current.Repositories {
			found := false
			for _, state := range revision.Repositories {
				if state.RepositoryID == repo.ID && state.BaseTree == repo.ApprovedTree {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("%w: approval baseline changed during capture", ErrConflict)
			}
		}
		if len(current.Revisions) > 0 && sameReviewTrees(current.Revisions[len(current.Revisions)-1], revision) {
			return errReviewUnchanged
		}
		current.Revisions = append(current.Revisions, revision)
		if current.Status != "CORRECTING" {
			if current.Required {
				current.Status = "WAITING"
			} else {
				current.Status = "OPEN"
			}
		}
		return nil
	})
	if errors.Is(err, errReviewUnchanged) {
		return result, nil
	}
	return result, err
}

func (s *Service) ReviewFile(ctx context.Context, identity auth.Identity, session domain.SessionID, revisionID, repositoryID, path, compareTo string) (domain.ReviewFileContent, error) {
	r, err := s.ReadReview(ctx, identity, session)
	if err != nil {
		return domain.ReviewFileContent{}, err
	}
	return s.reviewFile(ctx, r, revisionID, repositoryID, path, compareTo)
}

func (s *Service) reviewFile(ctx context.Context, r domain.Review, revisionID, repositoryID, path, compareTo string) (domain.ReviewFileContent, error) {
	var repo *domain.ReviewRepository
	for i := range r.Repositories {
		if r.Repositories[i].ID == repositoryID {
			repo = &r.Repositories[i]
			break
		}
	}
	if repo == nil {
		return domain.ReviewFileContent{}, ErrNotFound
	}
	revisionIndex := slices.IndexFunc(r.Revisions, func(v domain.ReviewRevision) bool { return v.ID == revisionID })
	if revisionIndex < 0 {
		return domain.ReviewFileContent{}, ErrNotFound
	}
	revision := r.Revisions[revisionIndex]
	if compareTo != "" {
		compared, err := s.ReviewComparison(ctx, auth.Identity{}, r, revisionID, compareTo)
		if err != nil {
			return domain.ReviewFileContent{}, err
		}
		revision = compared.Revisions[revisionIndex]
	}
	var state *domain.ReviewRevisionRepository
	for i := range revision.Repositories {
		if revision.Repositories[i].RepositoryID == repositoryID {
			state = &revision.Repositories[i]
			break
		}
	}
	if state == nil || state.Error != "" {
		return domain.ReviewFileContent{}, fmt.Errorf("%w: this repository was not available in that revision", ErrConflict)
	}
	fileIndex := slices.IndexFunc(state.Files, func(f domain.ReviewFile) bool { return f.Path == path })
	if fileIndex < 0 {
		return domain.ReviewFileContent{}, ErrNotFound
	}
	file := state.Files[fileIndex]
	base := state.BaseTree
	if compareTo == "initial" {
		base = repo.InitialTree
	} else if compareTo != "" {
		earlier := slices.IndexFunc(r.Revisions, func(v domain.ReviewRevision) bool { return v.ID == compareTo })
		if earlier < 0 || earlier >= revisionIndex {
			return domain.ReviewFileContent{}, fmt.Errorf("%w: choose an earlier revision", ErrInvalid)
		}
		found := false
		for _, prev := range r.Revisions[earlier].Repositories {
			if prev.RepositoryID == repositoryID && prev.Error == "" {
				base = prev.HeadTree
				found = true
				break
			}
		}
		if !found {
			return domain.ReviewFileContent{}, fmt.Errorf("%w: repository was not captured in the earlier revision", ErrConflict)
		}
	}
	var content domain.ReviewFileContent
	err := s.reviewRead(ctx, repo.BackendInstanceID, &backendv1.ReviewWorkspaceRequest{Operation: "FILE", Directory: repo.GitDirectory, BaseTree: base, HeadTree: state.HeadTree, Path: path, OldPath: file.OldPath}, &content)
	return content, err
}

func (s *Service) ActOnReview(ctx context.Context, identity auth.Identity, session domain.SessionID, in ReviewAction) (domain.Review, error) {
	r, err := s.ReadReview(ctx, identity, session)
	if err != nil {
		return r, err
	}
	if in.Action == "publish" && in.BatchID != "" {
		for _, t := range r.Threads {
			for _, m := range t.Messages {
				if m.BatchID == in.BatchID {
					return r, s.resumeReviewCorrections(ctx, identity, r)
				}
			}
		}
	}
	if r.Version != in.ExpectedVersion {
		return r, fmt.Errorf("%w: the review changed; reload it before retrying", ErrConflict)
	}
	switch in.Action {
	case "register":
		return s.RegisterReviewRepository(ctx, identity, session, in.Directory, in.Name, "DECLARED")
	case "refresh":
		return s.refreshReview(ctx, identity, r)
	case "require":
		if len(r.Repositories) == 0 {
			return r, fmt.Errorf("%w: track a repository before requiring review", ErrConflict)
		}
		run, e := s.store.LatestRun(ctx, session)
		if e != nil {
			return r, translate(e)
		}
		if _, connected := s.backends.Lookup(run.BackendInstanceID); !connected {
			return r, fmt.Errorf("%w: reconnect the backend before requiring a live review checkpoint", ErrBackendUnavailable)
		}
		backend, e := s.store.BackendInstanceByID(ctx, run.BackendInstanceID)
		if e != nil {
			return r, translate(e)
		}
		if !backend.HasFeature(domain.FeatureHumanReview) {
			return r, fmt.Errorf("%w: update this backend before requiring a review", ErrConflict)
		}
		r, err = s.editReview(ctx, identity, session, in.ExpectedVersion, func(_ *postgres.Store, r *domain.Review) error {
			r.Required = true
			r.Status = "WAITING"
			r.CorrectionJobID = ""
			return nil
		})
		if err != nil {
			return r, err
		}
		if err = s.syncReviewGate(ctx, identity, session); err != nil {
			return r, err
		}
		return s.refreshReview(ctx, identity, r)
	case "approve":
		if len(r.Revisions) == 0 || r.Revisions[len(r.Revisions)-1].ID != in.RevisionID {
			return r, fmt.Errorf("%w: approve the latest captured revision", ErrConflict)
		}
		for _, t := range r.Threads {
			if !t.Resolved {
				return r, fmt.Errorf("%w: resolve the remaining discussions before approving", ErrConflict)
			}
			for _, m := range t.Messages {
				if m.Draft {
					return r, fmt.Errorf("%w: send or discard your drafts before approving", ErrConflict)
				}
			}
		}
		for _, state := range r.Revisions[len(r.Revisions)-1].Repositories {
			if state.Error != "" {
				return r, fmt.Errorf("%w: an unavailable repository cannot be approved", ErrConflict)
			}
		}
		fresh, e := s.captureReview(ctx, r)
		if e != nil {
			return r, e
		}
		if !sameReviewTrees(fresh, r.Revisions[len(r.Revisions)-1]) {
			_, _ = s.refreshReview(ctx, identity, r)
			return r, fmt.Errorf("%w: files changed after this revision; refresh and review the new version", ErrConflict)
		}
		r, err = s.editReview(ctx, identity, session, in.ExpectedVersion, func(_ *postgres.Store, r *domain.Review) error {
			last := &r.Revisions[len(r.Revisions)-1]
			last.Approved = true
			for i := range r.Repositories {
				for _, state := range last.Repositories {
					if state.RepositoryID == r.Repositories[i].ID {
						r.Repositories[i].ApprovedTree = state.HeadTree
					}
				}
			}
			r.Status = "APPROVED"
			r.Required = false
			r.CorrectionJobID = ""
			return nil
		})
		if err == nil {
			if gateErr := s.syncReviewGate(ctx, identity, session); gateErr != nil {
				return r, gateErr
			}
			run, e := s.store.LatestRun(ctx, session)
			if e == nil {
				s.dispatchNext(ctx, run.ID)
			}
		}
		return r, err
	case "publish":
		return s.publishReview(ctx, identity, r, in)
	}
	var excerpt string
	if in.Action == "comment" {
		if in.StartLine < 0 || in.EndLine < 0 || (in.EndLine > 0 && in.StartLine == 0) {
			return r, fmt.Errorf("%w: invalid comment line range", ErrInvalid)
		}
		if strings.TrimSpace(in.Body) == "" || len(in.Body) > 20000 {
			return r, fmt.Errorf("%w: write a comment of up to 20000 bytes", ErrInvalid)
		}
		if slices.IndexFunc(r.Revisions, func(v domain.ReviewRevision) bool { return v.ID == in.RevisionID }) < 0 {
			return r, ErrNotFound
		}
		if in.Path != "" {
			content, e := s.reviewFile(ctx, r, in.RevisionID, in.RepositoryID, in.Path, "")
			if e != nil {
				return r, e
			}
			if in.StartLine > 0 {
				if in.Side != "before" && in.Side != "after" {
					return r, fmt.Errorf("%w: select the before or after side", ErrInvalid)
				}
				value := content.After
				if in.Side == "before" {
					value = content.Before
				}
				lines := strings.Split(value, "\n")
				if in.EndLine == 0 {
					in.EndLine = in.StartLine
				}
				if content.Binary || content.Truncated || in.StartLine < 1 || in.EndLine < in.StartLine || in.EndLine > len(lines) || in.EndLine-in.StartLine > 150 {
					return r, fmt.Errorf("%w: choose up to 151 available text lines", ErrInvalid)
				}
				excerpt = strings.Join(lines[in.StartLine-1:in.EndLine], "\n")
			}
		} else if in.StartLine != 0 || in.EndLine != 0 {
			return r, fmt.Errorf("%w: a line comment needs a file", ErrInvalid)
		}
	}
	return s.editReview(ctx, identity, session, in.ExpectedVersion, func(_ *postgres.Store, r *domain.Review) error {
		thread := slices.IndexFunc(r.Threads, func(t domain.ReviewThread) bool { return t.ID == in.ThreadID })
		switch in.Action {
		case "comment":
			message := domain.ReviewMessage{ID: domain.NewUUID(), Body: strings.TrimSpace(in.Body), Author: "user", Draft: true, CreatedAt: s.now()}
			if in.ThreadID != "" {
				if thread < 0 {
					return ErrNotFound
				}
				r.Threads[thread].Messages = append(r.Threads[thread].Messages, message)
				r.Threads[thread].Resolved = false
			} else {
				r.Threads = append(r.Threads, domain.ReviewThread{ID: domain.NewUUID(), RevisionID: in.RevisionID, RepositoryID: in.RepositoryID, Path: in.Path, Side: in.Side, StartLine: in.StartLine, EndLine: in.EndLine, Excerpt: excerpt, Messages: []domain.ReviewMessage{message}})
			}
		case "edit", "delete":
			if thread < 0 {
				return ErrNotFound
			}
			messages := r.Threads[thread].Messages
			idx := slices.IndexFunc(messages, func(m domain.ReviewMessage) bool { return m.ID == in.MessageID })
			if idx < 0 {
				return ErrNotFound
			}
			if !messages[idx].Draft || messages[idx].Author != "user" {
				return fmt.Errorf("%w: only your drafts can be edited", ErrConflict)
			}
			if in.Action == "delete" {
				r.Threads[thread].Messages = slices.Delete(messages, idx, idx+1)
				if len(r.Threads[thread].Messages) == 0 {
					r.Threads = slices.Delete(r.Threads, thread, thread+1)
				}
			} else {
				if strings.TrimSpace(in.Body) == "" || len(in.Body) > 20000 {
					return ErrInvalid
				}
				r.Threads[thread].Messages[idx].Body = strings.TrimSpace(in.Body)
			}
		case "resolve":
			if thread < 0 {
				return ErrNotFound
			}
			r.Threads[thread].Resolved = in.Resolved
		case "seen":
			revision := slices.IndexFunc(r.Revisions, func(v domain.ReviewRevision) bool { return v.ID == in.RevisionID })
			if revision < 0 {
				return ErrNotFound
			}
			found := false
			for _, state := range r.Revisions[revision].Repositories {
				if state.RepositoryID == in.RepositoryID {
					for _, f := range state.Files {
						if f.Path == in.Path {
							r.Seen[in.RepositoryID+":"+in.Path] = state.HeadTree
							found = true
						}
					}
				}
			}
			if !found {
				return ErrNotFound
			}
		default:
			return fmt.Errorf("%w: unknown review action", ErrInvalid)
		}
		return nil
	})
}

func (s *Service) publishReview(ctx context.Context, identity auth.Identity, r domain.Review, in ReviewAction) (domain.Review, error) {
	if _, err := uuid.Parse(in.BatchID); err != nil {
		return r, fmt.Errorf("%w: publication needs a batch UUID", ErrInvalid)
	}
	var job domain.Job
	jobEvents := &batch{ownerID: identity.UserID}
	result, err := s.editReview(ctx, identity, r.SessionID, in.ExpectedVersion, func(tx *postgres.Store, r *domain.Review) error {
		if r.Status == "CORRECTING" {
			return fmt.Errorf("%w: wait for the current corrections before sending another batch", ErrConflict)
		}
		if len(r.Revisions) == 0 || r.Revisions[len(r.Revisions)-1].ID != in.RevisionID {
			return fmt.Errorf("%w: send comments for the latest captured revision", ErrConflict)
		}
		var prompt strings.Builder
		fmt.Fprintf(&prompt, "Address this human code review (revision %s). Comments refer to immutable code snapshots; inspect the current files before editing. Make the requested corrections, report their outcome using review_reply, and stop for another human review. Do not resolve discussions or approve the review on the user's behalf.\n", in.RevisionID)
		count := 0
		for i := range r.Threads {
			t := &r.Threads[i]
			for j := range t.Messages {
				m := &t.Messages[j]
				if !m.Draft {
					continue
				}
				count++
				m.Draft = false
				m.BatchID = in.BatchID
				fmt.Fprintf(&prompt, "\nThread %s — repository %s, %s (%s lines %d–%d), revision %s\nCode context:\n%s\nHuman comment:\n%s\n", t.ID, t.RepositoryID, t.Path, t.Side, t.StartLine, t.EndLine, t.RevisionID, t.Excerpt, m.Body)
			}
		}
		if count == 0 {
			return fmt.Errorf("%w: add a draft comment before sending corrections", ErrInvalid)
		}
		if prompt.Len() > 512<<10 {
			return fmt.Errorf("%w: this review batch is too large; send fewer comments", ErrInvalid)
		}
		for _, repo := range r.Repositories {
			fmt.Fprintf(&prompt, "\nRepository %s: %s (%s), backend %s\n", repo.ID, repo.Name, repo.Directory, repo.BackendInstanceID)
		}
		run, e := tx.LatestRun(ctx, r.SessionID)
		if e != nil {
			return e
		}
		job = domain.Job{ID: domain.NewJobID(), RunID: run.ID, Status: domain.JobQueued, OriginChannel: domain.ChannelFrom(ctx)}
		if e = tx.CreateJob(ctx, &job); e != nil {
			return e
		}
		r.CorrectionJobID = job.ID
		r.Status = "CORRECTING"
		sess, e := tx.GetSession(ctx, identity.UserID, r.SessionID)
		if e != nil {
			return e
		}
		// Job + input + published comments commit together. A retry never creates a
		// second correction job or sends the user's draft twice.
		scope := domain.Scope{ProjectID: sess.ProjectID, SessionID: r.SessionID, RunID: run.ID, JobID: job.ID}
		if e = s.appendAll(ctx, tx, jobEvents, record{events.TypeJobCreated, scope, JobCreatedPayload{RunID: string(run.ID)}}, record{events.TypeUserMessage, scope, UserMessagePayload{Text: prompt.String(), Delivery: DeliveryQueue}}); e != nil {
			return e
		}
		// editReview publishes review.changed after this transaction; Job events
		// are replayable even if this process exits before their fast-path delivery.
		return nil
	})
	if err != nil {
		return result, err
	}
	s.flush(jobEvents)
	return result, s.resumeReviewCorrections(ctx, identity, result)
}

func (s *Service) resumeReviewCorrections(ctx context.Context, identity auth.Identity, r domain.Review) error {
	if r.Status != "CORRECTING" || r.CorrectionJobID == "" {
		return nil
	}
	if err := s.syncReviewGate(ctx, identity, r.SessionID); err != nil {
		return err
	}
	run, err := s.store.LatestRun(ctx, r.SessionID)
	if err != nil {
		return translate(err)
	}
	if active, e := s.store.ActiveJob(ctx, run.ID); e == nil && active.ID != r.CorrectionJobID && (active.Status == domain.JobWaitingInput || active.Status == domain.JobWaitingValidation) {
		if _, err = s.CancelJob(ctx, identity, active.ID, "The human review starts a dedicated correction turn at this waiting point."); err != nil {
			return err
		}
	} else if e != nil && !errors.Is(e, postgres.ErrNotFound) {
		return translate(e)
	}
	s.DispatchJob(ctx, r.CorrectionJobID)
	return nil
}

func (s *Service) finishReviewCorrection(ctx context.Context, tx *postgres.Store, b *batch, jc postgres.JobContext) error {
	r, err := tx.ReadReview(ctx, jc.OwnerID, jc.SessionID)
	if err != nil {
		return err
	}
	if r.CorrectionJobID != jc.Job.ID {
		return nil
	}
	r.CorrectionJobID = ""
	if r.Required {
		r.Status = "WAITING"
	} else {
		r.Status = "OPEN"
	}
	r.UpdatedAt = s.now()
	if err = tx.SaveReview(ctx, &r); err != nil {
		return err
	}
	return s.appendAll(ctx, tx, b, record{events.TypeReviewChanged, domain.Scope{ProjectID: jc.ProjectID, SessionID: jc.SessionID}, map[string]any{"version": r.Version, "status": r.Status, "required": r.Required}})
}

func (s *Service) syncReviewGate(ctx context.Context, identity auth.Identity, session domain.SessionID) error {
	review, err := s.ReadReview(ctx, identity, session)
	if err != nil {
		return err
	}
	run, err := s.store.LatestRun(ctx, session)
	if err != nil {
		return translate(err)
	}
	var response struct{ Acknowledged bool }
	err = s.reviewRead(ctx, run.BackendInstanceID, &backendv1.ReviewWorkspaceRequest{Operation: "GATE", RunId: string(run.ID), ReviewRequired: review.Required || review.Status == "CORRECTING", CorrectionJobId: string(review.CorrectionJobID), ReviewVersion: uint64(review.Version)}, &response)
	if err != nil {
		return fmt.Errorf("%w: review state was saved, but the backend has not acknowledged its action checkpoint: %v", ErrBackendUnavailable, err)
	}
	if !response.Acknowledged {
		return fmt.Errorf("%w: backend did not acknowledge the review checkpoint", ErrConflict)
	}
	return nil
}

func (s *Service) waitReviewCheckpoint(ctx context.Context, owner domain.UserID, session domain.SessionID, job domain.JobID) error {
	// Subscribe before the read to avoid missing an approval between them. The
	// database is rechecked periodically for cross-process changes and restarts.
	sub, stop := s.broker.Subscribe(owner, "", domain.Client{}, &events.Presence{Active: false})
	defer stop()
	timer := time.NewTicker(15 * time.Second)
	defer timer.Stop()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for {
		allowed, err := s.store.ReviewAllowsJob(ctx, session, job)
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}
		current, err := s.store.JobByID(ctx, job)
		if err != nil {
			return err
		}
		if current.Status.Terminal() || current.Status == domain.JobCancelling {
			return fmt.Errorf("%w: the review paused job ended", ErrConflict)
		}
		review, err := s.store.ReadReview(ctx, owner, session)
		if err != nil {
			return err
		}
		if review.Status == "CORRECTING" && review.CorrectionJobID != job {
			if _, err = s.CancelJob(ctx, auth.Identity{UserID: owner}, job, "Human review corrections continue in a dedicated turn after this action boundary."); err != nil {
				return err
			}
			return fmt.Errorf("%w: a dedicated human-review correction turn replaces this paused action", ErrConflict)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%w: human review is still pending; wait for approval in Threavia before attempting further actions", ErrConflict)
		case <-sub.Events():
		case <-timer.C:
		}
	}
}
