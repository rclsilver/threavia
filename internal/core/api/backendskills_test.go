package api_test

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/rclsilver/threavia/internal/backends/claude/adapter"
	"github.com/rclsilver/threavia/internal/backends/claude/runner"
	claudeskills "github.com/rclsilver/threavia/internal/backends/claude/skills"
	"github.com/rclsilver/threavia/internal/core/skills"
	sdkclient "github.com/rclsilver/threavia/pkg/backend-sdk/client"
	sdkstate "github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// idleRunner stands in for Claude Code: it records what it was asked to run and
// never spawns anything. The point of these tests is the backend plumbing around
// the provider, not the provider.
//
// Run blocks until the test releases it, because a Job's per-run directories are
// cleaned up the moment it ends and the assertions are about what the provider
// would have seen while it was live.
type idleRunner struct {
	started chan runner.StartParams
	release chan struct{}
}

func newIdleRunner() *idleRunner {
	return &idleRunner{
		started: make(chan runner.StartParams, 4),
		release: make(chan struct{}),
	}
}

func (r *idleRunner) Run(ctx context.Context, params runner.StartParams, _ runner.Sink) error {
	r.started <- params
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return nil
}

func (r *idleRunner) Cancel(string) error { return runner.ErrUnknownJob }
func (r *idleRunner) Available() error    { return nil }

// connectClaudeBackend runs the real Claude adapter against this Core, over the
// real control stream.
func (c *core) connectClaudeBackend(credential, skillCache string, discoveryRoots ...string) *idleRunner {
	c.t.Helper()

	cfg := adapter.Default()
	cfg.Claude.SkillCachePath = skillCache
	cfg.Claude.DefaultWorkingDirectory = c.t.TempDir()
	cfg.Claude.DiscoveryRoots = discoveryRoots

	local := newIdleRunner()
	c.t.Cleanup(func() { close(local.release) })
	handler := adapter.New(cfg, local, sdkstate.NewMemoryStore(),
		slog.New(slog.NewTextHandler(discard{}, nil)))

	clientCfg := sdkclient.DefaultConfig()
	clientCfg.CoreAddress = "passthrough:///bufnet"
	clientCfg.Token = credential
	clientCfg.InstanceName = "claude-test"
	clientCfg.BackendName = "claude"
	clientCfg.BackendVersion = "test"
	clientCfg.TLS.Enabled = false
	clientCfg.HeartbeatInterval = 200 * time.Millisecond
	clientCfg.DialOptions = []grpc.DialOption{c.dialer}

	sdk, err := sdkclient.New(clientCfg, handler, sdkstate.NewMemoryStore(),
		slog.New(slog.NewTextHandler(discard{}, nil)))
	if err != nil {
		c.t.Fatalf("building the claude backend: %v", err)
	}
	handler.Bind(sdk)

	ctx, cancel := context.WithCancel(context.Background())
	c.t.Cleanup(cancel)
	go func() { _ = sdk.Run(ctx) }()

	waitUntil(c.t, "the claude backend to connect", func() bool { return sdk.Connected() })
	return local
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// TestABackendFetchesAndCachesAProjectSkill pins the distribution path of
// specification section 18 end to end, and the regression that made it deadlock:
// a bundle is fetched over the very stream that delivers the start command, so
// the fetch must not happen on the goroutine reading that stream.
func TestABackendFetchesAndCachesAProjectSkill(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)
	c.svc.SetSkillAcquirer(skills.NewAcquirer(skills.DefaultLimits()))

	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")

	var installed skillResponse
	c.mustUpload("/api/v1/projects/"+project+"/skills?filename=skill.tar.gz",
		"application/gzip", skillArchive(t, "deploy-helm"), &installed)

	cache := t.TempDir()
	local := c.connectClaudeBackend(credential, cache)

	c.startSessionFrom(project, backendID, "web", "deploy the chart")

	params := receive(t, "the job to start", local.started)
	if params.SkillDirectory == "" {
		t.Fatal("the job must be given a skill directory")
	}

	// The skill reaches the provider as a session-scoped plugin, so nothing is
	// written into the user repository or their own configuration.
	manifest := filepath.Join(params.SkillDirectory, ".claude-plugin", "plugin.json")
	if _, err := readFile(manifest); err != nil {
		t.Fatalf("the plugin manifest must exist: %v", err)
	}
	exposed := filepath.Join(params.SkillDirectory, "skills", "deploy-helm", claudeskills.Manifest)
	if _, err := readFile(exposed); err != nil {
		t.Fatalf("the skill must be exposed to the provider: %v", err)
	}

	// The project instructions travel with the Job and are mapped by the
	// adapter, never modelled by Core as a provider file.
	c.mustDo(http.MethodPatch, "/api/v1/projects/"+project,
		map[string]any{"instructions": "Run the chart tests."}, nil, http.StatusOK)
}

