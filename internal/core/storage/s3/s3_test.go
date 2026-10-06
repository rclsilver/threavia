package s3

import (
	"context"
	"errors"
	"testing"
)

// TestObjectKeyIsOpaqueAndImmutable pins that keys are derived from identifiers
// only, never from a user-supplied filename (specification section 24).
func TestObjectKeyIsOpaqueAndImmutable(t *testing.T) {
	t.Parallel()

	key := ObjectKey("11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	if key != "projects/11111111-1111-1111-1111-111111111111/artifacts/22222222-2222-2222-2222-222222222222" {
		t.Fatalf("unexpected object key %q", key)
	}

	// Anything that is not an opaque identifier is dropped rather than escaped,
	// so no caller can shape a key with traversal or a filename.
	hostile := []string{"../../etc/passwd", "a/b", "screenshot.png", "..", ".", " ", ""}
	for _, input := range hostile {
		if key := ObjectKey(input, input); key != "artifacts" {
			t.Errorf("ObjectKey(%q, %q) = %q, want %q", input, input, key, "artifacts")
		}
	}
}

func TestValidateRequiresEndpointAndBucket(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("object storage is optional while disabled: %v", err)
	}

	cfg.Enabled = true
	if err := cfg.Validate(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("enabling without an endpoint: got %v, want ErrNotConfigured", err)
	}

	cfg.Endpoint = "http://minio:9000"
	if err := cfg.Validate(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("enabling without a bucket: got %v, want ErrNotConfigured", err)
	}

	cfg.Bucket = "threavia"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a complete configuration must validate: %v", err)
	}
}

func TestNewReportsItsState(t *testing.T) {
	t.Parallel()

	disabled := DefaultConfig()
	if _, err := New(context.Background(), disabled); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("disabled object storage: got %v, want ErrNotConfigured", err)
	}

	enabled := DefaultConfig()
	enabled.Enabled = true
	enabled.Endpoint = "http://minio:9000"
	enabled.Bucket = "threavia"
	if _, err := New(context.Background(), enabled); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("enabled object storage: got %v, want ErrNotImplemented", err)
	}
}
