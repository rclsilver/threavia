package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
	"github.com/rclsilver/threavia/internal/core/storage/s3"
)

// memoryObjects is an ObjectStore in memory. It keeps the Artifact tests about
// the Core behaviour — ownership, checksums, scoping, cleanup — rather than
// about an S3 implementation, which the s3 package covers.
type memoryObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	meta    map[string]s3.ObjectMeta
	// failWrites makes the metadata write fail, to pin that a failed upload
	// leaves no orphaned bytes behind.
	putErr error
}

func newMemoryObjects() *memoryObjects {
	return &memoryObjects{
		objects: make(map[string][]byte),
		meta:    make(map[string]s3.ObjectMeta),
	}
}

func (m *memoryObjects) Put(_ context.Context, key string, body io.Reader, meta s3.ObjectMeta) (s3.ObjectMeta, error) {
	if m.putErr != nil {
		return s3.ObjectMeta{}, m.putErr
	}

	content, err := io.ReadAll(body)
	if err != nil {
		return s3.ObjectMeta{}, err
	}
	digest := sha256.Sum256(content)

	stored := s3.ObjectMeta{
		Key:         key,
		Size:        int64(len(content)),
		ContentType: meta.ContentType,
		SHA256:      hex.EncodeToString(digest[:]),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = content
	m.meta[key] = stored
	return stored, nil
}

func (m *memoryObjects) Get(_ context.Context, key string) (io.ReadCloser, s3.ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	content, ok := m.objects[key]
	if !ok {
		return nil, s3.ObjectMeta{}, fmt.Errorf("%w: %s", s3.ErrObjectNotFound, key)
	}
	return io.NopCloser(bytes.NewReader(content)), m.meta[key], nil
}

func (m *memoryObjects) Stat(_ context.Context, key string) (s3.ObjectMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	meta, ok := m.meta[key]
	if !ok {
		return s3.ObjectMeta{}, fmt.Errorf("%w: %s", s3.ErrObjectNotFound, key)
	}
	return meta, nil
}

func (m *memoryObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.objects[key]; !ok {
		return fmt.Errorf("%w: %s", s3.ErrObjectNotFound, key)
	}
	delete(m.objects, key)
	delete(m.meta, key)
	return nil
}

func (m *memoryObjects) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objects)
}

// mustUpload posts a raw body to an Artifact route and decodes what was created.
func (c *core) mustUpload(path, contentType, body string, target any) {
	c.t.Helper()
	if got := c.doRaw(http.MethodPost, path, contentType, body, target); got != http.StatusCreated {
		c.t.Fatalf("POST %s = %d, want 201: %s", path, got, c.lastBody)
	}
}

// artifactResponse is the Artifact shape the client API returns.
type artifactResponse struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mimeType"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	SessionID string `json:"sessionId"`
	JobID     string `json:"jobId"`
}

// TestArtifactsRefuseWithoutStorage pins that a Core without object storage says
// so, instead of recording metadata that points at nothing.
func TestArtifactsRefuseWithoutStorage(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")

	status := c.doRaw(http.MethodPost,
		"/api/v1/projects/"+project+"/artifacts?filename=notes.txt", "text/plain", "x", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("upload without storage = %d, want 503: %s", status, c.lastBody)
	}
}

