package controller

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rclsilver/threavia/internal/execution/bridge"
	"golang.org/x/net/websocket"
)

type room struct {
	id, token               string
	mu                      sync.Mutex
	worker, backend         *websocket.Conn
	workerUsed, backendUsed bool
	pipeOnce                sync.Once
	closeOnce               sync.Once
	done                    chan struct{}
	stopMu                  sync.Mutex
}

func newRoom(id, token string) *room { return &room{id: id, token: token, done: make(chan struct{})} }
func (r *room) close() {
	r.closeOnce.Do(func() {
		r.mu.Lock()
		if r.worker != nil {
			_ = r.worker.SetWriteDeadline(time.Now())
			_ = r.worker.Close()
		}
		if r.backend != nil {
			_ = r.backend.SetWriteDeadline(time.Now())
			_ = r.backend.Close()
		}
		r.mu.Unlock()
		close(r.done)
	})
}
func (r *room) join(worker bool, conn *websocket.Conn, beforePipe func() error, onLost func()) error {
	r.mu.Lock()
	select {
	case <-r.done:
		r.mu.Unlock()
		return errors.New("execution stream is closed")
	default:
	}
	if worker {
		if r.workerUsed {
			r.mu.Unlock()
			return errors.New("worker stream is single use")
		}
		r.workerUsed = true
		r.worker = conn
	} else {
		if r.backendUsed {
			r.mu.Unlock()
			return errors.New("backend stream is single use")
		}
		r.backendUsed = true
		r.backend = conn
	}
	ready := r.worker != nil && r.backend != nil
	if ready {
		if err := beforePipe(); err != nil {
			r.mu.Unlock()
			r.close()
			return err
		}
		r.pipeOnce.Do(func() { go r.pipe(onLost) })
	}
	r.mu.Unlock()
	<-r.done
	return nil
}
func (r *room) pipe(onLost func()) {
	r.mu.Lock()
	worker, backend := r.worker, r.backend
	r.mu.Unlock()
	stopped := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(worker, backend); stopped <- struct{}{} }()
	go func() { _, _ = io.Copy(backend, worker); stopped <- struct{}{} }()
	<-stopped
	r.close()
	onLost()
}
func (c *Controller) admit(record *Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || record.State == "STOPPING" || terminal(record.State) || !c.leaseOKLocked(record.Profile, record.BootID) {
		return errors.New("execution is no longer admitted")
	}
	record.Admitted = true
	return c.saveLocked()
}
func (c *Controller) lost(key string) {
	c.mu.Lock()
	record := c.state.data.Records[key]
	active := record != nil && !terminal(record.State) && record.State != "STOPPING"
	c.mu.Unlock()
	if !active {
		return
	}
	c.background(func() {
		ctx, cancel := context.WithTimeout(c.ctx, 60*time.Second)
		defer cancel()
		_ = c.stop(ctx, record, "INTERRUPTED")
	})
}
func (c *Controller) backendStream(w http.ResponseWriter, r *http.Request) {
	p, ok := c.authorized(w, r)
	if !ok {
		return
	}
	c.mu.Lock()
	record, rt := c.lookupLocked(p, r.PathValue("id"), r.Header.Get("X-Threavia-Boot"))
	valid := record != nil && rt != nil && !terminal(record.State) && record.State != "STOPPING" && c.leaseOKLocked(p.Spec.Name, record.BootID)
	c.mu.Unlock()
	if !valid {
		http.Error(w, "execution stream is unavailable", 409)
		return
	}
	key := recordKey(p.Spec.Name, r.PathValue("id"))
	websocket.Handler(func(conn *websocket.Conn) {
		conn.PayloadType = websocket.BinaryFrame
		conn.MaxPayloadBytes = bridge.MaxFrame + 4
		_ = rt.join(false, conn, func() error { return c.admit(record) }, func() { c.lost(key) })
	}).ServeHTTP(w, r)
}
func (c *Controller) workerStream(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == r.Header.Get("Authorization") || len(token) < 32 {
		http.Error(w, "unauthorized", 401)
		return
	}
	c.mu.Lock()
	var rt *room
	var record *Record
	key := ""
	for k, room := range c.rooms {
		if room.id == r.PathValue("session") && subtle.ConstantTimeCompare([]byte(room.token), []byte(token)) == 1 {
			key = k
			rt = room
			record = c.state.data.Records[k]
			break
		}
	}
	valid := c.ready && !c.closed && rt != nil && record != nil && !terminal(record.State) && record.State != "STOPPING" && c.leaseOKLocked(record.Profile, record.BootID) && record.Request.ExpiresAt.After(time.Now())
	if !valid {
		c.mu.Unlock()
		http.Error(w, "unauthorized", 401)
		return
	}
	if record.PodUID == "" {
		c.mu.Unlock()
		http.Error(w, "pod identity is not yet confirmed", 503)
		return
	}
	if record.PodUID != r.Header.Get("X-Threavia-Pod-UID") {
		c.mu.Unlock()
		http.Error(w, "unauthorized", 401)
		return
	}
	c.mu.Unlock()
	websocket.Handler(func(conn *websocket.Conn) {
		conn.PayloadType = websocket.BinaryFrame
		conn.MaxPayloadBytes = bridge.MaxFrame + 4
		// The record is visible as connected before the control client attaches.
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			_ = conn.Close()
			return
		}
		if record.State == "STARTING" {
			record.State = "RUNNING"
			record.UpdatedAt = time.Now().UTC()
			if c.saveLocked() != nil {
				c.mu.Unlock()
				_ = conn.Close()
				return
			}
		}
		c.mu.Unlock()
		_ = rt.join(true, conn, func() error { return c.admit(record) }, func() { c.lost(key) })
	}).ServeHTTP(w, r)
}
