package api_test

import (
	"context"
	"encoding/json"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
	"net/http"
	"testing"
)

func TestSessionModelsPersistIndependentlyAndApplyToTheNextJob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newCore(t)
	project := c.createProject("models")
	backendID, credential := c.registerBackend("models")
	catalog := &backendv1.ModelCatalog{DefaultModel: "alpha", DefaultEffort: "medium", Models: []*backendv1.ModelOption{
		{Id: "alpha", Name: "Alpha", Efforts: []string{"low", "medium"}, DefaultEffort: "medium"},
		{Id: "beta", Name: "Beta", Efforts: []string{"low", "high", "max"}, DefaultEffort: "high"},
	}}
	backend := c.connectBackendWithReconcileGate(credential, nil, catalog)
	// Connected is observed in the SDK before Core finishes persisting Hello.
	// Explicit model preferences require the advertised catalog to be stored.
	waitUntil(t, "the advertised model catalog", func() bool {
		instance, err := c.store.GetBackendInstance(ctx, domain.UserID("thomas"), domain.BackendInstanceID(backendID))
		return err == nil && instance.ModelCatalog != nil && len(instance.ModelCatalog.Models) == len(catalog.Models)
	})
	start := func(model, effort string) service.StartSessionResult {
		var result service.StartSessionResult
		c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{"projectId": project, "backendInstanceId": backendID, "message": "start", "model": model, "effort": effort}, &result, http.StatusCreated)
		return result
	}
	first := start("alpha", "low")
	firstCmd := receive(t, "first launch", backend.starts)
	other := start("beta", "max")
	otherCmd := receive(t, "other launch", backend.starts)
	if firstCmd.GetModel() != "alpha" || firstCmd.GetEffort() != "low" || otherCmd.GetModel() != "beta" || otherCmd.GetEffort() != "max" {
		t.Fatal("session parameters crossed between launches")
	}

	// Provider confirmation is retained independently from the session alias.
	started := backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobStarted(ctx, firstCmd.GetRunId(), firstCmd.GetJobId())
	})
	started.GetJobStarted().Model = "alpha-v1"
	started.GetJobStarted().Effort = "low"
	backend.emit(t, ctx, started)
	waitUntil(t, "provider model to persist", func() bool {
		job, err := c.store.JobByID(ctx, first.Job.ID)
		return err == nil && job.Model == "alpha-v1"
	})
	path := "/api/v1/sessions/" + string(first.Session.ID)
	c.mustDo(http.MethodPatch, path, map[string]any{"model": "beta", "effort": "high"}, nil, http.StatusOK)
	var snapshot service.Snapshot
	c.mustDo(http.MethodGet, path, nil, &snapshot, http.StatusOK)
	if snapshot.Session.Model != "beta" || snapshot.Session.Effort != "high" || snapshot.Jobs[0].Model != "alpha-v1" || snapshot.Jobs[0].Effort != "low" {
		t.Fatalf("running job changed with session settings: %+v", snapshot)
	}
	var unchanged service.Snapshot
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+string(other.Session.ID), nil, &unchanged, http.StatusOK)
	if unchanged.Session.Model != "beta" || unchanged.Session.Effort != "max" {
		t.Fatal("changing one session affected another")
	}

	// Reject an incompatible effort atomically.
	c.mustDo(http.MethodPatch, path, map[string]any{"model": "alpha", "effort": "max"}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodGet, path, nil, &snapshot, http.StatusOK)
	if snapshot.Session.Model != "beta" || snapshot.Session.Effort != "high" {
		t.Fatal("invalid configuration changed stored settings")
	}
	alice := c.asUser("alice")
	alice.mustDo(http.MethodPatch, path, map[string]any{"model": "beta", "effort": "low"}, nil, http.StatusNotFound)

	// A queued message gets the configuration in force when it actually launches.
	c.mustDo(http.MethodPost, path+"/messages", map[string]any{"message": "next"}, nil, http.StatusCreated)
	expectNothing(t, "another launch while first is running", backend.starts)
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, firstCmd.GetRunId(), firstCmd.GetJobId(), "done", nil)
	}))
	next := receive(t, "next launch", backend.starts)
	if next.GetModel() != "beta" || next.GetEffort() != "high" {
		t.Fatalf("next job got %q/%q", next.GetModel(), next.GetEffort())
	}
	c.mustDo(http.MethodGet, path, nil, &snapshot, http.StatusOK)
	found := false
	for _, event := range snapshot.Events {
		if event.Type == "job.completed" && event.JobID != nil && *event.JobID == first.Job.ID {
			var payload struct{ Model, Effort string }
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Model != "alpha-v1" || payload.Effort != "low" {
				t.Fatalf("history lost original configuration: %+v", payload)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("completion event absent")
	}

	// Clear preferences to use advertised backend defaults again.
	c.mustDo(http.MethodPatch, path, map[string]any{"model": "", "effort": ""}, nil, http.StatusOK)
	c.mustDo(http.MethodPost, path+"/messages", map[string]any{"message": "defaults"}, nil, http.StatusCreated)
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, next.GetRunId(), next.GetJobId(), "done", nil)
	}))
	defaults := receive(t, "default launch", backend.starts)
	if defaults.GetModel() != "alpha" || defaults.GetEffort() != "medium" {
		t.Fatal("reset did not restore backend defaults")
	}
	c.mustDo(http.MethodGet, path, nil, &snapshot, http.StatusOK)
	stored, storeErr := c.store.GetSession(ctx, domain.UserID("thomas"), first.Session.ID)
	if storeErr != nil {
		t.Fatal(storeErr)
	}
	if stored.Model != "" || stored.Effort != "" {
		t.Fatal("reset preferences not persisted")
	}
	audit, err := c.store.ListAudit(ctx, domain.UserID("thomas"), domain.ProjectID(project), 50)
	if err != nil {
		t.Fatal(err)
	}
	changes := 0
	for _, entry := range audit {
		if entry.Action == "session.model_changed" {
			changes++
		}
	}
	if changes != 2 {
		t.Fatalf("got %d configuration audit entries, want 2", changes)
	}
}

func TestLegacyBackendsRefuseExplicitModelPreferences(t *testing.T) {
	t.Parallel()
	c := newCore(t)
	project := c.createProject("legacy")
	backendID, credential := c.registerBackend("legacy")
	backend := c.connectBackend(credential)
	session := c.startSession(project, backendID, "", "default")
	receive(t, "default launch", backend.starts)
	c.mustDo(http.MethodPatch, "/api/v1/sessions/"+session, map[string]any{"model": "beta"}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{"projectId": project, "backendInstanceId": backendID, "message": "explicit", "model": "beta"}, nil, http.StatusBadRequest)
}
