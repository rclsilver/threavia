package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/rclsilver/threavia/internal/execution/kube"
	"github.com/rclsilver/threavia/internal/execution/transport"
)

type fakeAPI struct {
	mu               sync.Mutex
	pods             map[string]kube.Pod
	created, deleted int
	observed         chan struct{}
	once             sync.Once
	secrets          map[string]bool
	secretErr        error
}

func (f *fakeAPI) CreatePod(_ context.Context, m map[string]any) (kube.Pod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var p kube.Pod
	p.Metadata.Name = m["metadata"].(map[string]any)["name"].(string)
	p.Metadata.UID = uuid.NewString()
	p.Metadata.Finalizers = append([]string{}, m["metadata"].(map[string]any)["finalizers"].([]string)...)
	p.Status.Phase = "Running"
	f.pods[p.Metadata.Name] = p
	f.created++
	return p, nil
}
func (f *fakeAPI) Pod(_ context.Context, name string) (kube.Pod, error) {
	if f.observed != nil {
		f.once.Do(func() { close(f.observed) })
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.pods[name]
	if !ok {
		return kube.Pod{}, &kube.APIError{Status: 404, Reason: "NotFound"}
	}
	return p, nil
}
func (f *fakeAPI) DeletePod(_ context.Context, name, uid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.pods[name]
	if !ok {
		return &kube.APIError{Status: 404, Reason: "NotFound"}
	}
	if p.Metadata.UID != uid {
		return &kube.APIError{Status: 409, Reason: "Conflict"}
	}
	if len(p.Metadata.Finalizers) > 0 {
		now := time.Now()
		p.Metadata.DeletionTimestamp = &now
		f.pods[name] = p
		return nil
	}
	delete(f.pods, name)
	f.deleted++
	return nil
}
func (f *fakeAPI) ListPods(context.Context, string) ([]kube.Pod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := []kube.Pod{}
	for _, p := range f.pods {
		result = append(result, p)
	}
	return result, nil
}
func (f *fakeAPI) CreatePolicy(context.Context, map[string]any) error { return nil }
func (f *fakeAPI) DeletePolicy(context.Context, string) error         { return nil }
func (f *fakeAPI) finish(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pods[name]
	p.Status.Phase = "Succeeded"
	p.Status.ContainerStatuses = []kube.ContainerStatus{{Name: "worker"}}
	p.Status.ContainerStatuses[0].State.Terminated = &kube.Termination{ExitCode: 0, FinishedAt: time.Now()}
	f.pods[name] = p
}
func (f *fakeAPI) ReleasePod(_ context.Context, p kube.Pod) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	current := f.pods[p.Metadata.Name]
	if current.Metadata.UID != p.Metadata.UID {
		return &kube.APIError{Status: 409, Reason: "Conflict"}
	}
	current.Metadata.Finalizers = nil
	f.pods[p.Metadata.Name] = current
	return nil
}
func (f *fakeAPI) CreateSecret(_ context.Context, secret map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.secretErr != nil {
		return f.secretErr
	}
	if f.secrets == nil {
		f.secrets = map[string]bool{}
	}
	f.secrets[secret["metadata"].(map[string]any)["name"].(string)] = true
	return nil
}
func (f *fakeAPI) DeleteSecret(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.secrets, name)
	return nil
}

type harness struct {
	t           *testing.T
	c           *Controller
	api         *fakeAPI
	http        *httptest.Server
	token, boot string
	cfg         Config
	cancel      context.CancelFunc
}

