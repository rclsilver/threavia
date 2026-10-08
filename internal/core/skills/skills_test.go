package skills

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

const manifest = `---
name: deploy-helm
description: How this project deploys its Helm charts.
---

Always run the chart tests before packaging.
`

func writeSkill(t *testing.T, directory string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(directory, "scripts"), 0o755); err != nil {
		t.Fatalf("creating the skill directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, Manifest), []byte(manifest), 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "scripts", "check.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing a skill asset: %v", err)
	}
}

// entries reads back the paths a packed bundle carries.
func entries(t *testing.T, bundle Bundle) map[string]string {
	t.Helper()

	gz, err := gzip.NewReader(bytes.NewReader(bundle.Content))
	if err != nil {
		t.Fatalf("reading the bundle: %v", err)
	}
	defer func() { _ = gz.Close() }()

	found := make(map[string]string)
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return found
		}
		if err != nil {
			t.Fatalf("reading the bundle: %v", err)
		}
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("reading %s: %v", header.Name, err)
		}
		found[header.Name] = string(content)
	}
}

func TestAcquireFromAnUploadedTarGz(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	writeSkill(t, source)

	archive := tarGz(t, source, "skill/")
	bundle, err := NewAcquirer(DefaultLimits()).Acquire(context.Background(),
		domain.SkillSource{Type: domain.SkillSourceUpload, URL: "skill.tar.gz"}, bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("acquiring the skill: %v", err)
	}

	if bundle.Name != "deploy-helm" {
		t.Fatalf("name = %q, want the one the manifest declares", bundle.Name)
	}
	if !strings.Contains(bundle.Description, "Helm charts") {
		t.Fatalf("description = %q, want the one the manifest declares", bundle.Description)
	}
	// Nothing upstream identifies an upload, so the content does.
	if bundle.InstalledRevision != bundle.SHA256 {
		t.Fatalf("installed revision = %q, want the bundle checksum", bundle.InstalledRevision)
	}

	files := entries(t, bundle)
	if _, ok := files[Manifest]; !ok {
		t.Fatalf("bundle = %v, want the manifest at its root", files)
	}
	if _, ok := files["scripts/check.sh"]; !ok {
		t.Fatalf("bundle = %v, want the skill assets", files)
	}
}

func TestAcquireFromAnUploadedZip(t *testing.T) {
	t.Parallel()

	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	add := func(name, content string) {
		t.Helper()
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("building the zip: %v", err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("building the zip: %v", err)
		}
	}
	add("skill/"+Manifest, manifest)
	add("skill/reference.md", "details\n")
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the zip: %v", err)
	}

	bundle, err := NewAcquirer(DefaultLimits()).Acquire(context.Background(),
		domain.SkillSource{Type: domain.SkillSourceUpload, URL: "skill.zip"}, bytes.NewReader(archive.Bytes()))
	if err != nil {
		t.Fatalf("acquiring the skill: %v", err)
	}
	if _, ok := entries(t, bundle)["reference.md"]; !ok {
		t.Fatal("the single wrapping directory must be unwrapped")
	}
}

// TestPackIsDeterministic pins what the checksum is for: the same content gives
// the same bundle, so a backend can tell a cached copy from a stale one.
func TestPackIsDeterministic(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	writeSkill(t, source)
	archive := tarGz(t, source, "skill/")

	acquirer := NewAcquirer(DefaultLimits())
	first, err := acquirer.Acquire(context.Background(),
		domain.SkillSource{Type: domain.SkillSourceUpload, URL: "a.tar.gz"}, bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("first acquisition: %v", err)
	}
	second, err := acquirer.Acquire(context.Background(),
		domain.SkillSource{Type: domain.SkillSourceUpload, URL: "a.tar.gz"}, bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("second acquisition: %v", err)
	}
	if first.SHA256 != second.SHA256 {
		t.Fatalf("checksums differ for the same content: %s and %s", first.SHA256, second.SHA256)
	}
}

// TestFilesReadsTheBundleBack pins what a person is shown of an installed
// Skill: every file, the text of the readable ones, and nothing of the rest.
func TestFilesReadsTheBundleBack(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	writeSkill(t, source)
	if err := os.WriteFile(filepath.Join(source, "logo.bin"), []byte{0x89, 0x50, 0x00, 0xff}, 0o644); err != nil {
		t.Fatalf("writing a binary asset: %v", err)
	}
	bundle, err := NewAcquirer(DefaultLimits()).Acquire(context.Background(),
		domain.SkillSource{Type: domain.SkillSourceUpload, URL: "a.tar.gz"},
		bytes.NewReader(tarGz(t, source, "skill/")))
	if err != nil {
		t.Fatalf("acquiring: %v", err)
	}

	files, err := Files(bytes.NewReader(bundle.Content), 1<<20)
	if err != nil {
		t.Fatalf("reading the files back: %v", err)
	}
	byPath := make(map[string]File)
	for _, file := range files {
		byPath[file.Path] = file
	}
	if len(byPath) != 3 {
		t.Fatalf("got %d files, want SKILL.md, scripts/check.sh and logo.bin: %+v", len(byPath), files)
	}
	if got := byPath[Manifest].Text; got == nil || *got != manifest {
		t.Fatalf("the manifest must come back as written, got %v", got)
	}
	if byPath["scripts/check.sh"].Text == nil {
		t.Fatal("a script is text and must be shown")
	}
	if byPath["logo.bin"].Text != nil || byPath["logo.bin"].Size != 4 {
		t.Fatalf("a binary file is listed with its size and no text, got %+v", byPath["logo.bin"])
	}

	// Past the limit, a text file is listed but not shown.
	small, err := Files(bytes.NewReader(bundle.Content), 8)
	if err != nil {
		t.Fatalf("reading with a small limit: %v", err)
	}
	for _, file := range small {
		if file.Path == Manifest && file.Text != nil {
			t.Fatal("a file over the limit must carry no text")
		}
	}
}

