package api_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/core/skills"
	sdkclient "github.com/rclsilver/threavia/pkg/backend-sdk/client"
)

// skillArchive packs a minimal Agent Skills directory.
func skillArchive(t *testing.T, name string) string {
	t.Helper()

	manifest := "---\nname: " + name + "\ndescription: What this skill is for.\n---\n\nDo the thing.\n"
	files := map[string]string{
		"skill/SKILL.md":         manifest,
		"skill/reference/api.md": "details\n",
	}

	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	writer := tar.NewWriter(gz)
	for path, content := range files {
		if err := writer.WriteHeader(&tar.Header{
			Name: path, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("packing the fixture: %v", err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("packing the fixture: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("packing the fixture: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("packing the fixture: %v", err)
	}
	return packed.String()
}

type skillResponse struct {
	ID                string `json:"id"`
	ProjectID         string `json:"projectId"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	InstalledRevision string `json:"installedRevision"`
	ArtifactID        string `json:"artifactId"`
	BundleSHA256      string `json:"bundleSha256"`
	Source            struct {
		Type string `json:"type"`
		URL  string `json:"url"`
	} `json:"source"`
}

// TestSkillUploadRoundTrip walks the life of a Core-managed Project Skill:
// upload, listing, reinstall, removal.
func TestSkillUploadRoundTrip(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))

	project := c.createProject("homelab")
	archive := skillArchive(t, "deploy-helm")

	var installed skillResponse
	c.mustUpload("/api/v1/projects/"+project+"/skills?filename=skill.tar.gz",
		"application/gzip", archive, &installed)

	if installed.Name != "deploy-helm" {
		t.Fatalf("name = %q, want the one the manifest declares", installed.Name)
	}
	if installed.Description == "" {
		t.Fatal("the description must come from the manifest")
	}
	// Nothing upstream identifies an upload, so the content does.
	if installed.InstalledRevision != installed.BundleSHA256 {
		t.Fatalf("installed revision = %q, want the bundle checksum", installed.InstalledRevision)
	}
	if objects.count() != 1 {
		t.Fatalf("stored objects = %d, want the bundle", objects.count())
	}

	var listed struct {
		Items []skillResponse `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/skills", nil, &listed, http.StatusOK)
	if len(listed.Items) != 1 || listed.Items[0].ID != installed.ID {
		t.Fatalf("listing = %+v, want the installed skill", listed.Items)
	}

	// Installing the same name again is an update, not a second Skill.
	var reinstalled skillResponse
	c.mustUpload("/api/v1/projects/"+project+"/skills?filename=skill.tar.gz",
		"application/gzip", skillArchive(t, "deploy-helm"), &reinstalled)

	c.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/skills", nil, &listed, http.StatusOK)
	if len(listed.Items) != 1 {
		t.Fatalf("listing = %+v, want one skill after a reinstall", listed.Items)
	}

	c.mustDo(http.MethodDelete, "/api/v1/skills/"+reinstalled.ID, nil, nil, http.StatusNoContent)
	c.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/skills", nil, &listed, http.StatusOK)
	if len(listed.Items) != 0 {
		t.Fatalf("listing = %+v, want nothing after the uninstall", listed.Items)
	}
	if objects.count() != 0 {
		t.Fatal("uninstalling a skill must remove its bundle")
	}
}

// TestSkillRefusesSomethingThatIsNotASkill pins that Core checks the shape
// before it stores anything.
func TestSkillRefusesSomethingThatIsNotASkill(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))

	project := c.createProject("homelab")

	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	writer := tar.NewWriter(gz)
	content := "hello\n"
	_ = writer.WriteHeader(&tar.Header{
		Name: "README.md", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
	})
	_, _ = writer.Write([]byte(content))
	_ = writer.Close()
	_ = gz.Close()

	status := c.doRaw(http.MethodPost, "/api/v1/projects/"+project+"/skills?filename=x.tar.gz",
		"application/gzip", packed.String(), nil)
	if status != http.StatusBadRequest {
		t.Fatalf("installing a non-skill = %d, want 400: %s", status, c.lastBody)
	}
	if objects.count() != 0 {
		t.Fatal("a refused install must store nothing")
	}
}

// TestSkillRefusesAnUnknownSource pins that Core only acquires from the sources
// section 18 lists.
func TestSkillRefusesAnUnknownSource(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))

	project := c.createProject("homelab")

	status := c.do(http.MethodPost, "/api/v1/projects/"+project+"/skills",
		map[string]any{"source": map[string]any{"type": "npm", "url": "https://example.invalid/x"}}, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("installing from an unknown source = %d, want 400: %s", status, c.lastBody)
	}
}

// TestSkillsRefuseWithoutStorage pins that a Core with no object storage says so
// rather than recording a Skill whose bundle went nowhere.
func TestSkillsRefuseWithoutStorage(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")

	status := c.doRaw(http.MethodPost, "/api/v1/projects/"+project+"/skills?filename=skill.tar.gz",
		"application/gzip", skillArchive(t, "deploy-helm"), nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("installing without storage = %d, want 503: %s", status, c.lastBody)
	}
}

