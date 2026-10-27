// Package controller manages constrained execution pods. It is deployed with
// its own service account; workers and control backends receive no such rights.
package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rclsilver/threavia/internal/execution/kube"
)

type Kubernetes interface {
	CreatePod(context.Context, map[string]any) (kube.Pod, error)
	Pod(context.Context, string) (kube.Pod, error)
	DeletePod(context.Context, string, string) error
	ReleasePod(context.Context, kube.Pod) error
	ListPods(context.Context, string) ([]kube.Pod, error)
	CreatePolicy(context.Context, map[string]any) error
	DeletePolicy(context.Context, string) error
	CreateSecret(context.Context, map[string]any) error
	DeleteSecret(context.Context, string) error
}
type Profile struct {
	Spec        kube.Profile `json:"spec"`
	TokenFile   string       `json:"tokenFile"`
	MaxJobs     int          `json:"maxJobs"`
	MaxMonitors int          `json:"maxMonitors"`
}
type Config struct {
	ID, Namespace, PublicURL, StatePath, CASecretName string
	ControlNetwork                                    kube.ControlNetwork
	Profiles                                          []Profile
}
type Controller struct {
	cfg      Config
	api      Kubernetes
	state    *store
	mu       sync.Mutex
	rooms    map[string]*room
	profiles map[string]Profile
	ctx      context.Context
	ready    bool
	closed   bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}
type Result struct {
	ID, State, PodName, PodUID, Error string
	ConfirmedExit                     bool
}

