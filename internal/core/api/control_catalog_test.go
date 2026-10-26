package api

import (
	"context"
	"net/http"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestControlCatalogHasValidatedSchemasAndOnlyDocumentedHandlers(t *testing.T) {
	h := &handler{spec: newSpecDocument("test")}
	mux := http.NewServeMux()
	h.registerProjects(mux)
	h.registerBackends(mux)
	h.registerSessions(mux)
	h.registerJobs(mux)
	h.registerKnowledge(mux)
	h.registerPolicy(mux)
	h.registerMCP(mux)
	definitions, err := h.controlCatalog()
	if err != nil {
		t.Fatal(err)
	}
	m := sdk.NewServer(&sdk.Implementation{Name: "catalog-test", Version: "1"}, nil)
	seen := map[string]bool{}
	for _, d := range definitions {
		if seen[d.name] {
			t.Fatalf("duplicate tool %s", d.name)
		}
		seen[d.name] = true
		sdk.AddTool(m, &sdk.Tool{Name: d.name, InputSchema: d.schema}, func(context.Context, *sdk.CallToolRequest, map[string]any) (*sdk.CallToolResult, any, error) {
			return nil, nil, nil
		})
	}
	for _, name := range []string{"instance_info", "project_create", "known_directory_bind", "session_create", "session_read", "session_wait", "session_answer", "session_resolve_validation", "backend_read", "mcp_server_add"} {
		if !seen[name] {
			t.Errorf("missing %s", name)
		}
	}
	for _, name := range []string{"control_access_create", "control_access_revoke", "exec", "http_request"} {
		if seen[name] {
			t.Errorf("unexpected privilege-granting tool %s", name)
		}
	}
}
