package state

import (
	"context"
	"sort"
	"sync"

	"google.golang.org/protobuf/proto"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// MemoryStore is an in-memory Store.
//
// It implements the whole contract but loses everything on restart, so it is
// suitable for tests and for a backend that is restarted together with its work.
// The durable implementation of specification section 10 replaces it without
// changing any caller.
type MemoryStore struct {
	mu        sync.Mutex
	identity  *Identity
	runs      map[string]RunRecord
	jobs      map[string]JobRecord
	sequences map[string]uint64
	pending   map[string][]*backendv1.JobEvent
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		runs:      make(map[string]RunRecord),
		jobs:      make(map[string]JobRecord),
		sequences: make(map[string]uint64),
		pending:   make(map[string][]*backendv1.JobEvent),
	}
}

// LoadIdentity implements Store.
func (s *MemoryStore) LoadIdentity(context.Context) (Identity, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.identity == nil {
		return Identity{}, false, nil
	}
	return *s.identity, true, nil
}

// SaveIdentity implements Store.
func (s *MemoryStore) SaveIdentity(_ context.Context, identity Identity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	copied := identity
	s.identity = &copied
	return nil
}

// SaveRun implements Store.
func (s *MemoryStore) SaveRun(_ context.Context, run RunRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.RunID] = run
	return nil
}

// Runs implements Store.
func (s *MemoryStore) Runs(context.Context) ([]RunRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RunRecord, 0, len(s.runs))
	for _, run := range s.runs {
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RunID < out[j].RunID })
	return out, nil
}

// SaveJob implements Store.
func (s *MemoryStore) SaveJob(_ context.Context, job JobRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.JobID] = job
	return nil
}

// Jobs implements Store.
func (s *MemoryStore) Jobs(context.Context) ([]JobRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JobRecord, 0, len(s.jobs))
	for _, job := range s.jobs {
		out = append(out, job)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JobID < out[j].JobID })
	return out, nil
}

// NextSequence implements Store.
func (s *MemoryStore) NextSequence(_ context.Context, jobID string) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequences[jobID]++
	return s.sequences[jobID], nil
}

// AppendPending implements Store.
func (s *MemoryStore) AppendPending(_ context.Context, event *backendv1.JobEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobID := event.GetJobId()
	s.pending[jobID] = append(s.pending[jobID], proto.Clone(event).(*backendv1.JobEvent))
	return nil
}

// PendingEvents implements Store.
func (s *MemoryStore) PendingEvents(context.Context) ([]*backendv1.JobEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	jobIDs := make([]string, 0, len(s.pending))
	for jobID := range s.pending {
		jobIDs = append(jobIDs, jobID)
	}
	sort.Strings(jobIDs)

	var out []*backendv1.JobEvent
	for _, jobID := range jobIDs {
		events := append([]*backendv1.JobEvent(nil), s.pending[jobID]...)
		sort.Slice(events, func(i, j int) bool {
			return events[i].GetBackendSequence() < events[j].GetBackendSequence()
		})
		out = append(out, events...)
	}
	return out, nil
}

// Ack implements Store.
func (s *MemoryStore) Ack(_ context.Context, jobID string, sequence uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	kept := s.pending[jobID][:0]
	for _, event := range s.pending[jobID] {
		if event.GetBackendSequence() > sequence {
			kept = append(kept, event)
		}
	}
	if len(kept) == 0 {
		delete(s.pending, jobID)
	} else {
		s.pending[jobID] = kept
	}

	if job, ok := s.jobs[jobID]; ok && sequence > job.AckedSequence {
		job.AckedSequence = sequence
		s.jobs[jobID] = job
	}
	return nil
}

// Close implements Store.
func (s *MemoryStore) Close() error { return nil }