// TestProjectInstructionsAreEditable pins the provider-independent project rules
// of specification section 18.
func TestProjectInstructionsAreEditable(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")

	var updated struct {
		Name         string `json:"name"`
		Instructions string `json:"instructions"`
	}
	c.mustDo(http.MethodPatch, "/api/v1/projects/"+project,
		map[string]any{"instructions": "Always run the chart tests."}, &updated, http.StatusOK)

	if updated.Instructions != "Always run the chart tests." {
		t.Fatalf("instructions = %q, want what was set", updated.Instructions)
	}
	// An absent field keeps its value: editing the instructions must not have to
	// resend the name.
	if updated.Name != "homelab" {
		t.Fatalf("name = %q, want it untouched", updated.Name)
	}

	var reread struct {
		Instructions string `json:"instructions"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+project, nil, &reread, http.StatusOK)
	if !strings.Contains(reread.Instructions, "chart tests") {
		t.Fatalf("instructions = %q, want them persisted", reread.Instructions)
	}
}

// TestSkillBundleReachesTheBackend pins specification section 18: Core
// distributes the immutable artefact over the protocol, and the backend verifies
// what it received.
func TestSkillBundleReachesTheBackend(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))

	project := c.createProject("homelab")
	_, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	var installed skillResponse
	c.mustUpload("/api/v1/projects/"+project+"/skills?filename=skill.tar.gz",
		"application/gzip", skillArchive(t, "deploy-helm"), &installed)

	bundle, err := backend.sdk.FetchSkill(context.Background(),
		installed.ID, installed.InstalledRevision, installed.BundleSHA256)
	if err != nil {
		t.Fatalf("fetching the bundle: %v", err)
	}
	if len(bundle) == 0 {
		t.Fatal("the bundle came back empty")
	}

	// The checksum was verified by FetchSkill; this pins that the bytes are the
	// skill rather than something that merely hashes.
	gz, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		t.Fatalf("reading the bundle: %v", err)
	}
	defer func() { _ = gz.Close() }()

	var names []string
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err != nil {
			break
		}
		names = append(names, header.Name)
	}
	if !slices.Contains(names, "SKILL.md") {
		t.Fatalf("bundle = %v, want the manifest at its root", names)
	}
}

// TestSkillBundleIsRefusedForAnUnknownSkill pins that a backend waiting on a
// bundle learns it is not coming, instead of hanging.
func TestSkillBundleIsRefusedForAnUnknownSkill(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)

	_, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := backend.sdk.FetchSkill(ctx, uuid.NewString(), "revision", "")
	if err == nil {
		t.Fatal("fetching an unknown skill must fail")
	}
	if !errors.Is(err, sdkclient.ErrSkillFetchFailed) {
		t.Fatalf("got %v, want ErrSkillFetchFailed", err)
	}
}

// TestBackendSkillInventoryIsRecorded pins the other half of section 18: Core
// knows the metadata of a Skill that only exists on one backend, and never its
// content.
func TestBackendSkillInventoryIsRecorded(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	backendID, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	if err := backend.sdk.SendSkillInventory(context.Background(), []*backendv1.LocalSkill{
		{Name: "corp-deploy", Description: "Only reachable from the work network.", Available: true},
	}); err != nil {
		t.Fatalf("reporting the inventory: %v", err)
	}

	var listed struct {
		Items []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Available   bool   `json:"available"`
		} `json:"items"`
	}
	waitUntil(t, "the local skill to be recorded", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/backends/"+backendID+"/skills", nil, &listed, http.StatusOK)
		return len(listed.Items) == 1
	})

	if listed.Items[0].Name != "corp-deploy" || !listed.Items[0].Available {
		t.Fatalf("recorded = %+v, want the reported skill", listed.Items[0])
	}
}

// TestSkillBundlesAreNotProjectArtifacts pins that the bytes behind an installed
// Skill are not offered as something to manage: deleting one would only produce
// a conflict with the Skill that points at it.
func TestSkillBundlesAreNotProjectArtifacts(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))

	project := c.createProject("homelab")

	var uploaded artifactResponse
	c.mustUpload("/api/v1/projects/"+project+"/artifacts?filename=build.log",
		"text/plain", "a line\n", &uploaded)

	var installed skillResponse
	c.mustUpload("/api/v1/projects/"+project+"/skills?filename=skill.tar.gz",
		"application/gzip", skillArchive(t, "deploy-helm"), &installed)

	var listed struct {
		Items []artifactResponse `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/artifacts", nil, &listed, http.StatusOK)

	if len(listed.Items) != 1 || listed.Items[0].ID != uploaded.ID {
		t.Fatalf("listing = %+v, want only the uploaded artifact", listed.Items)
	}
}
