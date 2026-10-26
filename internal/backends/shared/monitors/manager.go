// Package monitors supervises watches independently of provider Job processes.
package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rclsilver/threavia/pkg/monitor"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type entry struct {
	Snapshot      monitor.Snapshot
	cmd           *exec.Cmd
	stop          context.CancelFunc
	dirty         bool
	lastPublished time.Time
}
type Manager struct {
	mu      sync.Mutex
	path    string
	entries map[string]*entry
	err     error
	launch  func(context.Context, string, string, bool, bool) (*exec.Cmd, error)
}

// New recovers observations, never PIDs or commands with unknown side effects.
func New(path string) *Manager {
	m := &Manager{path: path, entries: make(map[string]*entry), launch: sandboxCommand}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		m.err = err
		return m
	}
	var values []monitor.Snapshot
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &values); err != nil {
			m.err = err
			return m
		}
	}
	for _, s := range values {
		if monitor.Active(s.State) {
			s.State = "INTERRUPTED"
			s.Error = "The backend restarted; the command was not restarted."
			s.Sequence++
			s.UpdatedAt = time.Now().UTC()
			s.Notify = true
		}
		m.entries[s.ID] = &entry{Snapshot: s}
	}
	return m
}
func (m *Manager) persist() error {
	if m.err != nil {
		return m.err
	}
	values := make([]monitor.Snapshot, 0, len(m.entries))
	for _, e := range m.entries {
		values = append(values, e.Snapshot)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(m.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(m.path), ".monitors-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), m.path)
	}
	return err
}

// Start is called only after policy approval; the launcher adds OS isolation.
func (m *Manager) Start(s monitor.Snapshot, dir string, writable, network bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if m.entries[s.ID] != nil {
		return nil
	}
	active := 0
	for _, e := range m.entries {
		if monitor.Active(e.Snapshot.State) {
			active++
		}
	}
	if active >= 20 || len(m.entries) >= 1000 {
		return fmt.Errorf("monitor capacity reached")
	}
	ctx, cancel := context.WithDeadline(context.Background(), s.ExpiresAt)
	cmd, err := m.launch(ctx, dir, s.Command, writable, network)
	if err != nil {
		cancel()
		return err
	}
	s.State = "RUNNING"
	s.Sequence = 1
	s.UpdatedAt = time.Now().UTC()
	s.Notify = false
	e := &entry{Snapshot: s, cmd: cmd, stop: cancel, lastPublished: time.Now()}
	m.entries[s.ID] = e
	if err = m.persist(); err != nil {
		delete(m.entries, s.ID)
		cancel()
		return err
	}
	cmd.Stdout = &outputWriter{m: m, e: e}
	cmd.Stderr = &outputWriter{m: m, e: e, stderr: true}
	if err = cmd.Start(); err != nil {
		cancel()
		e.Snapshot.State = "FAILED"
		e.Snapshot.Error = err.Error()
		e.Snapshot.Sequence++
		e.Snapshot.Notify = true
		_ = m.persist()
		return err
	}
	go m.wait(ctx, e)
	return nil
}
func (m *Manager) wait(ctx context.Context, e *entry) {
	err := e.cmd.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	defer e.stop()
	s := &e.Snapshot
	switch {
	case s.State == "STOPPING":
		s.State = "STOPPED"
		s.Notify = false
	case ctx.Err() != nil:
		s.State = "EXPIRED"
		s.Notify = true
	case err != nil:
		s.State = "FAILED"
		s.Error = err.Error()
		s.Notify = true
	default:
		s.State = "COMPLETED"
		s.Notify = true
	}
	s.Sequence++
	s.UpdatedAt = time.Now().UTC()
	e.dirty = false
	if err = m.persist(); err != nil {
		m.err = err
	}
}

type outputWriter struct {
	m      *Manager
	e      *entry
	stderr bool
}

func (w *outputWriter) Write(p []byte) (int, error) {
	w.m.mu.Lock()
	defer w.m.mu.Unlock()
	s := &w.e.Snapshot
	s.Output += strings.ToValidUTF8(string(p), "�")
	if len(s.Output) > monitor.MaxOutput {
		s.Output = s.Output[len(s.Output)-monitor.MaxOutput:]
		for !utf8.ValidString(s.Output) {
			s.Output = s.Output[1:]
		}
	}
	if !w.stderr {
		w.e.dirty = true
	}
	s.UpdatedAt = time.Now().UTC()
	return len(p), nil
}

// Snapshots coalesces stdout to at most one wake per 30 seconds.
func (m *Manager) Snapshots() ([]monitor.Snapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	out := make([]monitor.Snapshot, 0, len(m.entries))
	changed := false
	for _, e := range m.entries {
		if e.dirty && time.Since(e.lastPublished) >= 30*time.Second {
			e.Snapshot.Sequence++
			e.Snapshot.Notify = true
			e.dirty = false
			e.lastPublished = time.Now()
			changed = true
		}
		out = append(out, e.Snapshot)
	}
	if changed {
		if err := m.persist(); err != nil {
			m.err = err
			return nil, err
		}
	}
	return out, nil
}

// Ack forgets terminal local observations only once Core durably recorded them.
func (m *Manager) Ack(id, runID string, seq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil || e.Snapshot.RunID != runID || monitor.Active(e.Snapshot.State) || seq < e.Snapshot.Sequence {
		return
	}
	delete(m.entries, id)
	if err := m.persist(); err != nil {
		m.err = err
	}
}
func (m *Manager) Fail(s monitor.Snapshot, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries[s.ID] != nil {
		return
	}
	s.State = "FAILED"
	s.Sequence = 1
	s.Notify = true
	s.Error = err.Error()
	s.UpdatedAt = time.Now().UTC()
	m.entries[s.ID] = &entry{Snapshot: s}
	if err := m.persist(); err != nil {
		m.err = err
	}
}
func (m *Manager) Stop(id, runID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entries[id]
	if e == nil || e.Snapshot.RunID != runID {
		return fmt.Errorf("monitor not found")
	}
	if !monitor.Active(e.Snapshot.State) {
		return nil
	}
	e.Snapshot.State = "STOPPING"
	e.Snapshot.Sequence++
	e.Snapshot.Notify = false
	e.Snapshot.UpdatedAt = time.Now().UTC()
	err := m.persist()
	e.stop() // Stopping must remain possible when local persistence has failed.
	return err
}
