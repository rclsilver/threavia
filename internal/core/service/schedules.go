package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/schedule"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// scheduleTick is how often Core looks for a Schedule that came due. A
// message planned for 08:00 goes out within this of 08:00.
const scheduleTick = 20 * time.Second

// scheduleGrace is how late a run may still go out. Past it, the time was
// missed — Core was down — and the run is skipped rather than sent late: a
// "good morning, check X" at four in the afternoon is not what was asked.
const scheduleGrace = 2 * time.Minute

// upcomingShown is how many future runs a Schedule lists.
const upcomingShown = 3

// ScheduleInput is what a person writes a Schedule with.
type ScheduleInput struct {
	Cron     *string `json:"cron"`
	Timezone *string `json:"timezone"`
	Message  *string `json:"message"`
	Enabled  *bool   `json:"enabled"`
}

// CreateSchedule adds a Schedule to a Session.
func (s *Service) CreateSchedule(ctx context.Context, identity auth.Identity, sessionID domain.SessionID, in ScheduleInput) (domain.Schedule, error) {
	if _, err := s.store.GetSession(ctx, identity.UserID, sessionID); err != nil {
		return domain.Schedule{}, translate(err)
	}
	created := domain.Schedule{
		ID:        domain.ScheduleID(domain.NewUUID()),
		SessionID: sessionID,
		Enabled:   true,
		Timezone:  "Europe/Paris",
	}
	if err := applyScheduleInput(&created, in); err != nil {
		return domain.Schedule{}, err
	}
	if err := s.planNext(&created, s.now()); err != nil {
		return domain.Schedule{}, err
	}
	if err := s.store.CreateSchedule(ctx, &created); err != nil {
		return domain.Schedule{}, translate(err)
	}
	return s.withUpcoming(created), nil
}

// ListSchedules returns the Schedules of a Session.
func (s *Service) ListSchedules(ctx context.Context, identity auth.Identity, sessionID domain.SessionID) ([]domain.Schedule, error) {
	if _, err := s.store.GetSession(ctx, identity.UserID, sessionID); err != nil {
		return nil, translate(err)
	}
	schedules, err := s.store.ListSchedules(ctx, identity.UserID, sessionID)
	if err != nil {
		return nil, translate(err)
	}
	for i := range schedules {
		schedules[i] = s.withUpcoming(schedules[i])
	}
	return schedules, nil
}

// UpdateSchedule changes a Schedule, pausing or resuming it included. Its
// next run is worked out again from now: an edited time applies from the
// next occurrence, never to one already past.
func (s *Service) UpdateSchedule(ctx context.Context, identity auth.Identity, id domain.ScheduleID, in ScheduleInput) (domain.Schedule, error) {
	current, err := s.store.GetSchedule(ctx, identity.UserID, id)
	if err != nil {
		return domain.Schedule{}, translate(err)
	}
	if err := applyScheduleInput(&current, in); err != nil {
		return domain.Schedule{}, err
	}
	if err := s.planNext(&current, s.now()); err != nil {
		return domain.Schedule{}, err
	}
	if err := s.store.UpdateSchedule(ctx, &current); err != nil {
		return domain.Schedule{}, translate(err)
	}
	return s.withUpcoming(current), nil
}

// DeleteSchedule removes a Schedule. What it already sent stays in the
// Session.
func (s *Service) DeleteSchedule(ctx context.Context, identity auth.Identity, id domain.ScheduleID) error {
	return translate(s.store.DeleteSchedule(ctx, identity.UserID, id))
}

// RunScheduleNow sends a Schedule's message at once, as a person trying it
// out. It is an explicit act, so it queues behind a running Job like any
// message rather than being skipped, and it leaves the next planned run alone.
func (s *Service) RunScheduleNow(ctx context.Context, identity auth.Identity, id domain.ScheduleID) (domain.Job, error) {
	current, err := s.store.GetSchedule(ctx, identity.UserID, id)
	if err != nil {
		return domain.Job{}, translate(err)
	}
	job, err := s.queueMessage(domain.WithChannel(ctx, domain.ChannelSchedule), identity,
		current.SessionID, current.Message, "", current.ID)
	if err != nil {
		return domain.Job{}, err
	}
	if err := s.store.RecordScheduleRun(ctx, current.ID, s.now(), domain.ScheduleSent, &job.ID); err != nil {
		s.logger.Warn("cannot record a schedule run", slog.String("error", err.Error()))
	}
	return job, nil
}

