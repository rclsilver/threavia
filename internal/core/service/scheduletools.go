package service

import (
	"context"
	"errors"
	"fmt"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/tools"
)

func (s *Service) scheduleTool(ctx context.Context, identity auth.Identity, jc jobScope, name tools.Name, input map[string]any) (map[string]any, error) {
	for field := range input {
		allowed := (name == tools.NameScheduleCreate || name == tools.NameScheduleUpdate) && (field == "cron" || field == "timezone" || field == "message" || field == "enabled" || field == "quiet") || (name == tools.NameScheduleUpdate || name == tools.NameScheduleDelete) && field == "scheduleId"
		if !allowed {
			return nil, fmt.Errorf("unsupported field %s; schedules are scoped to this session: %w", field, ErrInvalid)
		}
	}
	ctx = context.WithValue(domain.WithChannel(ctx, domain.Channel("agent")), managerJobKey{}, jc.JobID)
	if name == tools.NameScheduleDismiss {
		if err := s.store.DismissJob(ctx, jc.JobID); errors.Is(err, postgres.ErrNotFound) {
			return nil, fmt.Errorf("%w: only a run of a quiet schedule can be dismissed", ErrConflict)
		} else if err != nil {
			return nil, translate(err)
		}
		return map[string]any{"dismissed": true}, nil
	}
	if name == tools.NameScheduleList {
		list, err := s.ListSchedules(ctx, identity, jc.SessionID)
		if err != nil {
			return nil, err
		}
		if list == nil {
			list = []domain.Schedule{}
		}
		return sessionToolResult(map[string]any{"schedules": list})
	}
	var in ScheduleInput
	for field, target := range map[string]**string{"cron": &in.Cron, "timezone": &in.Timezone, "message": &in.Message} {
		if value, present := input[field]; present {
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string: %w", field, ErrInvalid)
			}
			*target = &text
		}
	}
	for field, target := range map[string]**bool{"enabled": &in.Enabled, "quiet": &in.Quiet} {
		if value, present := input[field]; present {
			flag, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("%s must be a boolean: %w", field, ErrInvalid)
			}
			*target = &flag
		}
	}
	if name == tools.NameScheduleCreate {
		created, err := s.CreateSchedule(ctx, identity, jc.SessionID, in)
		if err != nil {
			return nil, err
		}
		return sessionToolResult(map[string]any{"scheduleId": created.ID, "schedule": created})
	}
	id, ok := input["scheduleId"].(string)
	if !ok || !isUUID(id) {
		return nil, fmt.Errorf("scheduleId must be a UUID: %w", ErrInvalid)
	}
	current, err := s.store.GetSchedule(ctx, identity.UserID, domain.ScheduleID(id))
	if err != nil {
		return nil, translate(err)
	}
	if current.SessionID != jc.SessionID {
		return nil, ErrNotFound
	}
	if name == tools.NameScheduleDelete {
		if err := s.DeleteSchedule(ctx, identity, current.ID); err != nil {
			return nil, err
		}
		return map[string]any{"scheduleId": id, "deleted": true}, nil
	}
	updated, err := s.UpdateSchedule(ctx, identity, current.ID, in)
	if err != nil {
		return nil, err
	}
	return sessionToolResult(map[string]any{"scheduleId": updated.ID, "schedule": updated})
}

// dismissedEnd says whether an event is the successful end of a Job its agent
// dismissed: the one ending that tells nobody. A failure still does.
func dismissedEnd(job domain.Job, event *backendv1.JobEvent) bool {
	_, completed := event.GetBody().(*backendv1.JobEvent_JobCompleted)
	return completed && job.Dismissed
}
