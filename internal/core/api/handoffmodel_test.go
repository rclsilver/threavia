package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/service"
)

// A model chosen for one backend must not keep a session from moving to a
// backend that does not offer it: the session takes the destination's default
// instead, and the preview says so beforehand.
func TestAMoveFallsBackToTheModelsTheDestinationOffers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newCore(t)
	project := c.createProject("handoff-models")
	connect := func(name string, catalog *backendv1.ModelCatalog) (string, *fakeBackend) {
		id, credential := c.registerBackend(name)
		backend := c.connectBackendWithReconcileGate(credential, nil, catalog)
		waitUntil(t, "the catalog of "+name, func() bool {
			instance, err := c.store.GetBackendInstance(ctx, domain.UserID("thomas"), domain.BackendInstanceID(id))
			return err == nil && instance.ModelCatalog != nil && len(instance.ModelCatalog.Models) == len(catalog.Models)
		})
		return id, backend
	}
	sourceID, source := connect("source", &backendv1.ModelCatalog{DefaultModel: "alpha", DefaultEffort: "medium", Models: []*backendv1.ModelOption{
		{Id: "alpha", Name: "Alpha", Efforts: []string{"low", "medium"}, DefaultEffort: "medium"},
		{Id: "beta", Name: "Beta", Efforts: []string{"low", "high", "max"}, DefaultEffort: "high"},
	}})
	sameID, _ := connect("same models", &backendv1.ModelCatalog{DefaultModel: "alpha", DefaultEffort: "medium", Models: []*backendv1.ModelOption{
		{Id: "alpha", Name: "Alpha", Efforts: []string{"low", "medium"}, DefaultEffort: "medium"},
		{Id: "beta", Name: "Beta", Efforts: []string{"low", "high"}, DefaultEffort: "high"},
	}})
	otherID, _ := connect("other models", &backendv1.ModelCatalog{DefaultModel: "gamma", Models: []*backendv1.ModelOption{{Id: "gamma", Name: "Gamma"}}})

	var started service.StartSessionResult
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{"projectId": project, "backendInstanceId": sourceID, "message": "start", "model": "beta", "effort": "max"}, &started, http.StatusCreated)
	start := receive(t, "the source launch", source.starts)
	source.emit(t, ctx, source.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return source.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "done", nil)
	}))
	session := string(started.Session.ID)
	waitUntil(t, "the source job to finish", func() bool { return c.jobStatus(session, start.GetJobId()) == "COMPLETED" })
	path := "/api/v1/sessions/" + session
	stored := func() domain.Session {
		s, err := c.store.GetSession(ctx, domain.UserID("thomas"), started.Session.ID)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	// The model is there but not the effort: the model stays.
	var preview service.HandoffPreview
	c.mustDo(http.MethodGet, path+"/handoff?backendInstanceId="+sameID, nil, &preview, http.StatusOK)
	if !preview.CanMove || preview.Model != "beta" || preview.Effort != "" || !strings.Contains(strings.Join(preview.Warnings, " "), `effort "max"`) {
		t.Fatalf("wrong preview: %+v", preview)
	}
	c.mustDo(http.MethodPatch, path, map[string]any{"backendInstanceId": sameID, "expectedRunId": preview.FromRunID}, nil, http.StatusOK)
	if s := stored(); s.Model != "beta" || s.Effort != "" {
		t.Fatalf("session kept %q/%q, want beta and the default effort", s.Model, s.Effort)
	}

	// Neither is there: both go back to the destination's defaults.
	c.mustDo(http.MethodGet, path+"/handoff?backendInstanceId="+otherID, nil, &preview, http.StatusOK)
	if !preview.CanMove || preview.Model != "" || preview.Effort != "" || !strings.Contains(strings.Join(preview.Warnings, " "), `model "beta"`) {
		t.Fatalf("wrong preview: %+v", preview)
	}
	c.mustDo(http.MethodPatch, path, map[string]any{"backendInstanceId": otherID}, nil, http.StatusOK)
	if s := stored(); s.Model != "" || s.Effort != "" {
		t.Fatalf("session kept %q/%q, want the defaults", s.Model, s.Effort)
	}
	if c.countEvents(session, "session.model_changed") != 2 {
		t.Fatal("each fallback must be told to the clients")
	}
}
