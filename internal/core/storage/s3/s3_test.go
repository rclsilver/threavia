package s3

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
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

func TestNewRefusesWhenDisabled(t *testing.T) {
	t.Parallel()

	disabled := DefaultConfig()
	if _, err := New(context.Background(), disabled); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("disabled object storage: got %v, want ErrNotConfigured", err)
	}
}

// TestNewFailsOnAnUnreachableEndpoint pins the startup behaviour: a bucket that
// cannot be reached is a configuration error an operator must see, not a store
// that fails later on the first upload.
func TestNewFailsOnAnUnreachableEndpoint(t *testing.T) {
	t.Parallel()

	// A listener that is immediately closed hands us a port nothing answers on.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	address := listener.Addr().String()
	_ = listener.Close()

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Endpoint = "http://" + address
	cfg.Bucket = "threavia"
	cfg.OperationTimeout = time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := New(ctx, cfg); err == nil {
		t.Fatal("an unreachable endpoint must fail at construction")
	}
}

func TestParseEndpoint(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		endpoint string
		useTLS   bool
		wantHost string
		wantTLS  bool
	}{
		{name: "explicit https", endpoint: "https://s3.example.com", wantHost: "s3.example.com", wantTLS: true},
		{name: "explicit http", endpoint: "http://garage:3900", wantHost: "garage:3900"},
		{name: "bare host follows useTLS", endpoint: "garage:3900", useTLS: true, wantHost: "garage:3900", wantTLS: true},
		{name: "bare host without tls", endpoint: "garage:3900", wantHost: "garage:3900"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := DefaultConfig()
			cfg.Endpoint = tc.endpoint
			cfg.UseTLS = tc.useTLS

			host, secure, err := parseEndpoint(cfg)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.endpoint, err)
			}
			if host != tc.wantHost || secure != tc.wantTLS {
				t.Fatalf("parse %q: got (%q, %v), want (%q, %v)",
					tc.endpoint, host, secure, tc.wantHost, tc.wantTLS)
			}
		})
	}
}
