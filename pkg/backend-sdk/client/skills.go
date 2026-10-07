package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// maxSkillBundleBytes bounds a bundle a backend will assemble in memory. Core
// packs the Skill, but a backend still refuses an unbounded transfer rather than
// trusting what arrives on a socket.
const maxSkillBundleBytes = 64 << 20

// ErrSkillFetchFailed is returned when Core cannot serve a Skill bundle.
var ErrSkillFetchFailed = errors.New("core could not serve the skill bundle")

// skillFetch assembles the chunks of one bundle.
//
// The chunks are accumulated as they arrive rather than handed to a channel the
// caller drains: the stream reader must never block on a consumer, or a slow
// Skill download would stall every Job on this backend.
type skillFetch struct {
	mu      sync.Mutex
	content bytes.Buffer
	err     error
	done    chan struct{}
	closed  bool
}

func (f *skillFetch) finish(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed {
		return
	}
	f.err = err
	f.closed = true
	close(f.done)
}

// SendSkillInventory reports the Skills this backend has locally. Core keeps the
// metadata only (spec section 18).
func (c *Client) SendSkillInventory(ctx context.Context, local []*backendv1.LocalSkill) error {
	return c.enqueue(ctx, &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_SkillInventory{
			SkillInventory: &backendv1.SkillInventory{Local: local},
		},
	})
}

// FetchSkill asks Core for the packed bundle of a Core-managed Skill and returns
// it once every chunk has arrived.
//
// The checksum is verified here: a bundle that does not match what the Job
// declared is refused rather than unpacked, so a Skill cannot change identity in
// transit.
func (c *Client) FetchSkill(ctx context.Context, skillID, installedRevision, expectedSHA256 string) ([]byte, error) {
	requestID := uuid.NewString()
	fetch := &skillFetch{done: make(chan struct{})}

	c.skillMu.Lock()
	if c.skillFetches == nil {
		c.skillFetches = make(map[string]*skillFetch)
	}
	c.skillFetches[requestID] = fetch
	c.skillMu.Unlock()

	defer func() {
		c.skillMu.Lock()
		delete(c.skillFetches, requestID)
		c.skillMu.Unlock()
	}()

	if err := c.enqueue(ctx, &backendv1.BackendToCore{
		Message: &backendv1.BackendToCore_SkillFetchRequest{
			SkillFetchRequest: &backendv1.SkillFetchRequest{
				RequestId:         requestID,
				SkillId:           skillID,
				InstalledRevision: installedRevision,
			},
		},
	}); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-fetch.done:
	}

	fetch.mu.Lock()
	defer fetch.mu.Unlock()

	if fetch.err != nil {
		return nil, fetch.err
	}
	content := fetch.content.Bytes()
	if expectedSHA256 != "" {
		digest := sha256.Sum256(content)
		if got := hex.EncodeToString(digest[:]); got != expectedSHA256 {
			return nil, fmt.Errorf("%w: checksum %s, expected %s", ErrSkillFetchFailed, got, expectedSHA256)
		}
	}
	return content, nil
}

// onSkillBundle accumulates one chunk of a bundle.
func (c *Client) onSkillBundle(bundle *backendv1.SkillBundle) {
	c.skillMu.Lock()
	fetch := c.skillFetches[bundle.GetRequestId()]
	c.skillMu.Unlock()

	if fetch == nil {
		// The caller gave up, or this is a late duplicate. Nothing is waiting.
		return
	}

	if failure := bundle.GetError(); failure != nil {
		fetch.finish(fmt.Errorf("%w: %s", ErrSkillFetchFailed, failure.GetMessage()))
		return
	}

	fetch.mu.Lock()
	if chunk := bundle.GetChunk(); len(chunk) > 0 {
		if fetch.content.Len()+len(chunk) > maxSkillBundleBytes {
			fetch.mu.Unlock()
			fetch.finish(fmt.Errorf("%w: bundle exceeds %d bytes", ErrSkillFetchFailed, maxSkillBundleBytes))
			return
		}
		fetch.content.Write(chunk)
	}
	fetch.mu.Unlock()

	if bundle.GetLast() {
		fetch.finish(nil)
	}
}
