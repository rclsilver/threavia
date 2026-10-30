package api_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/skills"
	sdktools "github.com/rclsilver/threavia/pkg/backend-sdk/tools"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

func invokeSkill(backend *fakeBackend, start *backendv1.StartJob, name string, input map[string]any, file string) (map[string]any, error) {
	encoded, _ := structpb.NewStruct(input)
	result, err := backend.sdk.Invoke(context.Background(), sdktools.Call{
		RunID: start.GetRunId(), JobID: start.GetJobId(), Name: name, Input: encoded, File: []byte(file),
	})
	if err != nil {
		return nil, err
	}
	return result.AsMap(), nil
}

func skillErrorCode(err error, code codes.Code) bool {
	return err != nil && strings.Contains(err.Error(), "core tool error "+code.String()+":")
}

func TestAgentSkillsLifecycle(t *testing.T) {
	c, backend, project, dir := setup(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))
	c.startSession(project, c.backendID, dir, "Manage a custom skill")
	start := receive(t, "job", backend.starts)
	call := func(name string, input map[string]any, file string) map[string]any {
		t.Helper()
		result, err := invokeSkill(backend, start, name, input, file)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return result
	}
	archive := skillArchive(t, "custom")
	input := map[string]any{"path": "custom.tar.gz", "filename": "custom.tar.gz"}
	installed := call("skill_import", input, archive)["skill"].(map[string]any)
	id, revision := installed["id"], installed["installedRevision"]
	if installed["projectId"] != project || objects.count() != 1 {
		t.Fatalf("import = %v, objects = %d", installed, objects.count())
	}
	var declared bool
	for _, spec := range start.GetProjectContext().GetTools() {
		if spec.GetName() == "skill_import" {
			declared = spec.GetFileInput() == "path" && spec.GetRequiresValidation()
		}
	}
	if !declared {
		t.Fatal("archive transfer and validation must be declared to existing backends")
	}
	read := call("skill_read", map[string]any{"skillId": id}, "")
	if !strings.Contains(read["file"].(map[string]any)["text"].(string), "Do the thing") || len(read["files"].([]any)) != 2 {
		t.Fatalf("read = %v", read)
	}
	for _, f := range read["files"].([]any) {
		if _, ok := f.(map[string]any)["text"]; ok {
			t.Fatal("inventory must not inline every file")
		}
	}
	call("skill_read", map[string]any{"skillId": id, "file": "reference/api.md"}, "")
	for _, expected := range []any{nil, "stale"} {
		in := map[string]any{"path": "custom.tar.gz", "filename": "custom.tar.gz"}
		if expected != nil {
			in["expectedRevision"] = expected
		}
		if _, err := invokeSkill(backend, start, "skill_import", in, archive); !skillErrorCode(err, codes.FailedPrecondition) {
			t.Fatalf("overwrite without current revision: %v", err)
		}
	}
	if objects.count() != 1 {
		t.Fatal("a rejected install must clean its uploaded bundle")
	}
	// Two deliberate updates race on the same revision: exactly one commits.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, description := range []string{"first", "second"} {
		// Repack through the existing fixture with another name, installed under
		// the explicit existing name, so both candidates have distinct hashes.
		changed := skillArchive(t, description)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := invokeSkill(backend, start, "skill_import", map[string]any{"path": "new.tar.gz", "name": "custom", "expectedRevision": revision}, changed)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else if skillErrorCode(err, codes.FailedPrecondition) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 || objects.count() != 1 {
		t.Fatalf("success=%d conflicts=%d objects=%d", successes, conflicts, objects.count())
	}
	current := call("skill_read", map[string]any{"skillId": id}, "")["skill"].(map[string]any)
	if current["id"] != id || current["installedRevision"] == revision {
		t.Fatal("update must preserve id and change revision")
	}
	call("skill_make_global", map[string]any{"skillId": id}, "")
	call("skill_set_enabled", map[string]any{"skillId": id, "enabled": false}, "")
	listed := call("skill_list", map[string]any{}, "")
	globals := listed["global"].([]any)
	if len(globals) != 1 || globals[0].(map[string]any)["disabledProjectIds"].([]any)[0] != project {
		t.Fatalf("list = %v", listed)
	}
	c.startSession(project, c.backendID, dir, "Disabled skill")
	if got := jobSkills(t, backend); len(got) != 0 {
		t.Fatal(got)
	}
	call("skill_set_enabled", map[string]any{"skillId": id, "enabled": true}, "")
	c.startSession(project, c.backendID, dir, "Enabled skill")
	if got := jobSkills(t, backend); got["custom"] != id {
		t.Fatal(got)
	}
	if _, err := invokeSkill(backend, start, "skill_delete", map[string]any{"skillId": id, "expectedRevision": revision}, ""); !skillErrorCode(err, codes.FailedPrecondition) {
		t.Fatalf("stale delete: %v", err)
	}
	call("skill_delete", map[string]any{"skillId": id, "expectedRevision": current["installedRevision"]}, "")
	if objects.count() != 0 {
		t.Fatal("delete must clean bundle")
	}
	var audit struct {
		Items []map[string]any `json:"items"`
	}
	c.mustDo("GET", "/api/v1/me/audit", nil, &audit, 200)
	var agentInstall bool
	for _, row := range audit.Items {
		if row["action"] == "skill.installed" && row["channel"] == "agent" && row["jobId"] == start.GetJobId() {
			agentInstall = true
		}
	}
	if !agentInstall {
		t.Fatalf("missing agent install audit: %v", audit.Items)
	}
}

