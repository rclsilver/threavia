package api_test

import (
	"context"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
	"net/http"
	"testing"
)

func TestMCPRegistryActivationRevisionAndSessionRestrictions(t *testing.T) {
	c := newCore(t)
	project := c.createProject("MCP fixture")
	v := mcpconfig.Server{Name: "docs", Transport: "http", URL: "https://example.test/mcp", Tools: []string{"read"}, ReadTools: []string{"read"}, Enabled: true}
	c.mustDo(http.MethodPost, "/api/v1/me/mcp", v, &v, http.StatusCreated)
	if v.ID == "" || v.Revision != 1 {
		t.Fatalf("created=%+v", v)
	}
	var list struct {
		Items []mcpconfig.Server `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/me/mcp", nil, &list, http.StatusOK)
	if len(list.Items) != 1 {
		t.Fatal("registry missing declaration")
	}
	stale := v
	v.Tools = append(v.Tools, "write")
	c.mustDo(http.MethodPut, "/api/v1/mcp/"+v.ID, v, &v, http.StatusOK)
	c.mustDo(http.MethodPut, "/api/v1/mcp/"+stale.ID, stale, nil, http.StatusConflict)
	b := mcpconfig.Binding{ServerID: v.ID, Enabled: true, Required: true}
	c.mustDo(http.MethodPut, "/api/v1/projects/"+project+"/mcp", b, nil, http.StatusNoContent)
	var bindings struct {
		Items []mcpconfig.Binding `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/mcp", nil, &bindings, http.StatusOK)
	if len(bindings.Items) != 1 || !bindings.Items[0].Required {
		t.Fatal("binding was not persisted")
	}
	// Ownership checks cover declarations and their project activation.
	other := auth.Identity{UserID: domain.UserID("someone-else")}
	if _, err := c.svc.SaveMCP(context.Background(), other, v, false); err == nil {
		t.Fatal("another owner edited the declaration")
	}
	if err := c.svc.BindMCP(context.Background(), other, domain.ProjectID(project), b); err == nil {
		t.Fatal("another owner bound the server")
	}
	c.mustDo(http.MethodDelete, "/api/v1/mcp/"+v.ID, nil, nil, http.StatusNoContent)
	c.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/mcp", nil, &bindings, http.StatusOK)
	if len(bindings.Items) != 0 {
		t.Fatal("delete left project binding")
	}
}

func TestMCPSelectionIsCapturedForEachJobAndRestrictionsCannotExpandProjectScope(t *testing.T) {
	ctx := context.Background()
	c := newCore(t)
	project := c.createProject("MCP snapshots")
	backendID, credential := c.registerBackend("fixture")
	backend := c.connectBackend(credential)
	waitUntil(t, "backend Hello persisted", func() bool {
		v, e := c.store.GetBackendInstance(ctx, "thomas", domain.BackendInstanceID(backendID))
		return e == nil && v.HasFeature(domain.FeaturePromptAttachments)
	})
	// This fixture only records StartJob. Advertise its ability to receive the new field.
	if _, err := c.db.Pool().Exec(ctx, `UPDATE backend_instances SET features=array_append(features,'EXTERNAL_MCP') WHERE id=$1`, backendID); err != nil {
		t.Fatal(err)
	}
	v := mcpconfig.Server{Name: "docs", Transport: "http", URL: "https://example.test/mcp", Tools: []string{"read"}, ReadTools: []string{"read"}, Enabled: true}
	c.mustDo(http.MethodPost, "/api/v1/me/mcp", v, &v, http.StatusCreated)
	c.mustDo(http.MethodPut, "/api/v1/projects/"+project+"/mcp", mcpconfig.Binding{ServerID: v.ID, Enabled: true, Required: true}, nil, http.StatusNoContent)
	session := c.startSession(project, backendID, "", "first")
	first := receive(t, "first MCP snapshot", backend.starts)
	if len(first.GetProjectContext().GetMcpServers()) != 1 {
		t.Fatal("first Job missing MCP declaration")
	}
	v.Tools = append(v.Tools, "write")
	c.mustDo(http.MethodPut, "/api/v1/mcp/"+v.ID, v, &v, http.StatusOK)
	if got := first.GetProjectContext().GetMcpServers()[0].AsMap()["tools"].([]any); len(got) != 1 {
		t.Fatal("active Job configuration was mutated")
	}
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/mcp", map[string]any{"serverIds": []string{domain.NewUUID()}}, nil, http.StatusBadRequest)
	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/mcp", map[string]any{"serverIds": []string{}}, nil, http.StatusNoContent)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "next"}, nil, http.StatusCreated)
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, first.GetRunId(), first.GetJobId(), "done", nil)
	}))
	next := receive(t, "restricted next Job", backend.starts)
	if len(next.GetProjectContext().GetMcpServers()) != 0 {
		t.Fatal("empty session restriction inherited project servers")
	}
	var saved []byte
	if err := c.db.Pool().QueryRow(ctx, `SELECT mcp_snapshot FROM jobs WHERE id=$1`, first.GetJobId()).Scan(&saved); err != nil || len(saved) == 0 {
		t.Fatalf("snapshot not durable: %v", err)
	}
	c.asUser("someone-else").mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/mcp", nil, nil, http.StatusNotFound)
}