// TestAcquireRefusesAnArchiveThatEscapesItsRoot pins that a third-party archive
// cannot write outside the directory it is unpacked into.
func TestAcquireRefusesAnArchiveThatEscapesItsRoot(t *testing.T) {
	t.Parallel()

	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	writer := tar.NewWriter(gz)
	content := []byte("owned\n")
	if err := writer.WriteHeader(&tar.Header{
		Name: "../../escaped.txt", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatalf("building the archive: %v", err)
	}
	if _, err := writer.Write(content); err != nil {
		t.Fatalf("building the archive: %v", err)
	}
	_ = writer.Close()
	_ = gz.Close()

	_, err := NewAcquirer(DefaultLimits()).Acquire(context.Background(),
		domain.SkillSource{Type: domain.SkillSourceUpload, URL: "evil.tar.gz"}, bytes.NewReader(packed.Bytes()))
	if !errors.Is(err, ErrInvalidSkill) {
		t.Fatalf("got %v, want ErrInvalidSkill", err)
	}
}

// TestAcquireRefusesSomethingThatIsNotASkill pins that Core checks the shape
// before it stores anything.
func TestAcquireRefusesSomethingThatIsNotASkill(t *testing.T) {
	t.Parallel()

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("writing the file: %v", err)
	}

	_, err := NewAcquirer(DefaultLimits()).Acquire(context.Background(),
		domain.SkillSource{Type: domain.SkillSourceUpload, URL: "a.tar.gz"},
		bytes.NewReader(tarGz(t, source, "")))
	if !errors.Is(err, ErrInvalidSkill) {
		t.Fatalf("got %v, want ErrInvalidSkill", err)
	}
}

func TestAcquireFromGit(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required for the git acquisition test")
	}

	repository := t.TempDir()
	writeSkill(t, repository)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repository
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=threavia", "GIT_AUTHOR_EMAIL=threavia@example.invalid",
			"GIT_COMMITTER_NAME=threavia", "GIT_COMMITTER_EMAIL=threavia@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--initial-branch", "main")
	run("add", ".")
	run("commit", "-m", "the skill")

	limits := DefaultLimits()
	// The fixture repository is on disk, which production refuses by default.
	limits.AllowLocalSources = true

	bundle, err := NewAcquirer(limits).Acquire(context.Background(),
		domain.SkillSource{Type: domain.SkillSourceGit, URL: "file://" + repository}, nil)
	if err != nil {
		t.Fatalf("acquiring from git: %v", err)
	}

	// A commit, not a checksum: a repository identifies its own revision.
	if len(bundle.InstalledRevision) != 40 {
		t.Fatalf("installed revision = %q, want the commit", bundle.InstalledRevision)
	}
	files := entries(t, bundle)
	if _, ok := files[Manifest]; !ok {
		t.Fatalf("bundle = %v, want the manifest", files)
	}
	for name := range files {
		if strings.HasPrefix(name, ".git/") {
			t.Fatalf("bundle carries repository metadata: %s", name)
		}
	}
}

// tarGz packs a directory, optionally under a wrapping prefix.
func tarGz(t *testing.T, directory, prefix string) []byte {
	t.Helper()

	var packed bytes.Buffer
	gz := gzip.NewWriter(&packed)
	writer := tar.NewWriter(gz)

	err := filepath.WalkDir(directory, func(current string, entry os.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		relative, err := filepath.Rel(directory, current)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(current)
		if err != nil {
			return err
		}
		if err := writer.WriteHeader(&tar.Header{
			Name: prefix + filepath.ToSlash(relative), Mode: 0o644,
			Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		_, err = writer.Write(content)
		return err
	})
	if err != nil {
		t.Fatalf("packing the fixture: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("packing the fixture: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("packing the fixture: %v", err)
	}
	return packed.Bytes()
}

// TestLocalGitSourcesAreRefusedByDefault pins the default: a Skill source names
// a third party, and a local path would reach the filesystem Core runs on.
func TestLocalGitSourcesAreRefusedByDefault(t *testing.T) {
	t.Parallel()

	_, err := NewAcquirer(DefaultLimits()).Acquire(context.Background(),
		domain.SkillSource{Type: domain.SkillSourceGit, URL: "file:///etc"}, nil)
	if !errors.Is(err, ErrInvalidSkill) {
		t.Fatalf("got %v, want ErrInvalidSkill", err)
	}
}