func testConfig(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	token := strings.Repeat("a", 48)
	file := filepath.Join(root, "client-token")
	if err := os.WriteFile(file, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	p := kube.Profile{Name: "codex", Provider: "codex", Image: "worker:test", WorkerBinary: "/worker", ProviderBinary: "codex", Home: "/home/agent", WorkingDirectory: "/workspace", UID: 1001, GID: 1001, ServiceAccount: "execution-worker", Mounts: []kube.Mount{{Name: "work", Claim: "workspace", Path: "/workspace", Role: "workspace"}}, Resources: map[string]any{"requests": map[string]string{"cpu": "100m", "memory": "128Mi"}, "limits": map[string]string{"cpu": "1", "memory": "1Gi"}}}
	return Config{ID: "test", Namespace: "execution", PublicURL: "https://executor.execution.svc:8443", CASecretName: "executor-ca", StatePath: filepath.Join(root, "state.json"), ControlNetwork: kube.ControlNetwork{Namespace: "execution", Selector: map[string]string{"app": "executor"}, Port: 8443}, Profiles: []Profile{{Spec: p, TokenFile: file, MaxJobs: 1, MaxMonitors: 2}}}
}
func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := testConfig(t)
	api := &fakeAPI{pods: map[string]kube.Pod{}}
	c, err := New(ctx, cfg, api)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, c: c, api: api, cfg: cfg, token: strings.Repeat("a", 48), boot: uuid.NewString(), cancel: cancel}
	h.http = httptest.NewTLSServer(c.Handler())
	t.Cleanup(func() {
		cancel()
		stop, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelStop()
		if err := c.Close(stop); err != nil {
			t.Error(err)
		}
		h.http.Close()
	})
	code, _ := h.request(http.MethodPost, "/v1/lease/open", map[string]any{"BootID": h.boot}, h.token)
	if code != 200 {
		t.Fatalf("open lease: %d", code)
	}
	return h
}
func (h *harness) request(method, path string, input any, token string) (int, []byte) {
	h.t.Helper()
	raw, _ := json.Marshal(input)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, method, h.http.URL+path, bytes.NewReader(raw))
	if err != nil {
		h.t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Threavia-Boot", h.boot)
	response, err := h.http.Client().Do(r)
	if err != nil {
		h.t.Error(err)
		return 0, nil
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response.StatusCode, body
}
func request() kube.Request {
	id := uuid.NewString()
	return kube.Request{ID: id, JobID: uuid.NewString(), RunID: uuid.NewString(), Kind: "job", Directory: "/workspace/project", ExpiresAt: time.Now().Add(time.Hour)}
}
func (h *harness) create(r kube.Request) Result {
	h.t.Helper()
	code, raw := h.request(http.MethodPost, "/v1/executions", r, h.token)
	if code != 200 {
		h.t.Fatalf("create execution: %d %s", code, raw)
	}
	var result Result
	if err := json.Unmarshal(raw, &result); err != nil {
		h.t.Fatal(err)
	}
	return result
}

func TestExecutionCreationIsScopedAndIdempotent(t *testing.T) {
	h := newHarness(t)
	r := request()
	first := h.create(r)
	again := h.create(r)
	if first.PodUID != again.PodUID || h.api.created != 1 {
		t.Fatal("idempotent retry launched another pod")
	}
	r.Writable = true
	code, _ := h.request(http.MethodPost, "/v1/executions", r, h.token)
	if code != 409 {
		t.Fatal("reused ID changed permissions")
	}
	code, _ = h.request(http.MethodGet, "/v1/executions/"+r.ID, nil, strings.Repeat("b", 48))
	if code != 401 {
		t.Fatal("untrusted caller read execution")
	}
	code, _ = h.request(http.MethodPost, "/v1/executions", map[string]any{"ID": uuid.NewString(), "manifest": map[string]any{"privileged": true}}, h.token)
	if code != 400 {
		t.Fatal("caller supplied arbitrary pod manifest")
	}
	code, _ = h.request(http.MethodPost, "/v1/executions", request(), h.token)
	if code != 409 {
		t.Fatal("execution capacity ignored")
	}
}
func TestUnadmittedPendingPodCanBeCancelled(t *testing.T) {
	h := newHarness(t)
	r := request()
	h.create(r)
	code, raw := h.request(http.MethodPost, "/v1/executions/"+r.ID+"/stop", nil, h.token)
	if code != 200 {
		t.Fatalf("pending stop: %d %s", code, raw)
	}
	h.api.mu.Lock()
	left := len(h.api.pods)
	secretsLeft := len(h.api.secrets)
	h.api.mu.Unlock()
	if left != 0 || secretsLeft != 0 {
		t.Fatal("pending pod was not cleaned up")
	}
	var result Result
	_ = json.Unmarshal(raw, &result)
	if !result.ConfirmedExit || result.State != "STOPPED" {
		t.Fatal("pending stop not recorded")
	}
}

func TestSecretFailureRetainsCreatedPodUntilConfirmedCleanup(t *testing.T) {
	h := newHarness(t)
	r := request()
	h.api.mu.Lock()
	h.api.secretErr = &kube.APIError{Status: 403, Reason: "Forbidden"}
	h.api.mu.Unlock()
	code, _ := h.request(http.MethodPost, "/v1/executions", r, h.token)
	if code != 503 {
		t.Fatalf("credential creation unexpectedly succeeded: %d", code)
	}
	h.c.mu.Lock()
	record := h.c.state.data.Records[recordKey("codex", r.ID)]
	state, uid, confirmed := record.State, record.PodUID, record.ConfirmedExit
	h.c.mu.Unlock()
	if state != "STOPPING" || uid == "" || confirmed {
		t.Fatal("failed Secret creation lost the created Pod's fence")
	}
	code, _ = h.request(http.MethodPost, "/v1/executions/"+r.ID+"/stop", nil, h.token)
	if code != 200 {
		t.Fatal("unadmitted Pod not cleaned after Secret failure")
	}
}
func TestAdmittedExecutionWaitsForContainerExitBeforeConfirmation(t *testing.T) {
	h := newHarness(t)
	r := request()
	created := h.create(r)
	h.c.mu.Lock()
	record := h.c.state.data.Records[recordKey("codex", r.ID)]
	record.Admitted = true
	h.c.mu.Unlock()
	h.api.observed = make(chan struct{})
	stopped := make(chan int, 1)
	go func() {
		code, _ := h.request(http.MethodPost, "/v1/executions/"+r.ID+"/stop", nil, h.token)
		stopped <- code
	}()
	select {
	case <-h.api.observed:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not inspect container")
	}
	select {
	case code := <-stopped:
		t.Fatalf("stop was confirmed before exit (%d)", code)
	default:
	}
	h.c.mu.Lock()
	confirmed := record.ConfirmedExit
	h.c.mu.Unlock()
	if confirmed {
		t.Fatal("running container marked stopped")
	}
	h.api.finish(created.PodName)
	select {
	case code := <-stopped:
		if code != 200 {
			t.Fatalf("stop returned %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not observe container termination")
	}
}
func TestDisappearedAdmittedPodStaysUnconfirmedAndHoldsCapacity(t *testing.T) {
	h := newHarness(t)
	r := request()
	created := h.create(r)
	h.c.mu.Lock()
	record := h.c.state.data.Records[recordKey("codex", r.ID)]
	record.Admitted = true
	h.c.mu.Unlock()
	h.api.mu.Lock()
	delete(h.api.pods, created.PodName)
	h.api.mu.Unlock()
	code, _ := h.request(http.MethodPost, "/v1/executions/"+r.ID+"/stop", nil, h.token)
	if code != 503 {
		t.Fatalf("missing pod became proof of stop: %d", code)
	}
	h.c.mu.Lock()
	confirmed := record.ConfirmedExit
	h.c.mu.Unlock()
	if confirmed {
		t.Fatal("uncertain execution freed its fence")
	}
	code, _ = h.request(http.MethodPost, "/v1/executions", request(), h.token)
	if code != 409 {
		t.Fatal("uncertain execution did not hold capacity")
	}
}
func TestBackendTakeoverRetiresOldIncarnationAndCleansPendingWork(t *testing.T) {
	h := newHarness(t)
	r := request()
	h.create(r)
	old := h.boot
	next := uuid.NewString()
	code, raw := h.request(http.MethodPost, "/v1/lease/open", map[string]any{"BootID": next}, h.token)
	if code != 200 {
		t.Fatalf("takeover: %d %s", code, raw)
	}
	code, _ = h.request(http.MethodPost, "/v1/lease/renew", map[string]any{"BootID": old}, h.token)
	if code != 409 {
		t.Fatal("old incarnation renewed a lease")
	}
	code, _ = h.request(http.MethodPost, "/v1/lease/open", map[string]any{"BootID": old}, h.token)
	if code != 409 {
		t.Fatal("old incarnation took the lease back")
	}
	h.api.mu.Lock()
	left := len(h.api.pods)
	h.api.mu.Unlock()
	if left != 0 {
		t.Fatal("takeover left old pending pod")
	}
}
func TestControllerRestartInterruptsWithoutReexecuting(t *testing.T) {
	h := newHarness(t)
	r := request()
	h.create(r)
	h.cancel()
	h.http.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	next, err := New(ctx, h.cfg, h.api)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = next.Close(stop)
	})
	if err = next.Boot(ctx); err != nil {
		t.Fatal(err)
	}
	next.mu.Lock()
	record := next.state.data.Records[recordKey("codex", r.ID)]
	state, confirmed := record.State, record.ConfirmedExit
	next.mu.Unlock()
	if state != "INTERRUPTED" || !confirmed || h.api.created != 1 {
		t.Fatalf("restart replayed or lost execution: %s %v", state, confirmed)
	}
}

