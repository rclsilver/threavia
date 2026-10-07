package s3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// client is an ObjectStore over any S3-compatible endpoint.
//
// The library is named after one implementation; nothing here assumes it. The
// endpoint, the region and the addressing style are all configured, which is
// what section 24 asks for and what lets Garage, MinIO, Ceph or S3 itself serve
// the same deployment.
type client struct {
	api    *minio.Client
	bucket string
	cfg    Config
}

// newClient builds the S3 client for cfg.
func newClient(ctx context.Context, cfg Config) (ObjectStore, error) {
	endpoint, secure, err := parseEndpoint(cfg)
	if err != nil {
		return nil, err
	}

	api, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: bucketLookup(cfg),
	})
	if err != nil {
		return nil, fmt.Errorf("build the object storage client: %w", err)
	}

	// Failing here rather than on the first upload turns a misconfiguration into
	// a startup error, which is where an operator can still see it.
	exists, err := api.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("reach object storage at %s: %w", cfg.Endpoint, err)
	}
	if !exists {
		return nil, fmt.Errorf("%w: bucket %q does not exist", ErrNotConfigured, cfg.Bucket)
	}

	return &client{api: api, bucket: cfg.Bucket, cfg: cfg}, nil
}

// parseEndpoint splits the configured URL into what the client needs.
func parseEndpoint(cfg Config) (host string, secure bool, err error) {
	raw := cfg.Endpoint
	if !strings.Contains(raw, "://") {
		scheme := "http"
		if cfg.UseTLS {
			scheme = "https"
		}
		raw = scheme + "://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false, fmt.Errorf("invalid object storage endpoint: %w", err)
	}
	if parsed.Host == "" {
		return "", false, fmt.Errorf("%w: endpoint has no host", ErrNotConfigured)
	}
	return parsed.Host, parsed.Scheme == "https", nil
}

func bucketLookup(cfg Config) minio.BucketLookupType {
	if cfg.ForcePathStyle {
		return minio.BucketLookupPath
	}
	return minio.BucketLookupAuto
}

// Put stores an object and verifies its checksum.
//
// The digest is computed from what was actually written rather than trusted from
// the caller, so a truncated or corrupted upload cannot be recorded as a good
// one (spec section 28).
func (c *client) Put(ctx context.Context, key string, body io.Reader, meta ObjectMeta) (ObjectMeta, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	digest := sha256.New()
	limited := io.LimitReader(io.TeeReader(body, digest), c.cfg.MaxUploadBytes+1)

	info, err := c.api.PutObject(ctx, c.bucket, key, limited, -1, minio.PutObjectOptions{
		ContentType: contentTypeOr(meta.ContentType),
	})
	if err != nil {
		return ObjectMeta{}, fmt.Errorf("store object: %w", err)
	}
	if info.Size > c.cfg.MaxUploadBytes {
		// Removing it keeps a rejected upload from occupying the bucket.
		_ = c.api.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
		return ObjectMeta{}, fmt.Errorf("object exceeds the %d byte limit", c.cfg.MaxUploadBytes)
	}

	stored := ObjectMeta{
		Key:         key,
		Size:        info.Size,
		ContentType: contentTypeOr(meta.ContentType),
		SHA256:      hex.EncodeToString(digest.Sum(nil)),
	}
	if meta.SHA256 != "" && meta.SHA256 != stored.SHA256 {
		_ = c.api.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
		return ObjectMeta{}, fmt.Errorf("checksum mismatch: stored %s, expected %s", stored.SHA256, meta.SHA256)
	}
	return stored, nil
}

// Get opens an object for reading.
func (c *client) Get(ctx context.Context, key string) (io.ReadCloser, ObjectMeta, error) {
	object, err := c.api.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectMeta{}, translateError(err, key)
	}

	info, err := object.Stat()
	if err != nil {
		_ = object.Close()
		return nil, ObjectMeta{}, translateError(err, key)
	}
	return object, metaFrom(key, info), nil
}

// Stat describes an object without reading it.
func (c *client) Stat(ctx context.Context, key string) (ObjectMeta, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	info, err := c.api.StatObject(ctx, c.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectMeta{}, translateError(err, key)
	}
	return metaFrom(key, info), nil
}

// Delete removes an object.
func (c *client) Delete(ctx context.Context, key string) error {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	if err := c.api.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return translateError(err, key)
	}
	return nil
}

func (c *client) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.cfg.OperationTimeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, c.cfg.OperationTimeout)
}

func metaFrom(key string, info minio.ObjectInfo) ObjectMeta {
	return ObjectMeta{
		Key:         key,
		Size:        info.Size,
		ContentType: info.ContentType,
	}
}

func contentTypeOr(value string) string {
	if value == "" {
		return "application/octet-stream"
	}
	return value
}

// translateError maps a storage error onto the package sentinels.
func translateError(err error, key string) error {
	if err == nil {
		return nil
	}
	response := minio.ToErrorResponse(err)
	if response.Code == "NoSuchKey" || response.StatusCode == 404 {
		return fmt.Errorf("%w: %s", ErrObjectNotFound, key)
	}
	var notFound *minio.ErrorResponse
	if errors.As(err, &notFound) && notFound.StatusCode == 404 {
		return fmt.Errorf("%w: %s", ErrObjectNotFound, key)
	}
	return err
}
