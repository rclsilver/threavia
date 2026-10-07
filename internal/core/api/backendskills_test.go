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
type idleRunner struct {
	started chan runner.StartParams
}

func (r *idleRunner) Run(_ context.Context, params runner.StartParams, _ runner.Sink) error {
	r.started <- params
	return nil
}

func (r *idleRunner) Cancel(string) error { return runner.ErrUnknownJob }
func (r *idleRunner) Available() error    { return nil }

// connectClaudeBackend runs the real Claude adapter against this Core, over the
// real control stream.
func (c *core) connectClaudeBackend(credential, skillCache string) *idleRunner {
	c.t.Helper()

	cfg := adapter.Default()
	cfg.Claude.SkillCachePath = skillCache
	cfg.Claude.DefaultWorkingDirectory = c.t.TempDir()

	local := &idleRunner{started: make(chan runner.StartParams, 4)}
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
