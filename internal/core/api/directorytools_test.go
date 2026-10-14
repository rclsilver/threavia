package api_test

import (
	"context"
	"testing"
)

func TestKnownDirectoryToolsManageMetadataAndBindings(t *testing.T) {
	c, backend, projectID, dirID := setup(t)
	c.startSession(projectID, c.backendID, dirID, "Inspect")
	start := receive(t, "job", backend.starts)
	ctx := context.Background()
	call := func(name string, input map[string]any) map[string]any {
		return backend.callCoreTool(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}
	fail := func(name string, input map[string]any) {
		backend.callCoreToolExpectingFailure(t, ctx, start.GetRunId(), start.GetJobId(), name, input)
	}
	fail("known_directory_register", map[string]any{"name": "invalid", "path": "relative"})
	fail("known_directory_register", map[string]any{"name": "invalid-option", "path": "/work/invalid-option", "useForSession": "true"})
	listed := call("known_directory_list", map[string]any{})
	if len(listed["directories"].([]any)) != 1 {
		t.Fatalf("failed registration left metadata: %v", listed)
	}
	result := call("known_directory_register", map[string]any{"name": "docs", "path": "/work/docs", "description": "Documentation", "gitRemote": "https://example.com/docs.git"})
	id := result["knownDirectoryId"].(string)
	call("known_directory_update", map[string]any{"knownDirectoryId": id, "name": "manual", "description": "", "gitRemote": ""})
	listed = call("known_directory_list", map[string]any{})
	var entry map[string]any
	for _, raw := range listed["directories"].([]any) {
		item := raw.(map[string]any)
		if item["knownDirectoryId"] == id {
			entry = item
		}
	}
	if entry == nil || entry["name"] != "manual" || entry["description"] != "" || entry["gitRemote"] != "" || len(entry["bindings"].([]any)) != 1 {
		t.Fatalf("updated directory: %v", entry)
	}
	call("known_directory_bind", map[string]any{"knownDirectoryId": id, "path": "/moved/docs"})
	call("known_directory_unbind", map[string]any{"knownDirectoryId": id})
	call("known_directory_unbind", map[string]any{"knownDirectoryId": id})
	fail("known_directory_delete", map[string]any{"knownDirectoryId": dirID})
	call("known_directory_delete", map[string]any{"knownDirectoryId": id})
	if got := call("known_directory_list", map[string]any{}); len(got["directories"].([]any)) != 1 {
		t.Fatalf("directory was not deleted: %v", got)
	}
	var other idOnly
	c.mustDo("POST", "/api/v1/projects", map[string]any{"name": "Other"}, &other, 201)
	var foreign idOnly
	c.mustDo("POST", "/api/v1/projects/"+other.ID+"/directories", map[string]any{"name": "foreign"}, &foreign, 201)
	for _, tool := range []string{"known_directory_bind", "known_directory_update", "known_directory_unbind", "known_directory_delete"} {
		fail(tool, map[string]any{"knownDirectoryId": foreign.ID, "path": "/foreign", "name": "stolen"})
	}
}
