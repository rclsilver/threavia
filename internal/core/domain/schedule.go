package domain

import "time"

// ScheduleID identifies a Schedule.
type ScheduleID string

// ScheduleOutcome is what became of the last time a Schedule came due.
type ScheduleOutcome string

const (
	// ScheduleSent: the message was posted and became a Job.
	ScheduleSent ScheduleOutcome = "SENT"
	// ScheduleSkippedBusy: the previous Job of the Session was still going,
	// and a scheduled message does not pile up behind it.
	ScheduleSkippedBusy ScheduleOutcome = "SKIPPED_BUSY"
	// ScheduleSkippedMissed: the time passed while nobody could send it —
	// Core was down, or the backend away. It is not sent late.
	ScheduleSkippedMissed ScheduleOutcome = "SKIPPED_MISSED"
	// ScheduleFailed: posting the message failed.
	ScheduleFailed ScheduleOutcome = "FAILED"
)

// Schedule sends a message to a Session at the times a cron expression names,
// in the time zone it was written in.
type Schedule struct {
	ID        ScheduleID `json:"id"`
	SessionID SessionID  `json:"sessionId"`
	// ProjectID and OwnerID come from the Session, for scoping what a run
	// records; they are not stored on the Schedule.
	ProjectID ProjectID `json:"-"`
	OwnerID   UserID    `json:"-"`
	Cron      string    `json:"cron"`
	Timezone  string    `json:"timezone"`
	Message   string    `json:"message"`
	Enabled   bool      `json:"enabled"`
	// NextRunAt is absent when the Schedule is paused or names no real date.
	NextRunAt   *time.Time      `json:"nextRunAt,omitempty"`
	LastRunAt   *time.Time      `json:"lastRunAt,omitempty"`
	LastOutcome ScheduleOutcome `json:"lastOutcome,omitempty"`
	LastJobID   *JobID          `json:"lastJobId,omitempty"`
	// Upcoming are the next few times it fires, so a person sees in plain
	// dates what the expression they typed means.
	Upcoming  []time.Time `json:"upcoming"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
}
