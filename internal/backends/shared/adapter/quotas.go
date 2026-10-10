package adapter

import (
	"context"
	"sort"
	"time"

	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

func (a *Adapter) startQuotaReporting(ctx context.Context) {
	a.mu.Lock()
	if a.quotaCancel != nil {
		a.quotaCancel()
	}
	ctx, a.quotaCancel = context.WithCancel(ctx)
	a.mu.Unlock()
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			if reader, ok := a.runner.(runner.QuotaReader); ok {
				readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				snapshot := reader.Quotas(readCtx)
				cancel()
				if ctx.Err() == nil {
					_ = a.ReportQuotas(ctx, snapshot, true)
				}
			} else {
				snapshot := quotas.Status{Availability: "UNSUPPORTED", ObservedAt: time.Now().UTC(), Message: "This provider does not report quotas."}
				if source, ok := a.runner.(runner.QuotaSource); ok {
					snapshot = source.InitialQuotas()
				}
				_ = a.ReportQuotas(ctx, snapshot, true)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (a *Adapter) ReportQuotas(ctx context.Context, snapshot quotas.Status, complete bool) error {
	if _, err := snapshot.Struct(); err != nil {
		return err
	}
	a.quotaMu.Lock()
	defer a.quotaMu.Unlock()
	if previous := a.quotaSnapshot; previous != nil {
		if snapshot.Availability == "UNAVAILABLE" {
			// Keep the last observation visibly stale instead of inventing zero usage.
			snapshot.Limits, snapshot.Details, snapshot.ObservedAt = previous.Limits, previous.Details, previous.ObservedAt
		} else if !complete {
			// Partial notifications can omit plan/credit metadata or other
			// families. Preserve them just as we preserve their quota windows.
			snapshot.Details = mergeQuotaDetails(previous.Details, snapshot.Details)
			limits := make(map[string]quotas.Limit)
			for _, limit := range previous.Limits {
				limits[limit.ID] = limit
			}
			for _, limit := range snapshot.Limits {
				limits[limit.ID] = limit
			}
			snapshot.Limits = nil
			for _, limit := range limits {
				snapshot.Limits = append(snapshot.Limits, limit)
			}
			sort.Slice(snapshot.Limits, func(i, j int) bool { return snapshot.Limits[i].ID < snapshot.Limits[j].ID })
		}
	}
	// Merging separately bounded notifications can exceed the report bound.
	// Do not replace the last valid snapshot with one the SDK cannot send.
	if _, err := snapshot.Struct(); err != nil {
		return err
	}
	a.quotaSnapshot = &snapshot
	if sdk := a.sdk(); sdk != nil {
		return sdk.SendQuotas(ctx, snapshot)
	}
	return nil
}

func mergeQuotaDetails(previous, update map[string]any) map[string]any {
	merged := make(map[string]any, len(previous)+len(update))
	for key, value := range previous {
		merged[key] = value
	}
	for key, value := range update {
		oldMap, oldOK := merged[key].(map[string]any)
		newMap, newOK := value.(map[string]any)
		if oldOK && newOK {
			value = mergeQuotaDetails(oldMap, newMap)
		}
		merged[key] = value
	}
	return merged
}
