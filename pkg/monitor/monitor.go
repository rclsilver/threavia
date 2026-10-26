// Package monitor defines provider-independent background watches.
package monitor

import "time"

const MaxOutput = 8192

type Snapshot struct {
	ID          string    `json:"id"`
	RunID       string    `json:"runId"`
	JobID       string    `json:"jobId"`
	Description string    `json:"description"`
	Command     string    `json:"command"`
	State       string    `json:"state"`
	Sequence    uint64    `json:"sequence"`
	Output      string    `json:"output"`
	Error       string    `json:"error,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
	Notify      bool      `json:"notify"`
}

func Active(state string) bool {
	return state == "STARTING" || state == "RUNNING" || state == "STOPPING"
}

type View struct {
	AutomaticResumesRemaining int `json:"automaticResumesRemaining"`
	Snapshot
	SessionID  string    `json:"sessionId"`
	BackendID  string    `json:"backendId"`
	Confirmed  bool      `json:"confirmed"`
	LastSeenAt time.Time `json:"lastSeenAt"`
}