func TestScopedWorkerAttachmentCannotManageOrReattachExecution(t *testing.T) {
	h := newHarness(t)
	req := request()
	created := h.create(req)
	h.c.mu.Lock()
	room := h.c.rooms[recordKey("codex", req.ID)]
	workerToken, sessionID := room.token, room.id
	h.c.mu.Unlock()
	code, _ := h.request(http.MethodPost, "/v1/lease/renew", map[string]any{"BootID": h.boot}, workerToken)
	if code != 401 {
		t.Fatal("worker credential managed backend lease")
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: h.http.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := transport.New(h.http.URL, ca)
	if err != nil {
		t.Fatal(err)
	}
	defer client.HTTP.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	workerHeaders := http.Header{}
	workerHeaders.Set("Authorization", "Bearer "+workerToken)
	workerHeaders.Set("X-Threavia-Pod-UID", "another-pod")
	if conn, err := client.Dial(ctx, "/v1/workers/"+sessionID+"/stream", workerHeaders); err == nil {
		conn.Close()
		t.Fatal("worker attached with another Pod UID")
	}
	workerHeaders.Set("X-Threavia-Pod-UID", created.PodUID)
	workerConn, err := client.Dial(ctx, "/v1/workers/"+sessionID+"/stream", workerHeaders)
	if err != nil {
		t.Fatal(err)
	}
	defer workerConn.Close()
	backendHeaders := http.Header{}
	backendHeaders.Set("Authorization", "Bearer "+h.token)
	backendHeaders.Set("X-Threavia-Boot", h.boot)
	backendConn, err := client.Dial(ctx, "/v1/executions/"+req.ID+"/stream", backendHeaders)
	if err != nil {
		t.Fatal(err)
	}
	defer backendConn.Close()
	if _, err = workerConn.Write([]byte("scoped")); err != nil {
		t.Fatal(err)
	}
	_ = backendConn.SetReadDeadline(time.Now().Add(time.Second))
	var raw [6]byte
	if _, err = io.ReadFull(backendConn, raw[:]); err != nil || string(raw[:]) != "scoped" {
		t.Fatal("scoped stream failed:", err)
	}
	duplicate, err := client.Dial(ctx, "/v1/workers/"+sessionID+"/stream", workerHeaders)
	if err == nil {
		defer duplicate.Close()
		_ = duplicate.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		if _, err = duplicate.Read(b[:]); err == nil {
			t.Fatal("consumed worker token attached twice")
		}
	}
	h.c.mu.Lock()
	admitted := h.c.state.data.Records[recordKey("codex", req.ID)].Admitted
	h.c.mu.Unlock()
	if !admitted {
		t.Fatal("bridge forwarded bytes before durable admission")
	}
	h.api.finish(created.PodName)
}

func TestExpiredBackendIncarnationCannotBeResurrected(t *testing.T) {
	h := newHarness(t)
	h.c.mu.Lock()
	h.c.state.data.Leases["codex"].ExpiresAt = time.Now().Add(-time.Second)
	h.c.mu.Unlock()
	for _, operation := range []string{"open", "renew"} {
		code, _ := h.request(http.MethodPost, "/v1/lease/"+operation, map[string]any{"BootID": h.boot}, h.token)
		if code != 409 {
			t.Fatalf("expired incarnation %s returned %d", operation, code)
		}
	}
}