func New(ctx context.Context, cfg Config, api Kubernetes) (*Controller, error) {
	if cfg.ID == "" || cfg.Namespace == "" || cfg.PublicURL == "" || cfg.StatePath == "" || cfg.CASecretName == "" || len(cfg.Profiles) == 0 {
		return nil, errors.New("execution controller configuration is incomplete")
	}
	name := regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	u, urlErr := url.Parse(cfg.PublicURL)
	if !name.MatchString(cfg.ID) || !name.MatchString(cfg.Namespace) || !name.MatchString(cfg.CASecretName) || urlErr != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return nil, errors.New("executor identity, namespace, CA secret or HTTPS origin is invalid")
	}
	s, err := openStore(cfg.StatePath)
	if err != nil {
		return nil, fmt.Errorf("open durable executor state: %w", err)
	}
	c := &Controller{cfg: cfg, api: api, state: s, ctx: ctx, rooms: map[string]*room{}, profiles: map[string]Profile{}}
	tokens := map[[32]byte]bool{}
	for _, p := range cfg.Profiles {
		if err := p.Spec.Validate(); err != nil {
			return nil, err
		}
		if p.TokenFile == "" || p.MaxJobs < 1 || p.MaxMonitors < 1 {
			return nil, errors.New("profile credentials and capacities are required")
		}
		if _, exists := c.profiles[p.Spec.Name]; exists {
			return nil, errors.New("duplicate execution profile")
		}
		token, err := readToken(p.TokenFile)
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256([]byte(token))
		if tokens[hash] {
			return nil, errors.New("execution profiles must have distinct credentials")
		}
		tokens[hash] = true
		c.profiles[p.Spec.Name] = p
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	return c, nil
}
func readToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(raw))
	if len(s) < 32 {
		return "", errors.New("execution credential must contain at least 32 bytes")
	}
	return s, nil
}
func (c *Controller) authenticate(r *http.Request) (Profile, bool) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == r.Header.Get("Authorization") || len(token) < 32 {
		return Profile{}, false
	}
	for _, p := range c.profiles {
		expected, err := readToken(p.TokenFile)
		if err == nil && subtle.ConstantTimeCompare([]byte(expected), []byte(token)) == 1 {
			return p, true
		}
	}
	return Profile{}, false
}
func (c *Controller) saveLocked() error {
	if err := c.state.save(); err != nil {
		c.ready = false
		return err
	}
	return nil
}
func (c *Controller) Boot(ctx context.Context) error {
	// A controller restart interrupts executions rather than issuing their
	// commands again. Fence every owned pod before accepting any new work.
	pods, err := c.api.ListPods(ctx, "execution.threavia.io/controller="+c.cfg.ID)
	if err != nil {
		return err
	}
	for _, pod := range pods {
		c.mu.Lock()
		var found *Record
		for _, r := range c.state.data.Records {
			if r.PodName == pod.Metadata.Name && (r.PodUID == "" || r.PodUID == pod.Metadata.UID) {
				found = r
				break
			}
		}
		c.mu.Unlock()
		if found == nil {
			return fmt.Errorf("owned execution pod %s has no durable record; refusing to start", pod.Metadata.Name)
		}
		if found.PodUID == "" {
			c.mu.Lock()
			found.PodUID = pod.Metadata.UID
			if err = c.saveLocked(); err != nil {
				c.mu.Unlock()
				return err
			}
			c.mu.Unlock()
		}
		if !found.ConfirmedExit {
			if err = c.stop(ctx, found, "INTERRUPTED"); err != nil {
				return err
			}
		} else if found.ConfirmedExit {
			if err = c.cleanup(ctx, found); err != nil {
				return err
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.state.data.Records {
		if !r.ConfirmedExit {
			if r.PodUID != "" {
				return fmt.Errorf("execution %s disappeared without a confirmed exit", r.Request.ID)
			}
			r.State = "INTERRUPTED"
			r.Error = "The executor restarted before confirming this execution."
			// No worker is admitted before the Pod UID is durably stored.
			r.ConfirmedExit = true
			r.UpdatedAt = time.Now().UTC()
		}
	}
	if err = c.saveLocked(); err != nil {
		return err
	}
	c.ready = true
	c.wg.Add(1)
	go func() { defer c.wg.Done(); c.reap() }()
	return nil
}
func (c *Controller) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		ready := c.ready
		c.mu.Unlock()
		if !ready {
			http.Error(w, "executor unavailable", 503)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("POST /v1/lease/open", c.openLease)
	mux.HandleFunc("POST /v1/lease/renew", c.renewLease)
	mux.HandleFunc("POST /v1/executions", c.create)
	mux.HandleFunc("GET /v1/executions/{id}", c.get)
	mux.HandleFunc("POST /v1/executions/{id}/stop", c.stopHTTP)
	mux.HandleFunc("GET /v1/executions/{id}/stream", c.backendStream)
	mux.HandleFunc("GET /v1/workers/{session}/stream", c.workerStream)
	return mux
}
func readBody(w http.ResponseWriter, r *http.Request, v any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request must contain a single JSON value")
	}
	return nil
}
func (c *Controller) authorized(w http.ResponseWriter, r *http.Request) (Profile, bool) {
	p, ok := c.authenticate(r)
	if !ok {
		http.Error(w, "unauthorized", 401)
		return p, false
	}
	c.mu.Lock()
	ready := c.ready
	c.mu.Unlock()
	if !ready {
		http.Error(w, "executor unavailable", 503)
		return p, false
	}
	return p, true
}
func (c *Controller) leaseOKLocked(profile, boot string) bool {
	l := c.state.data.Leases[profile]
	return l != nil && l.BootID == boot && l.ExpiresAt.After(time.Now())
}
func (c *Controller) openLease(w http.ResponseWriter, r *http.Request) {
	p, ok := c.authorized(w, r)
	if !ok {
		return
	}
	var input struct{ BootID string }
	if readBody(w, r, &input) != nil || uuid.Validate(input.BootID) != nil {
		http.Error(w, "invalid backend incarnation", 400)
		return
	}
	c.mu.Lock()
	l := c.state.data.Leases[p.Spec.Name]
	if l == nil {
		l = &lease{}
		c.state.data.Leases[p.Spec.Name] = l
	}
	for _, old := range l.Retired {
		if old == input.BootID {
			c.mu.Unlock()
			http.Error(w, "backend incarnation was superseded", 409)
			return
		}
	}
	previous := l.BootID
	if previous == input.BootID && !l.ExpiresAt.IsZero() && !l.ExpiresAt.After(time.Now()) {
		c.mu.Unlock()
		http.Error(w, "backend incarnation expired; start a new incarnation", 409)
		return
	}
	if previous != "" && previous != input.BootID {
		l.Retired = append(l.Retired, previous)
	}
	l.BootID = input.BootID
	l.ExpiresAt = time.Time{}
	records := []*Record{}
	for _, e := range c.state.data.Records {
		if e.Profile == p.Spec.Name && e.BootID != input.BootID && !e.ConfirmedExit {
			records = append(records, e)
		}
	}
	err := c.saveLocked()
	c.mu.Unlock()
	if err != nil {
		http.Error(w, "cannot persist backend incarnation", 503)
		return
	}
	for _, e := range records {
		if err = c.stop(r.Context(), e, "INTERRUPTED"); err != nil {
			http.Error(w, "previous execution stop is unconfirmed", 503)
			return
		}
	}
	c.mu.Lock()
	if c.state.data.Leases[p.Spec.Name].BootID != input.BootID {
		c.mu.Unlock()
		http.Error(w, "backend incarnation was superseded", 409)
		return
	}
	l.ExpiresAt = time.Now().Add(30 * time.Second)
	expires := l.ExpiresAt
	err = c.saveLocked()
	c.mu.Unlock()
	if err != nil {
		http.Error(w, "cannot persist lease", 503)
		return
	}
	writeJSON(w, map[string]any{"expiresAt": expires})
}
func (c *Controller) renewLease(w http.ResponseWriter, r *http.Request) {
	p, ok := c.authorized(w, r)
	if !ok {
		return
	}
	var input struct{ BootID string }
	if readBody(w, r, &input) != nil {
		http.Error(w, "invalid lease", 400)
		return
	}
	c.mu.Lock()
	l := c.state.data.Leases[p.Spec.Name]
	if l == nil || l.BootID != input.BootID || !l.ExpiresAt.After(time.Now()) {
		c.mu.Unlock()
		http.Error(w, "backend lease is no longer active", 409)
		return
	}
	l.ExpiresAt = time.Now().Add(30 * time.Second)
	expires := l.ExpiresAt
	err := c.saveLocked()
	c.mu.Unlock()
	if err != nil {
		http.Error(w, "cannot persist lease", 503)
		return
	}
	writeJSON(w, map[string]any{"expiresAt": expires})
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func (c *Controller) lookupLocked(p Profile, id, boot string) (*Record, *room) {
	r := c.state.data.Records[recordKey(p.Spec.Name, id)]
	if r == nil || r.BootID != boot {
		return nil, nil
	}
	return r, c.rooms[recordKey(p.Spec.Name, id)]
}
func result(r *Record) Result {
	return Result{ID: r.Request.ID, State: r.State, PodName: r.PodName, PodUID: r.PodUID, Error: r.Error, ConfirmedExit: r.ConfirmedExit}
}
func (c *Controller) get(w http.ResponseWriter, r *http.Request) {
	p, ok := c.authorized(w, r)
	if !ok {
		return
	}
	c.mu.Lock()
	record, _ := c.lookupLocked(p, r.PathValue("id"), r.Header.Get("X-Threavia-Boot"))
	if record == nil {
		c.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	v := result(record)
	c.mu.Unlock()
	writeJSON(w, v)
}
func (c *Controller) create(w http.ResponseWriter, r *http.Request) {
	p, ok := c.authorized(w, r)
	if !ok {
		return
	}
	var request kube.Request
	if readBody(w, r, &request) != nil || request.Validate(p.Spec, time.Now()) != nil {
		http.Error(w, "invalid execution request", 400)
		return
	}
	raw, _ := json.Marshal(request)
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	boot := r.Header.Get("X-Threavia-Boot")
	key := recordKey(p.Spec.Name, request.ID)
	c.mu.Lock()
	if !c.leaseOKLocked(p.Spec.Name, boot) {
		c.mu.Unlock()
		http.Error(w, "backend lease is not active", 409)
		return
	}
	if old := c.state.data.Records[key]; old != nil {
		if old.Digest != digest || old.BootID != boot {
			c.mu.Unlock()
			http.Error(w, "execution ID already belongs to another request", 409)
			return
		}
		v := result(old)
		c.mu.Unlock()
		writeJSON(w, v)
		return
	}
	jobs, monitors, probes := 0, 0, 0
	for _, entry := range c.state.data.Records {
		if entry.Profile == p.Spec.Name && !entry.ConfirmedExit {
			if entry.Request.Kind == "monitor" {
				monitors++
			} else if entry.Request.Kind == "probe" {
				probes++
			} else {
				jobs++
			}
		}
	}
	if request.Kind == "monitor" && monitors >= p.MaxMonitors || request.Kind == "job" && jobs >= p.MaxJobs || request.Kind == "probe" && probes >= 1 {
		c.mu.Unlock()
		http.Error(w, "execution profile capacity reached", 409)
		return
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		c.mu.Unlock()
		http.Error(w, "cannot create worker identity", 503)
		return
	}
	rt := newRoom(uuid.NewString(), hex.EncodeToString(random))
	record := &Record{Profile: p.Spec.Name, Request: request, Digest: digest, BootID: boot, PodName: kube.PodName(c.cfg.ID, p.Spec.Name, request.ID), State: "STARTING", StartedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	c.state.data.Records[key] = record
	c.rooms[key] = rt
	err := c.saveLocked()
	c.mu.Unlock()
	if err != nil {
		http.Error(w, "cannot persist execution", 503)
		return
	}
	launch := kube.Launch{Controller: c.cfg.ID, Namespace: c.cfg.Namespace, PublicURL: c.cfg.PublicURL, CASecretName: c.cfg.CASecretName, SessionID: rt.id, Token: rt.token, Profile: p.Spec, Request: request}
	policy, err := launch.NetworkPolicy(c.cfg.ControlNetwork)
	if err == nil {
		err = c.api.CreatePolicy(r.Context(), policy)
	}
	var pod kube.Pod
	podAttempted := false
	if err == nil {
		var manifest map[string]any
		manifest, err = launch.Pod(time.Now())
		if err == nil {
			podAttempted = true
			pod, err = c.api.CreatePod(r.Context(), manifest)
			if err == nil {
				err = c.api.CreateSecret(r.Context(), launch.WorkerSecret(pod.Metadata.UID))
			}
		}
	}
	c.mu.Lock()
	if pod.Metadata.UID != "" {
		record.PodUID = pod.Metadata.UID
	}
	if err != nil {
		var rejected *kube.APIError
		definitelyRejected := pod.Metadata.UID == "" && errors.As(err, &rejected) && rejected.Status >= 400 && rejected.Status < 500 && rejected.Status != http.StatusConflict
		if !podAttempted || definitelyRejected {
			record.State = "FAILED"
			record.ConfirmedExit = true
		} else {
			record.State = "STOPPING"
			record.FinalState = "INTERRUPTED"
		}
		record.Error = "Kubernetes could not create the execution pod."
	} else {
		record.PodUID = pod.Metadata.UID
	}
	record.UpdatedAt = time.Now().UTC()
	persistErr := c.saveLocked()
	v := result(record)
	cleanup := record.ConfirmedExit && pod.Metadata.UID != ""
	c.mu.Unlock()
	// Stop may have won the race while the API was creating an unadmitted pod.
	// It cannot run a command, but still must not be left behind.
	if cleanup {
		cleanupCtx, cancel := context.WithTimeout(c.ctx, 30*time.Second)
		_ = c.cleanup(cleanupCtx, record)
		cancel()
	}
	if persistErr != nil {
		http.Error(w, "cannot persist pod identity", 503)
		return
	}
	// An API timeout can occur after the server created the Pod. Its ownership
	// labels and durable name are retained for reconciliation; do not retry a
	// command with a fresh ID or pretend that absence of the response is a stop.
	if err != nil {
		http.Error(w, "execution creation failed; reconciliation required", 503)
		return
	}
	writeJSON(w, v)
}
func (c *Controller) stopHTTP(w http.ResponseWriter, r *http.Request) {
	p, ok := c.authorized(w, r)
	if !ok {
		return
	}
	c.mu.Lock()
	record, _ := c.lookupLocked(p, r.PathValue("id"), r.Header.Get("X-Threavia-Boot"))
	c.mu.Unlock()
	if record == nil {
		http.NotFound(w, r)
		return
	}
	if err := c.stop(r.Context(), record, "STOPPED"); err != nil {
		http.Error(w, "execution stop is unconfirmed", 503)
		return
	}
	c.mu.Lock()
	v := result(record)
	c.mu.Unlock()
	writeJSON(w, v)
}
func (c *Controller) stop(ctx context.Context, r *Record, final string) error {
	key := recordKey(r.Profile, r.Request.ID)
	c.mu.Lock()
	if terminal(r.State) && r.ConfirmedExit {
		c.mu.Unlock()
		return c.cleanup(ctx, r)
	}
	rt := c.rooms[key]
	if rt == nil {
		rt = newRoom("", "")
		c.rooms[key] = rt
	}
	if !rt.stopMu.TryLock() {
		c.mu.Unlock()
		return errors.New("execution stop is already being confirmed")
	}
	defer rt.stopMu.Unlock()
	if r.FinalState == "" {
		r.FinalState = final
	}
	r.State = "STOPPING"
	r.UpdatedAt = time.Now().UTC()
	err := c.saveLocked()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if rt != nil {
		rt.close()
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		pod, err := c.api.Pod(ctx, r.PodName)
		if err == nil {
			c.mu.Lock()
			expectedUID := r.PodUID
			admitted := r.Admitted
			c.mu.Unlock()
			if expectedUID != "" && pod.Metadata.UID != expectedUID {
				return errors.New("execution pod identity changed")
			}
			ended, _ := pod.Terminated()
			// Ask the runtime to stop independently of a cooperative worker. Our
			// finalizer retains the Pod object as evidence until exit is confirmed,
			// even when its node is unreachable or a grace period elapses.
			if admitted && !ended && pod.Metadata.DeletionTimestamp == nil {
				if err := c.api.DeletePod(ctx, r.PodName, pod.Metadata.UID); err != nil && !kube.IsNotFound(err) {
					return err
				}
			}
			if ended || !admitted {
				c.mu.Lock()
				r.PodUID = pod.Metadata.UID
				r.ConfirmedExit = true
				r.State = r.FinalState
				r.UpdatedAt = time.Now().UTC()
				err = c.saveLocked()
				c.mu.Unlock()
				if err != nil {
					return err
				}
				return c.cleanup(ctx, r)
			}
		} else if kube.IsNotFound(err) {
			c.mu.Lock()
			confirmed := r.ConfirmedExit
			if !r.Admitted {
				r.ConfirmedExit = true
				r.State = r.FinalState
				err = c.saveLocked()
				confirmed = err == nil
			}
			c.mu.Unlock()
			if confirmed {
				return c.cleanup(ctx, r)
			}
			return errors.New("execution pod disappeared without a confirmed container exit")
		} else if ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (c *Controller) reap() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastCollected := time.Now()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
		}
		now := time.Now()
		if now.Sub(lastCollected) >= 30*time.Second {
			collection, cancel := context.WithTimeout(c.ctx, 15*time.Second)
			c.collect(collection)
			cancel()
			lastCollected = now
		}
		c.mu.Lock()
		records := []*Record{}
		for _, r := range c.state.data.Records {
			if r.ConfirmedExit {
				continue
			}
			l := c.state.data.Leases[r.Profile]
			if r.State == "STOPPING" || l == nil || l.BootID != r.BootID || !l.ExpiresAt.After(now) || !r.Request.ExpiresAt.After(now) {
				records = append(records, r)
			}
		}
		c.mu.Unlock()
		for _, r := range records {
			c.background(func() {
				ctx, cancel := context.WithTimeout(c.ctx, 60*time.Second)
				defer cancel()
				final := "INTERRUPTED"
				if !r.Request.ExpiresAt.After(time.Now()) {
					final = "EXPIRED"
				}
				_ = c.stop(ctx, r, final)
			})
		}
	}
}
