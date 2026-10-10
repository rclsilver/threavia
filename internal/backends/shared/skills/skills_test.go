package skills

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func bundle(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	writer := tar.NewWriter(gz)
	for name, content := range files {
		if err := writer.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("packing the bundle: %v", err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("packing the bundle: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("packing the bundle: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("packing the bundle: %v", err)
	}
	return packed.Bytes()
}

func TestCacheStoresAndReportsARevision(t *testing.T) {
	t.Parallel()

	cache := NewCache(t.TempDir())
	if cache.Has("skill-1", "rev-a") {
		t.Fatal("an empty cache must report nothing")
	}

	path, err := cache.Store("skill-1", "rev-a", bundle(t, map[string]string{
		Manifest:         "---\nname: a\n---\n",
		"reference/x.md": "details\n",
	}))
	if err != nil {
		t.Fatalf("storing the bundle: %v", err)
	}
	if !cache.Has("skill-1", "rev-a") {
		t.Fatal("a stored revision must be reported as cached")
	}
	if _, err := os.Stat(filepath.Join(path, "reference", "x.md")); err != nil {
		t.Fatalf("the bundle assets must be unpacked: %v", err)
	}
	// Another revision of the same Skill is a different directory, so a new
	// version never overwrites one a running Job is reading.
	if cache.Has("skill-1", "rev-b") {
		t.Fatal("a different revision must not be reported as cached")
	}
}

// TestCacheRefusesABundleThatEscapesItsDirectory pins that what arrives over the
// wire cannot write outside the cache.
func TestCacheRefusesABundleThatEscapesItsDirectory(t *testing.T) {
	t.Parallel()

	cache := NewCache(t.TempDir())
	_, err := cache.Store("skill-1", "rev-a", bundle(t, map[string]string{
		"../../escaped.txt": "owned\n",
	}))
	if err == nil {
		t.Fatal("a bundle that escapes its directory must be refused")
	}
}

// TestCacheRefusesABundleWithNoManifest pins that a half-valid bundle never
// becomes a Skill the provider is told about.
func TestCacheRefusesABundleWithNoManifest(t *testing.T) {
	t.Parallel()

	cache := NewCache(t.TempDir())
	if _, err := cache.Store("skill-1", "rev-a", bundle(t, map[string]string{"README.md": "hi\n"})); err == nil {
		t.Fatal("a bundle with no manifest must be refused")
	}
	if cache.Has("skill-1", "rev-a") {
		t.Fatal("a refused bundle must not look cached")
	}
}

func TestAssembleBuildsAPluginDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := filepath.Join(root, "project-skill")
	local := filepath.Join(root, "local-skill")
	for _, directory := range []string{project, local} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("creating %s: %v", directory, err)
		}
		if err := os.WriteFile(filepath.Join(directory, Manifest), []byte("---\n---\n"), 0o644); err != nil {
			t.Fatalf("writing the manifest: %v", err)
		}
	}

	assembled, err := Assemble(filepath.Join(root, "plugin"),
		[]Entry{{Name: "deploy", Path: project}},
		[]Entry{{Name: "corp", Path: local}})
	if err != nil {
		t.Fatalf("assembling the plugin: %v", err)
	}
	if assembled == "" {
		t.Fatal("a non-empty skill set must yield a directory")
	}

	manifest, err := os.ReadFile(filepath.Join(assembled, ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatalf("reading the plugin manifest: %v", err)
	}
	var declared pluginManifest
	if err := json.Unmarshal(manifest, &declared); err != nil {
		t.Fatalf("decoding the plugin manifest: %v", err)
	}
	if declared.Name == "" {
		t.Fatal("the plugin manifest must name the plugin")
	}

	for _, name := range []string{"deploy", "corp"} {
		if _, err := os.Stat(filepath.Join(assembled, "skills", name, Manifest)); err != nil {
			t.Fatalf("the skill %q must be reachable: %v", name, err)
		}
	}
}

// TestAssemblePrefersTheProjectSkillOnACollision pins the order: a Core-managed
// Skill is the shared, reviewed definition, so it wins over a local one with the
// same name.
func TestAssemblePrefersTheProjectSkillOnACollision(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	project := filepath.Join(root, "from-core")
	local := filepath.Join(root, "from-laptop")
	for _, directory := range []string{project, local} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("creating %s: %v", directory, err)
		}
		if err := os.WriteFile(filepath.Join(directory, Manifest),
			[]byte("---\n---\n"+filepath.Base(directory)+"\n"), 0o644); err != nil {
			t.Fatalf("writing the manifest: %v", err)
		}
	}

	assembled, err := Assemble(filepath.Join(root, "plugin"),
		[]Entry{{Name: "deploy", Path: project}},
		[]Entry{{Name: "deploy", Path: local}})
	if err != nil {
		t.Fatalf("assembling the plugin: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(assembled, "skills", "deploy", Manifest))
	if err != nil {
		t.Fatalf("reading the exposed skill: %v", err)
	}
	if !bytes.Contains(content, []byte("from-core")) {
		t.Fatalf("exposed skill = %q, want the project one", content)
	}
}

func TestAssembleSaysNothingForAnEmptySet(t *testing.T) {
	t.Parallel()

	assembled, err := Assemble(filepath.Join(t.TempDir(), "plugin"))
	if err != nil {
		t.Fatalf("assembling nothing: %v", err)
	}
	if assembled != "" {
		t.Fatalf("assembled = %q, want nothing for an empty set", assembled)
	}
}

func TestDiscoverReadsLocalSkills(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "corp-deploy")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("creating the skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, Manifest),
		[]byte("---\nname: corp-deploy\ndescription: Only on the work laptop.\n---\n"), 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}
	// A directory that is not a Skill is ignored rather than reported as broken.
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}

	found := Discover([]string{root})
	if len(found) != 1 {
		t.Fatalf("discovered = %+v, want the single skill", found)
	}
	if found[0].Name != "corp-deploy" || found[0].Description == "" {
		t.Fatalf("discovered = %+v, want the declared metadata", found[0])
	}
	if !found[0].Available {
		t.Fatal("a discovered local skill is available")
	}
}