func TestAgentSkillsBoundariesAndInputs(t *testing.T) {
	c, backend, project, dir := setup(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))
	c.startSession(project, c.backendID, dir, "Manage skills")
	start := receive(t, "job", backend.starts)
	otherProject := c.createProject("other")
	otherUser := c.asUser("someone-else")
	foreignProject := otherUser.createProject("foreign")
	for _, target := range []struct {
		core    *core
		project string
	}{{c, otherProject}, {otherUser, foreignProject}} {
		var installed skillResponse
		target.core.mustUpload("/api/v1/projects/"+target.project+"/skills?filename=x.tar.gz", "application/gzip", skillArchive(t, "hidden"), &installed)
		for _, name := range []string{"skill_read", "skill_make_global", "skill_set_enabled", "skill_delete"} {
			in := map[string]any{"skillId": installed.ID}
			if name == "skill_set_enabled" {
				in["enabled"] = true
			}
			if name == "skill_delete" {
				in["expectedRevision"] = installed.InstalledRevision
			}
			if _, err := invokeSkill(backend, start, name, in, ""); !skillErrorCode(err, codes.NotFound) {
				t.Fatalf("%s crossed ownership/project boundary: %v", name, err)
			}
		}
	}
	for _, in := range []map[string]any{{"projectId": otherProject}, {"scope": "invalid"}, {"scope": 5}, {"filename": true}, {"expectedRevision": false}} {
		in["path"] = "x.tar.gz"
		if _, err := invokeSkill(backend, start, "skill_import", in, skillArchive(t, "custom")); !skillErrorCode(err, codes.InvalidArgument) {
			t.Fatalf("invalid input %v: %v", in, err)
		}
	}
	for _, content := range []string{"", "not an archive", strings.Repeat("x", 3<<20+1)} {
		if _, err := invokeSkill(backend, start, "skill_import", map[string]any{"path": "x.tar.gz"}, content); !skillErrorCode(err, codes.InvalidArgument) {
			t.Fatalf("invalid archive (%d bytes): %v", len(content), err)
		}
	}
	if _, err := invokeSkill(backend, start, "skill_list", map[string]any{"ownerId": "someone-else"}, ""); !skillErrorCode(err, codes.InvalidArgument) {
		t.Fatal(err)
	}
	listed, err := invokeSkill(backend, start, "skill_list", map[string]any{}, "")
	if err != nil || len(listed["global"].([]any)) != 0 {
		t.Fatalf("list = %v %v", listed, err)
	}
	// The same transport cannot impersonate a different run or backend's job.
	copyStart := proto.Clone(start).(*backendv1.StartJob)
	copyStart.RunId = "incorrect"
	if _, err := invokeSkill(backend, copyStart, "skill_list", map[string]any{}, ""); err == nil {
		t.Fatal("wrong run accepted")
	}
}

func TestAgentGlobalSkillImportAndPromotionConflict(t *testing.T) {
	c, backend, project, dir := setup(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))
	c.startSession(project, c.backendID, dir, "Manage global skills")
	start := receive(t, "job", backend.starts)
	archive := skillArchive(t, "shared")
	global, err := invokeSkill(backend, start, "skill_import", map[string]any{"path": "x.tar.gz", "scope": "global"}, archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := global["skill"].(map[string]any)["projectId"]; exists {
		t.Fatal("global import has a project")
	}
	local, err := invokeSkill(backend, start, "skill_import", map[string]any{"path": "x.tar.gz"}, archive)
	if err != nil {
		t.Fatal(err)
	}
	id := local["skill"].(map[string]any)["id"]
	if _, err := invokeSkill(backend, start, "skill_make_global", map[string]any{"skillId": id}, ""); !skillErrorCode(err, codes.FailedPrecondition) {
		t.Fatalf("global collision: %v", err)
	}
	other := c.asUser("another-owner")
	var foreign skillResponse
	other.mustUpload("/api/v1/me/skills?filename=x.tar.gz", "application/gzip", archive, &foreign)
	if _, err := invokeSkill(backend, start, "skill_read", map[string]any{"skillId": foreign.ID}, ""); !skillErrorCode(err, codes.NotFound) {
		t.Fatalf("foreign global: %v", err)
	}
	listed, err := invokeSkill(backend, start, "skill_list", map[string]any{}, "")
	if err != nil || len(listed["global"].([]any)) != 1 || len(listed["project"].([]any)) != 1 {
		t.Fatalf("list = %v %v", listed, err)
	}
	// The next project Job receives the project override; a different project
	// receives the global copy, without affecting the active caller's snapshot.
	c.startSession(project, c.backendID, dir, "Project override")
	if got := jobSkills(t, backend); got["shared"] != id {
		t.Fatal(got)
	}
	otherProject := c.createProject("uses-global")
	c.startSession(otherProject, c.backendID, "", "Global skill")
	if got := jobSkills(t, backend); got["shared"] != global["skill"].(map[string]any)["id"] {
		t.Fatal(got)
	}
}