// TestProjectInstructionsReachTheBackend pins the other half of section 18.
func TestProjectInstructionsReachTheBackend(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")

	c.mustDo(http.MethodPatch, "/api/v1/projects/"+project,
		map[string]any{"instructions": "Always run the chart tests."}, nil, http.StatusOK)

	local := c.connectClaudeBackend(credential, t.TempDir())
	c.startSessionFrom(project, backendID, "web", "deploy the chart")

	params := receive(t, "the job to start", local.started)
	if params.ProjectInstructions != "Always run the chart tests." {
		t.Fatalf("instructions = %q, want the project ones", params.ProjectInstructions)
	}
}

// readFile is os.ReadFile, named here so the intent of the assertions above is
// "the file exists and is readable" rather than a filesystem detail.
func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

// TestADirectoryIsResolvedByDiscovery pins the first steps of specification
// section 11: a Session whose working directory has no binding on this backend
// is located under the configured discovery roots, and the binding is recorded
// so the next Job costs nothing.
func TestADirectoryIsResolvedByDiscovery(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")

	var directory struct {
		ID string `json:"id"`
	}
	c.mustDo(http.MethodPost, "/api/v1/projects/"+project+"/directories",
		map[string]any{"name": "puppet"}, &directory, http.StatusCreated)

	// The directory exists here but Core has never been told where.
	root := t.TempDir()
	expected := filepath.Join(root, "puppet")
	if err := os.MkdirAll(expected, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}

	local := c.connectClaudeBackend(credential, t.TempDir(), root)

	var started startSessionResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{
		"projectId":          project,
		"backendInstanceId":  backendID,
		"workingDirectoryId": directory.ID,
		"message":            "deploy the chart",
	}, &started, http.StatusCreated)

	params := receive(t, "the job to start", local.started)
	if params.WorkingDirectory != expected {
		t.Fatalf("working directory = %q, want the discovered one %q", params.WorkingDirectory, expected)
	}

	// Recorded, so the next Job resolves without searching anything.
	var bindings struct {
		Items []struct {
			Path string `json:"path"`
		} `json:"items"`
	}
	waitUntil(t, "the binding to be recorded", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/directories/"+directory.ID+"/bindings", nil, &bindings, http.StatusOK)
		return len(bindings.Items) == 1
	})
	if bindings.Items[0].Path != expected {
		t.Fatalf("binding = %q, want %q", bindings.Items[0].Path, expected)
	}
}

// TestAnUnresolvedDirectoryAsksTheUser pins the rest of section 11: a directory
// discovery cannot find is not guessed at. The Job waits for the user, and
// starts where they say.
func TestAnUnresolvedDirectoryAsksTheUser(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")

	var directory struct {
		ID string `json:"id"`
	}
	c.mustDo(http.MethodPost, "/api/v1/projects/"+project+"/directories",
		map[string]any{"name": "puppet"}, &directory, http.StatusCreated)

	// Nothing under the roots matches, so discovery finds nothing.
	local := c.connectClaudeBackend(credential, t.TempDir(), t.TempDir())
	elsewhere := t.TempDir()

	var started startSessionResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{
		"projectId":          project,
		"backendInstanceId":  backendID,
		"workingDirectoryId": directory.ID,
		"message":            "deploy the chart",
	}, &started, http.StatusCreated)

	var attention attentionResponse
	waitUntil(t, "the backend to ask where the directory is", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/me/attention", nil, &attention, http.StatusOK)
		return len(attention.UserInputs) == 1
	})

	c.mustDo(http.MethodPost, "/api/v1/user-input/"+attention.UserInputs[0].ID+"/resolve",
		map[string]any{"value": elsewhere}, nil, http.StatusOK)

	params := receive(t, "the job to start", local.started)
	if params.WorkingDirectory != elsewhere {
		t.Fatalf("working directory = %q, want the answered one %q", params.WorkingDirectory, elsewhere)
	}
}
