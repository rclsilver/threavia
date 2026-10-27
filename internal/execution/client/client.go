// Package client connects a logical backend to its administrator-owned
// execution profile. It has no Kubernetes API token or manifest interface.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rclsilver/threavia/internal/backends/shared/runner"
	"github.com/rclsilver/threavia/internal/execution/bridge"
	"github.com/rclsilver/threavia/internal/execution/controller"
	"github.com/rclsilver/threavia/internal/execution/kube"
	"github.com/rclsilver/threavia/internal/execution/protocol"
	"github.com/rclsilver/threavia/internal/execution/remote"
	"github.com/rclsilver/threavia/internal/execution/transport"
)

type Config struct{ Endpoint, CAFile, TokenFile, WorkingDirectory string }
type Client struct {
	transport *transport.Transport
	cfg       Config
	boot      string
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	leaseErr  error
}
type httpError struct{ code int }

func (e *httpError) Error() string { return fmt.Sprintf("executor returned HTTP %d", e.code) }

func New(ctx context.Context, cfg Config) (*Client, error) {
	t, err := transport.New(cfg.Endpoint, cfg.CAFile)
	if err != nil {
		return nil, err
	}
	if cfg.TokenFile == "" || cfg.WorkingDirectory == "" {
		return nil, errors.New("executor token file and working directory required")
	}
	ctx, cancel := context.WithCancel(ctx)
	c := &Client{transport: t, cfg: cfg, boot: uuid.NewString(), ctx: ctx, cancel: cancel}
	if err = c.request(ctx, "POST", "/v1/lease/open", map[string]string{"BootID": c.boot}, nil); err != nil {
		cancel()
		return nil, err
	}
	go c.renew()
	return c, nil
}
func (c *Client) Close() error { c.cancel(); c.transport.HTTP.CloseIdleConnections(); return nil }
func (c *Client) Available() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.leaseErr != nil {
		return c.leaseErr
	}
	return c.ctx.Err()
}
func (c *Client) headers() (http.Header, error) {
	raw, err := os.ReadFile(c.cfg.TokenFile)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < 32 {
		return nil, errors.New("executor token must contain at least 32 bytes")
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	h.Set("X-Threavia-Boot", c.boot)
	return h, nil
}
func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	r, err := http.NewRequestWithContext(ctx, method, c.transport.Base+path, body)
	if err != nil {
		return err
	}
	r.Header, err = c.headers()
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	res, err := c.transport.HTTP.Do(r)
	if err != nil {
		return errors.New("executor request failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return &httpError{code: res.StatusCode}
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(output)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	return err
}
func (c *Client) renew() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	// Failing closed also closes all active streams through c.ctx. We never
	// silently obtain a new incarnation and replay active work after a partition.
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(c.ctx, 5*time.Second)
		err := c.request(ctx, "POST", "/v1/lease/renew", map[string]string{"BootID": c.boot}, nil)
		cancel()
		if err != nil {
			c.mu.Lock()
			c.leaseErr = errors.New("executor lease lost; restart the control backend after reconciling executions")
			c.mu.Unlock()
			c.cancel()
			return
		}
	}
}

type session struct {
	io.ReadWriteCloser
	client    *Client
	id        string
	closeOnce sync.Once
	stopClose func() bool
}

func (s *session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.stopClose != nil {
			s.stopClose()
		}
		if conn, ok := s.ReadWriteCloser.(interface{ SetWriteDeadline(time.Time) error }); ok {
			_ = conn.SetWriteDeadline(time.Now())
		}
		err = s.ReadWriteCloser.Close()
	})
	return err
}
func (s *session) Stop(ctx context.Context) error {
	err := s.client.stop(ctx, s.id)
	_ = s.Close()
	return err
}
func (c *Client) stop(ctx context.Context, id string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result controller.Result
		err := c.request(ctx, "POST", "/v1/executions/"+id+"/stop", nil, &result)
		if err == nil && result.ConfirmedExit {
			return nil
		}
		var status controller.Result
		if c.request(ctx, "GET", "/v1/executions/"+id, nil, &status) == nil && status.ConfirmedExit {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("execution stop unconfirmed: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
func (c *Client) open(ctx context.Context, request kube.Request) (remote.Session, error) {
	if err := c.Available(); err != nil {
		return nil, err
	}
	var result controller.Result
	if err := c.request(ctx, "POST", "/v1/executions", request, &result); err != nil {
		// No run/status command has been sent; reconciliation cannot duplicate
		// provider work. Preserve the same execution ID for cleanup.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		_ = c.stop(cleanup, request.ID)
		cancel()
		return nil, err
	}
	attached := false
	defer func() {
		if !attached {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			_ = c.stop(cleanup, request.ID)
			cancel()
		}
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if result.State == "RUNNING" {
			h, err := c.headers()
			if err != nil {
				return nil, err
			}
			conn, err := c.transport.Dial(ctx, "/v1/executions/"+request.ID+"/stream", h)
			if err != nil {
				return nil, err
			}
			s := &session{ReadWriteCloser: conn, client: c, id: request.ID}
			s.stopClose = context.AfterFunc(c.ctx, func() { _ = conn.SetWriteDeadline(time.Now()); _ = conn.Close() })
			attached = true
			return s, nil
		}
		if result.ConfirmedExit || result.State == "STOPPING" {
			return nil, fmt.Errorf("execution did not become ready (%s)", result.State)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.ctx.Done():
			return nil, c.Available()
		case <-ticker.C:
		}
		if err := c.request(ctx, "GET", "/v1/executions/"+request.ID, nil, &result); err != nil {
			return nil, err
		}
	}
}
func (c *Client) OpenJob(ctx context.Context, p runner.StartParams) (remote.Session, error) {
	if p.Policy.Scratch != "" {
		if err := os.MkdirAll(p.Policy.Scratch, 0700); err != nil {
			return nil, fmt.Errorf("prepare shared Run scratch: %w", err)
		}
	}
	duration := 7 * 24 * time.Hour
	if p.Policy.MaxDurationSeconds > 0 {
		duration = time.Duration(p.Policy.MaxDurationSeconds)*time.Second - time.Duration(p.ActiveSecondsUsed)*time.Second
		if duration < time.Second {
			duration = time.Second
		}
	}
	if duration > 7*24*time.Hour {
		duration = 7 * 24 * time.Hour
	}
	start, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return c.open(start, kube.Request{ID: uuid.NewString(), JobID: p.JobID, RunID: p.RunID, Kind: "job", Directory: p.WorkingDirectory, Writable: p.Policy.AllowFilesystemWrite, Network: p.Policy.AllowNetwork, Skills: p.SkillDirectory != "", Scratch: p.Policy.Scratch != "", ExpiresAt: time.Now().Add(duration)})
}
func (c *Client) Probe(ctx context.Context) (protocol.Status, error) {
	s, err := c.open(ctx, kube.Request{ID: uuid.NewString(), Kind: "probe", Directory: c.cfg.WorkingDirectory, Network: true, ExpiresAt: time.Now().Add(2 * time.Minute)})
	if err != nil {
		return protocol.Status{}, err
	}
	defer s.Close()
	peer := bridge.New(ctx, s, nil)
	defer peer.Close()
	defer protocol.KeepAlive(ctx, peer)()
	var status protocol.Status
	err = peer.Call(ctx, "status", nil, &status)
	stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer cancel()
	if stopErr := s.Stop(stop); stopErr != nil {
		return protocol.Status{}, stopErr
	}
	return status, err
}
