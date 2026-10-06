// Package s3 owns Core access to S3-compatible object storage.
//
// Artifact metadata lives in PostgreSQL, Artifact bytes live here
// (THREAVIA_SPEC_V1.md sections 19 and 24). The configuration is a generic
// S3-compatible endpoint: MinIO is one possible deployment, never a hard-coded
// assumption.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"
)

// ErrNotConfigured is returned when object storage is required but no endpoint
// or bucket is configured.
var ErrNotConfigured = errors.New("object storage is not configured")

// ErrNotImplemented is returned by New until the S3 client lands. The
// ObjectStore contract is already fixed so callers can be written against it.
var ErrNotImplemented = errors.New("object storage client not implemented yet")

// ErrObjectNotFound is returned when the requested object key does not exist.
var ErrObjectNotFound = errors.New("object not found")

// Config describes the S3-compatible endpoint Core uses.
type Config struct {
	// Enabled turns object storage on. S3 is a V1 infrastructure requirement,
	// but the first vertical slice can run without it.
	Enabled bool

	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string

	// UseTLS selects https for the endpoint.
	UseTLS bool
	// ForcePathStyle is required by most self-hosted S3 implementations.
	ForcePathStyle bool

	// MaxUploadBytes bounds a single Artifact upload (section 24).
	MaxUploadBytes int64
	// OperationTimeout bounds a single object storage call.
	OperationTimeout time.Duration
}

// DefaultConfig returns development-oriented defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:          false,
		Region:           "us-east-1",
		UseTLS:           false,
		ForcePathStyle:   true,
		MaxUploadBytes:   100 << 20, // 100 MiB
		OperationTimeout: 30 * time.Second,
	}
}

// Validate checks the configuration when object storage is enabled.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Endpoint == "" {
		return fmt.Errorf("%w: endpoint is required", ErrNotConfigured)
	}
	if _, err := url.Parse(c.Endpoint); err != nil {
		return fmt.Errorf("invalid object storage endpoint: %w", err)
	}
	if c.Bucket == "" {
		return fmt.Errorf("%w: bucket is required", ErrNotConfigured)
	}
	if c.MaxUploadBytes <= 0 {
		return errors.New("object storage max upload size must be greater than zero")
	}
	return nil
}

// ObjectMeta describes a stored object.
type ObjectMeta struct {
	Key         string
	Size        int64
	ContentType string
	// SHA256 is the hex-encoded checksum verified on upload (section 28).
	SHA256 string
}

// ObjectStore is the Core view of object storage. Implementations must treat
// keys as opaque and never derive them from user-supplied filenames.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, meta ObjectMeta) (ObjectMeta, error)
	Get(ctx context.Context, key string) (io.ReadCloser, ObjectMeta, error)
	Stat(ctx context.Context, key string) (ObjectMeta, error)
	Delete(ctx context.Context, key string) error
}

// New builds the ObjectStore for cfg.
func New(_ context.Context, cfg Config) (ObjectStore, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, ErrNotConfigured
	}
	return nil, ErrNotImplemented
}

// ObjectKey builds the immutable, opaque storage key of an Artifact. The
// original filename stays metadata in PostgreSQL and never reaches the key
// (section 24).
func ObjectKey(projectID, artifactID string) string {
	projectID = sanitiseKeySegment(projectID)
	artifactID = sanitiseKeySegment(artifactID)
	if projectID == "" {
		return path.Join("artifacts", artifactID)
	}
	return path.Join("projects", projectID, "artifacts", artifactID)
}

// sanitiseKeySegment keeps only characters that are safe in an object key. Any
// unexpected input yields an empty segment rather than a traversal-shaped key.
func sanitiseKeySegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == ".." {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			return ""
		}
	}
	return b.String()
}
