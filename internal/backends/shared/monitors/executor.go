package monitors

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/rclsilver/threavia/pkg/monitor"
)

// Executor supplies a runtime that owns the full process tree. Wait may report
// completion only after that runtime has confirmed termination.
type Process interface{ Wait() error }
type Executor interface {
	Available() error
	Start(context.Context, monitor.Snapshot, string, bool, bool, io.Writer, io.Writer) (Process, error)
}

var ErrStopUnconfirmed = errors.New("monitor execution stop is unconfirmed")

func NewWithExecutor(path string, executor Executor) *Manager {
	m := New(path, false)
	m.executor = executor
	return m
}
func (m *Manager) Available() bool {
	if m.executor != nil {
		return m.executor.Available() == nil
	}
	return m.unsandboxed || Probe() == nil
}
func (m *Manager) startRemote(s monitor.Snapshot, dir string, writable, network bool) error {
	m.mu.Lock()
	if m.err != nil {
		m.mu.Unlock()
		return m.err
	}
	if m.entries[s.ID] != nil {
		m.mu.Unlock()
		return nil
	}
	active := 0
	for _, e := range m.entries {
		if monitor.Active(e.Snapshot.State) {
			active++
		}
	}
	if active >= 20 || len(m.entries) >= 1000 {
		m.mu.Unlock()
		return errors.New("monitor capacity reached")
	}
	ctx, cancel := context.WithDeadline(context.Background(), s.ExpiresAt)
	s.State = "STARTING"
	s.Sequence = 1
	s.UpdatedAt = time.Now().UTC()
	s.Notify = false
	e := &entry{Snapshot: s, stop: cancel, lastPublished: time.Now()}
	m.entries[s.ID] = e
	if err := m.persist(); err != nil {
		delete(m.entries, s.ID)
		cancel()
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	// Pod scheduling and attachment may wait. Output, Stop and reporting must
	// remain responsive while the executor is acquiring this process.
	process, err := m.executor.Start(ctx, s, dir, writable, network, &outputWriter{m: m, e: e}, &outputWriter{m: m, e: e, stderr: true})
	m.mu.Lock()
	if err != nil {
		priorState := e.Snapshot.State
		cancel()
		e.Snapshot.State = "FAILED"
		e.Snapshot.Error = err.Error()
		e.Snapshot.Notify = true
		if priorState == "STOPPING" {
			e.Snapshot.State = "STOPPED"
			e.Snapshot.Notify = false
			e.Snapshot.Error = ""
		} else if ctx.Err() == context.DeadlineExceeded {
			e.Snapshot.State = "EXPIRED"
		}
		e.Snapshot.Sequence++
		e.Snapshot.UpdatedAt = time.Now().UTC()
		_ = m.persist()
		m.mu.Unlock()
		return err
	}
	e.process = process
	if e.Snapshot.State == "STARTING" {
		e.Snapshot.State = "RUNNING"
		e.Snapshot.Sequence++
		e.Snapshot.UpdatedAt = time.Now().UTC()
	}
	if err = m.persist(); err != nil {
		m.err = err
		cancel()
	}
	m.mu.Unlock()
	go m.wait(ctx, e)
	return err
}
