package service

import (
	"context"
	"fmt"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

const (
	defaultUsageLimit = 100
	maxUsageLimit     = 1000
	maxUsageDays      = 366
)

// SessionUsage ranks what the caller's Sessions consumed between two UTC days,
// both included. Without a period it covers the last seven days.
func (s *Service) SessionUsage(ctx context.Context, identity auth.Identity, projectID domain.ProjectID, from, to string, limit int) (time.Time, time.Time, []domain.UsageRow, error) {
	end := s.now().UTC().Truncate(24 * time.Hour)
	if to != "" {
		parsed, err := time.Parse(time.DateOnly, to)
		if err != nil {
			return time.Time{}, time.Time{}, nil, fmt.Errorf("%w: to must be a YYYY-MM-DD day", ErrInvalid)
		}
		end = parsed
	}
	start := end.AddDate(0, 0, -6)
	if from != "" {
		parsed, err := time.Parse(time.DateOnly, from)
		if err != nil {
			return time.Time{}, time.Time{}, nil, fmt.Errorf("%w: from must be a YYYY-MM-DD day", ErrInvalid)
		}
		start = parsed
	}
	if start.After(end) {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("%w: from is after to", ErrInvalid)
	}
	if end.Sub(start) >= maxUsageDays*24*time.Hour {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("%w: a period covers at most %d days", ErrInvalid, maxUsageDays)
	}
	if limit <= 0 {
		limit = defaultUsageLimit
	}
	limit = min(limit, maxUsageLimit)
	rows, err := s.store.SessionUsage(ctx, identity.UserID, projectID, start, end, limit)
	if err != nil {
		return time.Time{}, time.Time{}, nil, translate(err)
	}
	if rows == nil {
		rows = []domain.UsageRow{}
	}
	return start, end, rows, nil
}
