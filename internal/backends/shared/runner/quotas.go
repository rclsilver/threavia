package runner

import (
	"context"

	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

// QuotaReader can collect a complete snapshot while the backend is idle.
type QuotaReader interface {
	Quotas(context.Context) quotas.Status
}

// QuotaSource describes a provider that reports quotas only while running.
type QuotaSource interface{ InitialQuotas() quotas.Status }

// QuotaReporter accepts both complete polls and partial provider notifications.
type QuotaReporter interface {
	ReportQuotas(context.Context, quotas.Status, bool) error
}

func ReportQuotas(ctx context.Context, sink Sink, snapshot quotas.Status, complete bool) error {
	if reporter, ok := sink.(QuotaReporter); ok {
		return reporter.ReportQuotas(ctx, snapshot, complete)
	}
	return nil
}
