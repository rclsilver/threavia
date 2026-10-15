package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

func backendAuditState(t *testing.T, c *core) string {
	t.Helper()
	var state string
	if err := c.db.Pool().QueryRow(context.Background(), `SELECT jsonb_build_object(
	 'backends',(SELECT jsonb_agg(to_jsonb(b) ORDER BY id) FROM backend_instances b),
	 'tokens',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM backend_registration_tokens t),
	 'conditions',(SELECT jsonb_agg(to_jsonb(c) ORDER BY backend_instance_id,type) FROM backend_conditions c),
	 'audit',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM audit_entries a),
	 'events',(SELECT jsonb_agg(to_jsonb(e) ORDER BY global_sequence) FROM events e))::text`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestBackendAuditRegistrationClaimRevokeAndPrivacy(t *testing.T) {
	c := newCore(t)
	ctx := context.Background()
	web, closeWeb := c.openStream(t, "")
	defer closeWeb()
	editor, closeEditor := c.openStream(t, "")
	defer closeEditor()
	id, credential := c.registerBackend("Historical backend")
	registered := awaitFrame(t, web, "backend.registered")
	if awaitFrame(t, editor, "backend.registered").id != registered.id {
		t.Fatal("clients disagree about registration")
	}
	c.asUser("other").mustDo("POST", "/api/v1/backends/"+id+"/revoke", nil, nil, 404)
	c.mustDo("POST", "/api/v1/backends/"+id+"/revoke", nil, nil, 204, [2]string{"X-Threavia-Channel", "web"})
	removed := awaitFrame(t, web, "backend.revoked")
	if awaitFrame(t, editor, "backend.revoked").id != removed.id {
		t.Fatal("clients disagree about revocation")
	}
	c.mustDo("POST", "/api/v1/backends/"+id+"/revoke", nil, nil, 404)
	if _, err := c.svc.Resolve(ctx, credential); err == nil {
		t.Fatal("revoked credential still works")
	}
	replay, closeReplay := c.openStream(t, fmt.Sprint(registered.event.Sequence))
	defer closeReplay()
	if awaitFrame(t, replay, "backend.revoked").id != removed.id {
		t.Fatal("revocation cannot replay")
	}
	entries := lifecycleAudit(t, c, "backend.")
	if len(entries) != 2 || entries[0].Action != "backend.revoked" || entries[0].ActorID != "thomas" || entries[0].Channel != "web" || entries[1].ActorID != "backend:"+id {
		t.Fatal(entries)
	}
	for _, entry := range entries {
		if entry.SubjectID != id || entry.ProjectID != "" || !strings.Contains(string(entry.Detail), "Historical backend") || strings.Contains(string(entry.Detail), credential) {
			t.Fatal("lost name, secret or wrong scope", entry)
		}
	}
	other, err := c.store.ListAudit(ctx, "other", "", 100)
	if err != nil || len(other) != 0 {
		t.Fatal("audit leaked", other, err)
	}
	c.svc.SetRegistration(service.RegistrationConfig{SharedKey: "private-shared-key"})
	shared, err := c.svc.RegisterBackend(ctx, service.RegisterBackendInput{Name: "Claim me", SharedKey: "private-shared-key", Capabilities: []domain.Capability{domain.CapabilityCode}})
	if err != nil {
		t.Fatal(err)
	}
	if len(lifecycleAudit(t, c, "backend.")) != 2 {
		t.Fatal("unowned registration was attributed to a user")
	}
	c.mustDo("POST", "/api/v1/backends/claim", map[string]any{"claimCode": shared.ClaimCode}, nil, 200, [2]string{"X-Threavia-Channel", "vscode"})
	c.mustDo("POST", "/api/v1/backends/claim", map[string]any{"claimCode": shared.ClaimCode}, nil, 404)
	entries = lifecycleAudit(t, c, "backend.")
	if len(entries) != 4 || entries[0].Action != "backend.claimed" || entries[0].Channel != "vscode" || entries[1].Action != "backend.registered" {
		t.Fatal(entries)
	}
	var detail map[string]any
	_ = json.Unmarshal(entries[1].Detail, &detail)
	if detail["registeredAt"] == nil || detail["registrationFlow"] != "shared_key" {
		t.Fatal("original registration metadata missing", detail)
	}
	for _, entry := range entries {
		for _, secret := range []string{shared.ClaimCode, shared.Credential, "private-shared-key"} {
			if strings.Contains(string(entry.Detail), secret) {
				t.Fatal("secret in audit")
			}
		}
	}
}

func TestBackendLifecycleHistoryFailureRollsBack(t *testing.T) {
	for _, table := range []string{"audit_entries", "events"} {
		for _, action := range []string{"registered", "claimed", "revoked"} {
			t.Run(table+"/"+action, func(t *testing.T) {
				c := newCore(t)
				ctx, who := context.Background(), auth.Identity{UserID: "thomas"}
				var attempt func() error
				switch action {
				case "registered":
					token, _, err := c.svc.CreateRegistrationToken(ctx, who, "Retry me", time.Hour)
					if err != nil {
						t.Fatal(err)
					}
					attempt = func() error {
						_, err := c.svc.RegisterBackend(ctx, service.RegisterBackendInput{Name: "Retry registration", Token: token, Capabilities: []domain.Capability{domain.CapabilityCode}})
						return err
					}
				case "claimed":
					c.svc.SetRegistration(service.RegistrationConfig{SharedKey: "shared"})
					result, err := c.svc.RegisterBackend(ctx, service.RegisterBackendInput{Name: "Retry claim", SharedKey: "shared", Capabilities: []domain.Capability{domain.CapabilityCode}})
					if err != nil {
						t.Fatal(err)
					}
					attempt = func() error { _, err := c.svc.ClaimBackend(ctx, who, result.ClaimCode); return err }
				case "revoked":
					id, _ := c.registerBackend("Retry revoke")
					attempt = func() error { return c.svc.RevokeBackendInstance(ctx, who, domain.BackendInstanceID(id)) }
				}
				column, rejected := "action", "backend."+action
				if table == "events" {
					column = "type"
					if action == "claimed" {
						rejected = "backend.registered"
					}
				}
				before := backendAuditState(t, c)
				lifecycleSQL(t, c, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT reject_backend_history CHECK (%s <> '%s') NOT VALID", table, column, rejected))
				subscription, unsubscribe := c.svc.Broker().Subscribe("thomas", domain.ChannelWeb, domain.Client{}, nil)
				defer unsubscribe()
				if err := attempt(); err == nil {
					t.Fatal("failed history was accepted")
				}
				if after := backendAuditState(t, c); after != before {
					t.Fatal("failed history changed state")
				}
				noLifecyclePublication(t, subscription, "backend.")
				lifecycleSQL(t, c, fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT reject_backend_history", table))
				if err := attempt(); err != nil {
					t.Fatal("retry failed", err)
				}
			})
		}
	}
}

func TestBackendAvailabilityAuditIsQuietAndTransactional(t *testing.T) {
	c := newCore(t)
	id, _ := c.registerBackend("Availability")
	ctx, instanceID := context.Background(), domain.BackendInstanceID(id)
	if err := c.svc.Connected(ctx, instanceID, "lease", &backendv1.Hello{}); err != nil {
		t.Fatal(err)
	}
	report := &backendv1.StatusUpdate{Status: backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY, ProviderAuthState: backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATED}
	c.svc.StatusUpdate(ctx, instanceID, report)
	c.svc.StatusUpdate(ctx, instanceID, report)
	c.svc.Heartbeat(ctx, instanceID, time.Now(), &backendv1.Capacity{})
	report.Conditions = []*backendv1.Condition{{Type: "diagnostic", Message: "private provider output"}}
	c.svc.StatusUpdate(ctx, instanceID, report)
	if len(lifecycleAudit(t, c, "backend.availability_changed")) != 1 {
		t.Fatal("routine reports added audit noise")
	}
	lifecycleSQL(t, c, `ALTER TABLE audit_entries ADD CONSTRAINT reject_availability CHECK (action <> 'backend.availability_changed') NOT VALID`)
	before := backendAuditState(t, c)
	report.Status = backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED
	c.svc.StatusUpdate(ctx, instanceID, report)
	if backendAuditState(t, c) != before {
		t.Fatal("failed availability audit changed state")
	}
	c.svc.Disconnected(ctx, instanceID, "lease")
	if backendAuditState(t, c) != before {
		t.Fatal("failed disconnect audit changed state")
	}
	lifecycleSQL(t, c, `ALTER TABLE audit_entries DROP CONSTRAINT reject_availability`)
	c.svc.StatusUpdate(ctx, instanceID, report)
	report.Status = backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY
	c.svc.StatusUpdate(ctx, instanceID, report)
	c.svc.Disconnected(ctx, instanceID, "old-lease")
	c.svc.Disconnected(ctx, instanceID, "lease")
	c.svc.Disconnected(ctx, instanceID, "lease")
	entries := lifecycleAudit(t, c, "backend.availability_changed")
	if len(entries) != 4 {
		t.Fatal(entries)
	}
	for _, entry := range entries {
		if strings.Contains(string(entry.Detail), "private") || strings.Contains(string(entry.Detail), "lease") {
			t.Fatal("diagnostics leaked into audit")
		}
	}
}

func TestBackendConcurrentClaimsAndRevocationsAuditOnce(t *testing.T) {
	c := newCore(t)
	c.svc.SetRegistration(service.RegistrationConfig{SharedKey: "shared"})
	ctx := context.Background()
	result, err := c.svc.RegisterBackend(ctx, service.RegisterBackendInput{Name: "Race", SharedKey: "shared", Capabilities: []domain.Capability{domain.CapabilityCode}})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { _, _ = c.svc.ClaimBackend(ctx, auth.Identity{UserID: "thomas"}, result.ClaimCode) })
	}
	wg.Wait()
	for range 2 {
		wg.Go(func() {
			_ = c.svc.RevokeBackendInstance(ctx, auth.Identity{UserID: "thomas"}, result.BackendInstanceID)
		})
	}
	wg.Wait()
	if got := len(lifecycleAudit(t, c, "backend.")); got != 3 {
		t.Fatalf("%d traces, want registration, claim and revoke", got)
	}
}
