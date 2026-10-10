package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

func TestBackendQuotasReachClientsAndRemainOwnerScoped(t *testing.T) {
	c := newCore(t)
	id, credential := c.registerBackend("quota-backend")
	backend := c.connectBackend(credential)
	zero, threshold := 0.0, 0.93
	report := quotas.Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC(), Limits: []quotas.Limit{
		{ID: "primary", Label: "Five hours", PercentUsed: &zero, Thresholds: []quotas.Threshold{{Label: "Custom threshold", Value: &threshold}}},
		{ID: "model-week", Label: "Model week", Status: "restricted"},
	}}
	if err := backend.sdk.SendQuotas(context.Background(), report); err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []domain.BackendInstance `json:"items"`
	}
	waitUntil(t, "the quota snapshot to reach the HTTP API", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/backends", nil, &list, http.StatusOK)
		return len(list.Items) == 1 && list.Items[0].Quotas != nil
	})
	found := list.Items[0]
	if string(found.ID) != id || found.Backend != "fake" || len(found.Quotas.Limits) != 2 || found.Quotas.Limits[0].PercentUsed == nil || *found.Quotas.Limits[0].PercentUsed != 0 || found.Quotas.Limits[1].PercentUsed != nil {
		t.Fatalf("invalid HTTP quota snapshot: %+v", found)
	}
	waitUntil(t, "the quota notification to persist", func() bool {
		replay, err := c.store.EventsAfter(context.Background(), "thomas", 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, event := range replay {
			if event.Type == events.TypeBackendQuotasUpdated && event.ProjectID == nil {
				return true
			}
		}
		return false
	})
	stranger := c.asUser("stranger")
	stranger.mustDo(http.MethodGet, "/api/v1/backends", nil, &list, http.StatusOK)
	if len(list.Items) != 0 {
		t.Fatal("another user can see the backend quotas")
	}
	replay, err := c.store.EventsAfter(context.Background(), "stranger", 0, 100)
	if err != nil || len(replay) != 0 {
		t.Fatalf("quota events leaked to another owner: %v (%v)", replay, err)
	}
}
