package api_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/pkg/backend-sdk/quotas"
)

func revokedBackendReports(t *testing.T, c *core, id domain.BackendInstanceID) map[string]func() {
	t.Helper()
	ctx := context.Background()
	snapshot, err := (quotas.Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC(), Limits: []quotas.Limit{{ID: "late-limit", Label: "Late quota"}}}).Struct()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]func(){
		"status": func() {
			c.svc.StatusUpdate(ctx, id, &backendv1.StatusUpdate{
				Status:            backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY,
				ProviderAuthState: backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATED,
				Capacity:          &backendv1.Capacity{MaxConcurrentRuns: 9, ActiveRuns: 7},
				Conditions:        []*backendv1.Condition{{Type: "late-condition", Status: "True"}},
			})
		},
		"heartbeat": func() { c.svc.Heartbeat(ctx, id, time.Now().Add(time.Hour), &backendv1.Capacity{ActiveRuns: 7}) },
		"quotas":    func() { c.svc.QuotaReport(ctx, id, &backendv1.QuotaReport{Snapshot: snapshot}) },
		"skills": func() {
			c.svc.SkillInventory(ctx, id, &backendv1.SkillInventory{Local: []*backendv1.LocalSkill{{Name: "late-skill", Available: true}}})
		},
		"disconnect": func() { c.svc.Disconnected(ctx, id, "old-lease") },
		"connect": func() {
			if err := c.svc.Connected(ctx, id, "late-lease", &backendv1.Hello{InstanceName: "late-name", ModelCatalog: &backendv1.ModelCatalog{DefaultModel: "late-model"}}); err == nil {
				t.Error("accepted a connection authenticated before revocation")
			}
		},
	}
}

func revokedBackendState(t *testing.T, c *core) string {
	t.Helper()
	var skills string
	if err := c.db.Pool().QueryRow(context.Background(), `SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY backend_instance_id,name),'[]')::text FROM backend_skills s`).Scan(&skills); err != nil {
		t.Fatal(err)
	}
	return backendAuditState(t, c) + skills
}

func TestRevokedBackendIgnoresLateReports(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	id, credential := c.registerBackend("Retired backend")
	instanceID := domain.BackendInstanceID(id)
	if err := c.svc.Connected(ctx, instanceID, "old-lease", &backendv1.Hello{}); err != nil {
		t.Fatal(err)
	}
	if err := c.svc.RecordBackendSkills(ctx, instanceID, []domain.BackendSkill{{Name: "historical-skill", Available: true}}); err != nil {
		t.Fatal(err)
	}
	c.mustDo(http.MethodPost, "/api/v1/backends/"+id+"/revoke", nil, nil, http.StatusNoContent)
	before := revokedBackendState(t, c)
	subscription, unsubscribe := c.svc.Broker().Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
	defer unsubscribe()
	for name, report := range revokedBackendReports(t, c, instanceID) {
		t.Run(name, func(t *testing.T) {
			report()
			if revokedBackendState(t, c) != before {
				t.Fatal("late report changed revoked backend state, metadata or history")
			}
		})
	}
	noLifecyclePublication(t, subscription, "backend.")
	if _, err := c.svc.Resolve(ctx, credential); err == nil {
		t.Fatal("revoked credential resolves")
	}
	visible, err := c.store.ListBackendInstances(ctx, "thomas")
	if err != nil || len(visible) != 0 {
		t.Fatal("revoked backend became visible", visible, err)
	}
}

func TestBackendReportWaitingOnRevocationIsIgnored(t *testing.T) {
	for _, name := range []string{"status", "heartbeat", "quotas", "skills", "connect", "disconnect"} {
		t.Run(name, func(t *testing.T) {
			c := newCore(t)
			ctx := context.Background()
			id, _ := c.registerBackend("Revocation race")
			instanceID := domain.BackendInstanceID(id)
			if err := c.svc.Connected(ctx, instanceID, "old-lease", &backendv1.Hello{}); err != nil {
				t.Fatal(err)
			}
			before, err := c.store.BackendInstanceByID(ctx, instanceID)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := c.db.Pool().Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			// Hold the exact row lock revocation takes, then commit REVOKED
			// while the already-received report is waiting on that lock.
			if _, err := tx.Exec(ctx, `UPDATE backend_instances SET ownership_status='REVOKED', operational_status='OFFLINE', credential_sha256=NULL, connection_id=NULL, revoked_at=now() WHERE id=$1`, instanceID); err != nil {
				t.Fatal(err)
			}
			var pid int
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			report := revokedBackendReports(t, c, instanceID)[name]
			done := make(chan struct{})
			go func() { defer close(done); report() }()
			waitUntil(t, "report to block behind revocation", func() bool {
				var blocked bool
				if err := c.db.Pool().QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, pid).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				return blocked
			})
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("report did not finish after revocation")
			}
			backend, err := c.store.BackendInstanceByID(ctx, instanceID)
			if err != nil || backend.OwnershipStatus != domain.BackendRevoked || backend.OperationalStatus != domain.BackendOffline || backend.ConnectionID != nil || backend.Name != "Revocation race" || backend.ModelCatalog != nil || backend.Quotas != nil || backend.Capacity.ActiveRuns != 0 {
				t.Fatal("report survived concurrent revocation", backend, err)
			}
			if len(backendStatusEvents(t, c)) != 1 {
				t.Fatal("late report emitted a lifecycle change")
			}
			if backend.LastHeartbeatAt == nil || before.LastHeartbeatAt == nil || !backend.LastHeartbeatAt.Equal(*before.LastHeartbeatAt) {
				t.Fatal("late report changed the historical heartbeat")
			}
			skills, err := c.store.ListBackendSkills(ctx, instanceID)
			if err != nil || len(skills) != 0 {
				t.Fatal("late inventory survived concurrent revocation", skills, err)
			}
		})
	}
}