// TestArtifactRoundTrip walks the whole life of an Artifact through the public
// API: upload, metadata, bytes, listing, deletion.
func TestArtifactRoundTrip(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 1<<20)

	project := c.createProject("homelab")
	content := "the build log of a long run\n"

	var uploaded artifactResponse
	c.mustUpload("/api/v1/projects/"+project+"/artifacts?filename=build.log",
		"text/plain", content, &uploaded)

	digest := sha256.Sum256([]byte(content))
	if uploaded.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("checksum = %q, want the digest of what was sent", uploaded.SHA256)
	}
	if uploaded.Size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", uploaded.Size, len(content))
	}
	if uploaded.Filename != "build.log" {
		t.Fatalf("filename = %q, want build.log", uploaded.Filename)
	}

	var fetched artifactResponse
	c.mustDo(http.MethodGet, "/api/v1/artifacts/"+uploaded.ID, nil, &fetched, http.StatusOK)
	if fetched.ID != uploaded.ID || fetched.SHA256 != uploaded.SHA256 {
		t.Fatalf("metadata does not match the upload: %+v", fetched)
	}

	body, headers := c.download("/api/v1/artifacts/" + uploaded.ID + "/content")
	if body != content {
		t.Fatalf("downloaded %q, want %q", body, content)
	}
	// Agent output is never rendered inside the Threavia origin.
	if got := headers.Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("download content type = %q, want application/octet-stream", got)
	}
	if got := headers.Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("download disposition = %q, want an attachment", got)
	}

	var listed struct {
		Items []artifactResponse `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/artifacts", nil, &listed, http.StatusOK)
	if len(listed.Items) != 1 || listed.Items[0].ID != uploaded.ID {
		t.Fatalf("listing = %+v, want the uploaded artifact", listed.Items)
	}

	c.mustDo(http.MethodDelete, "/api/v1/artifacts/"+uploaded.ID, nil, nil, http.StatusNoContent)
	if objects.count() != 0 {
		t.Fatal("deleting an artifact must remove its bytes too")
	}
	c.mustDo(http.MethodGet, "/api/v1/artifacts/"+uploaded.ID, nil, nil, http.StatusNotFound)
}

// TestArtifactMultipartUpload covers what a browser sends.
func TestArtifactMultipartUpload(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	c.svc.SetObjectStore(newMemoryObjects(), 1<<20)
	project := c.createProject("homelab")

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile("file", "screenshot.png")
	if err != nil {
		t.Fatalf("building the form: %v", err)
	}
	if _, err := part.Write([]byte("not really a png")); err != nil {
		t.Fatalf("writing the form: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing the form: %v", err)
	}

	var uploaded artifactResponse
	c.mustUpload("/api/v1/projects/"+project+"/artifacts",
		writer.FormDataContentType(), form.String(), &uploaded)

	if uploaded.Filename != "screenshot.png" {
		t.Fatalf("filename = %q, want the one the form carried", uploaded.Filename)
	}
}

// TestArtifactFilenameNeverShapesTheKey pins specification section 24: a caller
// cannot steer where the bytes land, whatever it names the file.
func TestArtifactFilenameNeverShapesTheKey(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 1<<20)
	project := c.createProject("homelab")

	var uploaded artifactResponse
	c.mustUpload("/api/v1/projects/"+project+"/artifacts?filename=../../etc/passwd",
		"text/plain", "x", &uploaded)

	if uploaded.Filename != "passwd" {
		t.Fatalf("filename = %q, want the path stripped", uploaded.Filename)
	}

	objects.mu.Lock()
	defer objects.mu.Unlock()
	for key := range objects.objects {
		if key != "projects/"+project+"/artifacts/"+uploaded.ID {
			t.Fatalf("object key = %q, want one derived from the identifiers", key)
		}
	}
}

// TestArtifactRefusedUploadStoresNothing pins that ownership is checked before any byte
// is written, so a refused request cannot leave an orphaned blob behind.
func TestArtifactRefusedUploadStoresNothing(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	objects := newMemoryObjects()
	c.svc.SetObjectStore(objects, 1<<20)

	status := c.doRaw(http.MethodPost,
		"/api/v1/projects/missing/artifacts?filename=a.txt", "text/plain", "x", nil)
	if status != http.StatusNotFound {
		t.Fatalf("upload to an unknown project = %d, want 404: %s", status, c.lastBody)
	}
	if objects.count() != 0 {
		t.Fatal("a refused upload must not store anything")
	}
}

// TestArtifactsAreScopedToTheirOwner pins that another user's Artifact is
// reported as missing rather than as forbidden, so an identifier cannot be
// probed.
func TestArtifactsAreScopedToTheirOwner(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	c.svc.SetObjectStore(newMemoryObjects(), 1<<20)
	project := c.createProject("homelab")

	var uploaded artifactResponse
	c.mustUpload("/api/v1/projects/"+project+"/artifacts?filename=a.txt",
		"text/plain", "x", &uploaded)

	_, err := c.store.GetArtifact(context.Background(), "someone-else", domain.ArtifactID(uploaded.ID))
	if !errors.Is(err, postgres.ErrNotFound) {
		t.Fatalf("reading another owner artifact: got %v, want ErrNotFound", err)
	}
}

// TestMalformedIdentifierIsNotFound pins that a path identifier that is not a
// UUID is answered as missing. It used to reach PostgreSQL and come back as an
// internal error, which told a caller that its typo was the server's problem.
func TestMalformedIdentifierIsNotFound(t *testing.T) {
	t.Parallel()

	c := newCore(t)

	for _, path := range []string{
		"/api/v1/projects/not-a-uuid",
		"/api/v1/projects/not-a-uuid/artifacts",
		"/api/v1/artifacts/not-a-uuid",
		"/api/v1/sessions/not-a-uuid",
	} {
		if got := c.do(http.MethodGet, path, nil, nil); got != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404: %s", path, got, c.lastBody)
		}
	}
}
