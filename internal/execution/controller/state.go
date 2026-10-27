package controller

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/rclsilver/threavia/internal/execution/kube"
)

type Record struct {
	Profile   string       `json:"profile"`
	Request   kube.Request `json:"request"`
	Digest    string       `json:"digest"`
	BootID    string       `json:"bootId"`
	PodName   string       `json:"podName"`
	PodUID    string       `json:"podUid,omitempty"`
	State     string       `json:"state"`
	Error     string       `json:"error,omitempty"`
	StartedAt time.Time    `json:"startedAt"`
	UpdatedAt time.Time    `json:"updatedAt"`
	// ConfirmedExit is durable before Pod deletion. Absence of a Pod alone must
	// never be mistaken for proof that an unreachable node stopped its process.
	ConfirmedExit bool `json:"confirmedExit"`
	Cleaned       bool `json:"cleaned,omitempty"`
	// Admitted is fsynced before joining the streams. A worker cannot receive
	// its command until then, which makes cancellation of pending pods safe.
	Admitted   bool   `json:"admitted"`
	FinalState string `json:"finalState,omitempty"`
}
type lease struct {
	BootID    string    `json:"bootId"`
	ExpiresAt time.Time `json:"expiresAt"`
	Retired   []string  `json:"retired"`
}
type state struct {
	Records map[string]*Record `json:"records"`
	Leases  map[string]*lease  `json:"leases"`
}
type store struct {
	path string
	data state
}

func openStore(path string) (*store, error) {
	s := &store{path: path, data: state{Records: map[string]*Record{}, Leases: map[string]*lease{}}}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &s.data); err != nil {
			return nil, err
		}
	}
	if s.data.Records == nil {
		s.data.Records = map[string]*Record{}
	}
	if s.data.Leases == nil {
		s.data.Leases = map[string]*lease{}
	}
	if err = s.save(); err != nil {
		return nil, err
	}
	return s, nil
}

// save is called while the owning controller state mutex is held (or before
// publication). Directory fsync closes the atomic-rename durability window.
func (s *store) save() error {
	raw, err := json.Marshal(s.data)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".execution-state-*")
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
	if err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func terminal(state string) bool {
	switch state {
	case "COMPLETED", "FAILED", "STOPPED", "EXPIRED", "INTERRUPTED":
		return true
	}
	return false
}
func recordKey(profile, id string) string { return profile + "/" + id }