// RunSchedules sends what came due, until the context ends.
func (s *Service) RunSchedules(ctx context.Context) {
	ticker := time.NewTicker(scheduleTick)
	defer ticker.Stop()
	for {
		s.RunDueSchedules(ctx, s.now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunDueSchedules sends, or skips, every run due at the given instant. The
// loop calls it with the clock; a test calls it with the instant it needs.
func (s *Service) RunDueSchedules(ctx context.Context, now time.Time) {
	now = now.UTC()
	due, err := s.store.DueSchedules(ctx, now, 100)
	if err != nil {
		s.logger.Error("cannot read the due schedules", slog.String("error", err.Error()))
		return
	}
	for _, sc := range due {
		s.fire(ctx, sc, now)
	}
}

// fire takes one due run of a Schedule and either sends it or says why not.
func (s *Service) fire(ctx context.Context, sc domain.Schedule, now time.Time) {
	if sc.NextRunAt == nil {
		return
	}
	dueAt := *sc.NextRunAt

	// The next run is planned from now, not from the one missed: a Core down
	// for a day owes one skipped run, not every one of them.
	next := sc
	if err := s.planNext(&next, now); err != nil {
		next.NextRunAt = nil
	}
	claimed, err := s.store.ClaimScheduleRun(ctx, sc.ID, dueAt, next.NextRunAt)
	if err != nil {
		s.logger.Error("cannot claim a schedule run", slog.String("scheduleId", string(sc.ID)), slog.String("error", err.Error()))
		return
	}
	if !claimed {
		return
	}

	logger := s.logger.With(slog.String("scheduleId", string(sc.ID)), slog.String("sessionId", string(sc.SessionID)))
	skip := func(outcome domain.ScheduleOutcome, reason string) {
		logger.Info("scheduled message skipped", slog.String("outcome", string(outcome)), slog.String("reason", reason))
		if err := s.store.RecordScheduleRun(ctx, sc.ID, now, outcome, nil); err != nil {
			logger.Warn("cannot record a schedule run", slog.String("error", err.Error()))
		}
		s.emit(ctx, sc.OwnerID, events.TypeScheduleSkipped,
			domain.Scope{ProjectID: sc.ProjectID, SessionID: sc.SessionID},
			ScheduleSkippedPayload{ScheduleID: sc.ID, Outcome: outcome, Reason: reason, Due: dueAt.Format(time.RFC3339)})
	}

	if now.Sub(dueAt) > scheduleGrace {
		skip(domain.ScheduleSkippedMissed, "Core was not running when it came due")
		return
	}
	run, err := s.store.LatestRun(ctx, sc.SessionID)
	if err != nil {
		skip(domain.ScheduleFailed, "the session has no backend to run it on")
		return
	}
	if _, online := s.backends.Lookup(run.BackendInstanceID); !online {
		skip(domain.ScheduleSkippedMissed, "the backend of this session was offline")
		return
	}
	if busy, err := s.sessionBusy(ctx, run.ID); err != nil || busy {
		skip(domain.ScheduleSkippedBusy, "the previous job was still running")
		return
	}

	identity := auth.Identity{UserID: sc.OwnerID}
	job, err := s.queueMessage(domain.WithChannel(ctx, domain.ChannelSchedule), identity,
		sc.SessionID, sc.Message, "", sc.ID)
	if err != nil {
		skip(domain.ScheduleFailed, err.Error())
		return
	}
	logger.Info("scheduled message sent", slog.String("jobId", string(job.ID)))
	if err := s.store.RecordScheduleRun(ctx, sc.ID, now, domain.ScheduleSent, &job.ID); err != nil {
		logger.Warn("cannot record a schedule run", slog.String("error", err.Error()))
	}
}

// sessionBusy reports whether a Run still has a Job going or waiting.
func (s *Service) sessionBusy(ctx context.Context, runID domain.RunID) (bool, error) {
	if _, err := s.store.ActiveJob(ctx, runID); err == nil {
		return true, nil
	} else if !errors.Is(err, postgres.ErrNotFound) {
		return false, err
	}
	if _, err := s.store.NextQueuedJob(ctx, runID); err == nil {
		return true, nil
	} else if !errors.Is(err, postgres.ErrNotFound) {
		return false, err
	}
	return false, nil
}

func applyScheduleInput(sc *domain.Schedule, in ScheduleInput) error {
	if in.Cron != nil {
		sc.Cron = strings.Join(strings.Fields(*in.Cron), " ")
	}
	if in.Timezone != nil {
		sc.Timezone = strings.TrimSpace(*in.Timezone)
	}
	if in.Message != nil {
		sc.Message = strings.TrimSpace(*in.Message)
	}
	if in.Enabled != nil {
		sc.Enabled = *in.Enabled
	}
	if sc.Message == "" {
		return fmt.Errorf("%w: a schedule needs a message to send", ErrInvalid)
	}
	if _, err := schedule.Parse(sc.Cron); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	if _, err := time.LoadLocation(sc.Timezone); err != nil || sc.Timezone == "" {
		return fmt.Errorf("%w: unknown time zone %q", ErrInvalid, sc.Timezone)
	}
	return nil
}

// planNext sets when an enabled Schedule fires next after the given instant.
func (s *Service) planNext(sc *domain.Schedule, after time.Time) error {
	if !sc.Enabled {
		sc.NextRunAt = nil
		return nil
	}
	spec, err := schedule.Parse(sc.Cron)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	location, err := time.LoadLocation(sc.Timezone)
	if err != nil {
		return fmt.Errorf("%w: unknown time zone %q", ErrInvalid, sc.Timezone)
	}
	next, err := spec.Next(after, location)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	next = next.UTC()
	sc.NextRunAt = &next
	return nil
}

// withUpcoming fills the next few runs, for a person to read in dates.
func (s *Service) withUpcoming(sc domain.Schedule) domain.Schedule {
	sc.Upcoming = []time.Time{}
	if !sc.Enabled || sc.NextRunAt == nil {
		return sc
	}
	spec, err := schedule.Parse(sc.Cron)
	if err != nil {
		return sc
	}
	location, err := time.LoadLocation(sc.Timezone)
	if err != nil {
		return sc
	}
	at := *sc.NextRunAt
	sc.Upcoming = append(sc.Upcoming, at)
	for len(sc.Upcoming) < upcomingShown {
		next, err := spec.Next(at, location)
		if err != nil {
			break
		}
		sc.Upcoming = append(sc.Upcoming, next.UTC())
		at = next
	}
	return sc
}
