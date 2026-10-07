package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestClient builds a client against the S3-compatible endpoint the
// environment points at, and skips when there is none. It mirrors the PostgreSQL
// harness: these tests run against real storage or not at all, because what they
// pin — checksums, size bounds, missing keys — is exactly what a fake would get
// wrong.
func newTestClient(t *testing.T) (ObjectStore, Config) {
	t.Helper()

	endpoint := os.Getenv("THREAVIA_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set THREAVIA_TEST_S3_ENDPOINT to run the object storage integration tests")
	}

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Endpoint = endpoint
	cfg.Bucket = envOr("THREAVIA_TEST_S3_BUCKET", "threavia")
	cfg.Region = envOr("THREAVIA_TEST_S3_REGION", "garage")
	cfg.AccessKeyID = os.Getenv("THREAVIA_TEST_S3_ACCESS_KEY_ID")
	cfg.SecretAccessKey = os.Getenv("THREAVIA_TEST_S3_SECRET_ACCESS_KEY")
	cfg.OperationTimeout = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := New(ctx, cfg)
	if err != nil {
		t.Fatalf("connecting to object storage at %s: %v", endpoint, err)
	}
	return store, cfg
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func TestClientRoundTrip(t *testing.T) {
	store, _ := newTestClient(t)
	ctx := context.Background()

	key := ObjectKey("project-roundtrip", "artifact-"+hex.EncodeToString([]byte(t.Name()))[:16])
	t.Cleanup(func() { _ = store.Delete(context.Background(), key) })

	content := []byte("a stored artifact, written once and read back\n")
	digest := sha256.Sum256(content)

	stored, err := store.Put(ctx, key, bytes.NewReader(content), ObjectMeta{ContentType: "text/plain"})
	if err != nil {
		t.Fatalf("storing an object: %v", err)
	}
	if stored.Size != int64(len(content)) {
		t.Fatalf("stored size = %d, want %d", stored.Size, len(content))
	}
	if stored.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("stored checksum = %q, want the digest of what was written", stored.SHA256)
	}

	body, meta, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("reading the object back: %v", err)
	}
	defer func() { _ = body.Close() }()

	readBack, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("draining the object: %v", err)
	}
	if !bytes.Equal(readBack, content) {
		t.Fatalf("read back %q, want %q", readBack, content)
	}
	if meta.Size != stored.Size {
		t.Fatalf("read back size %d, want %d", meta.Size, stored.Size)
	}

	described, err := store.Stat(ctx, key)
	if err != nil {
		t.Fatalf("describing the object: %v", err)
	}
	if described.Size != stored.Size {
		t.Fatalf("stat size = %d, want %d", described.Size, stored.Size)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("removing the object: %v", err)
	}
	if _, err := store.Stat(ctx, key); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("after deletion: got %v, want ErrObjectNotFound", err)
	}
}

// TestClientRejectsAChecksumMismatch pins that a caller-declared digest is
// verified against what was written, and that a mismatch leaves nothing behind.
func TestClientRejectsAChecksumMismatch(t *testing.T) {
	store, _ := newTestClient(t)
	ctx := context.Background()

	key := ObjectKey("project-checksum", "artifact-mismatch")
	t.Cleanup(func() { _ = store.Delete(context.Background(), key) })

	_, err := store.Put(ctx, key, strings.NewReader("the real content"),
		ObjectMeta{SHA256: strings.Repeat("0", 64)})
	if err == nil {
		t.Fatal("a declared checksum that does not match what was written must be refused")
	}
	if _, err := store.Stat(ctx, key); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("a refused upload must leave nothing behind: %v", err)
	}
}

// TestClientEnforcesTheSizeBound pins specification section 24: an upload past
// the configured limit is refused and does not occupy the bucket.
func TestClientEnforcesTheSizeBound(t *testing.T) {
	_, cfg := newTestClient(t)

	cfg.MaxUploadBytes = 16
	bounded, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("building a bounded client: %v", err)
	}

	ctx := context.Background()
	key := ObjectKey("project-bound", "artifact-toolarge")
	t.Cleanup(func() { _ = bounded.Delete(context.Background(), key) })

	if _, err := bounded.Put(ctx, key, strings.NewReader(strings.Repeat("x", 1024)), ObjectMeta{}); err == nil {
		t.Fatal("an upload past the configured limit must be refused")
	}
	if _, err := bounded.Stat(ctx, key); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("a rejected upload must leave nothing behind: %v", err)
	}
}

// TestClientReportsAMissingObject pins the sentinel the service relies on to
// tell "no such Artifact" from "the storage is broken".
func TestClientReportsAMissingObject(t *testing.T) {
	store, _ := newTestClient(t)

	if _, _, err := store.Get(context.Background(), ObjectKey("project-missing", "nothing")); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("reading a missing object: got %v, want ErrObjectNotFound", err)
	}
}
