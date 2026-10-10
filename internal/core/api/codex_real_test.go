package api_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/rclsilver/threavia/internal/backends/codex/hook"
	codex "github.com/rclsilver/threavia/internal/backends/codex/runner"
	"github.com/rclsilver/threavia/internal/backends/shared/adapter"
	"github.com/rclsilver/threavia/internal/backends/shared/mcp"
	"github.com/rclsilver/threavia/internal/core/domain"
	sdkclient "github.com/rclsilver/threavia/pkg/backend-sdk/client"
	sdkstate "github.com/rclsilver/threavia/pkg/backend-sdk/state"
)

// The installed CLI invokes this test executable as its policy hook, exactly
// as the deployed backend invokes its own executable. Intercept before Go's
// testing flag parser, which does not know --codex-policy-hook.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--codex-policy-hook" {
		hook.Run(os.Getenv("THREAVIA_CODEX_POLICY_ENDPOINT"), os.Stdin, os.Stdout)
		return
	}
	os.Exit(m.Run())
}

// Opt-in: runs billable inference with the installed CLI and its existing
// authentication. Core uses a fresh migrated PostgreSQL schema and real SDK,
// adapter, policy/MCP endpoints and event persistence. No fake provider.
func TestCodexRealCoreFeatureParity(t *testing.T) {
	if os.Getenv("THREAVIA_CODEX_REAL_E2E") != "1" {
		t.Skip("set THREAVIA_CODEX_REAL_E2E=1 to run real Codex inference")
	}
	c, backendID, cfg := realCodexBackend(t)
	project := c.createProject("codex-real-feature-parity")
	projectPolicy := guarded()
	projectPolicy["maxDurationSeconds"] = 240
	projectPolicy["rules"] = []map[string]any{
		{"effect": "ASK", "capability": "SHELL", "match": "touch *"},
		{"effect": "DENY", "capability": "FILE_READ", "match": "secrets/**"},
	}
	c.mustDo(http.MethodPut, "/api/v1/projects/"+project+"/policy", projectPolicy, nil, http.StatusOK)
	prompt := "$codex-parity This is an integration test. The test working directory is exactly " + cfg.Provider.DefaultWorkingDirectory + ". Run all commands and create all test files in this directory, not in the scratch directory. Use this exact workdir for shell calls and absolute paths for file tools. " +
		"First call mcp__threavia__ask_user asking for the target environment, then use the answer. " +
		"Run exactly touch approval-marker.txt to exercise validation. Attempt cat secrets/blocked.txt once; expect a policy refusal and do not work around it. " +
		"Write report.txt containing real-codex-confirmed and the environment answer, then publish report.txt with artifact_publish. " +
		"Record a project decision titled real-codex-memory with decision_create. " +
		"Call mcp__threavia__web_search for Go documentation with allowed_domains [go.dev], then mcp__threavia__web_fetch on https://go.dev/doc/. " +
		"Finish with your skill marker and one source URL. Do not commit, push, deploy, or start background jobs."
	session := c.startSession(project, backendID, "", prompt)
	validations, inputs := c.waitRealCodexJobs(t, session, 1, true)
	if validations == 0 || inputs == 0 {
		t.Fatalf("real approval/question path missing: validations=%d inputs=%d", validations, inputs)
	}
	if _, err := os.Stat(filepath.Join(cfg.Provider.DefaultWorkingDirectory, "approval-marker.txt")); err != nil {
		t.Fatal("approved command did not run", err)
	}
	report, err := os.ReadFile(filepath.Join(cfg.Provider.DefaultWorkingDirectory, "report.txt"))
	if err != nil || !strings.Contains(string(report), "real-codex-confirmed") || !strings.Contains(string(report), "staging") {
		t.Fatalf("report verification: %s %v", report, err)
	}
	if len(c.decisions(project)) == 0 {
		t.Fatal("project memory was not recorded")
	}
	var artifacts struct {
		Items []artifactResponse `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+project+"/artifacts", nil, &artifacts, http.StatusOK)
	if len(artifacts.Items) == 0 {
		t.Fatal("artifact was not published")
	}
	events := c.snapshot(session).Events
	deniedRead := false
	for _, event := range events {
		var payload struct{ Name, Error string }
		if event.Type == "tool.failed" && json.Unmarshal(event.Payload, &payload) == nil && payload.Name == "Bash" && strings.Contains(payload.Error, "execution policy refuses") {
			deniedRead = true
		}
		if strings.Contains(string(event.Payload), "test-only-blocked-content") {
			t.Fatal("refused file content leaked into the conversation")
		}
	}
	if !deniedRead {
		t.Fatal("blocked native read was not recorded as a policy refusal")
	}
	for _, name := range []string{"WebSearch", "WebFetch"} {
		completed := false
		for _, event := range events {
			var payload struct {
				Name string `json:"name"`
			}
			if event.Type == "tool.completed" && json.Unmarshal(event.Payload, &payload) == nil && payload.Name == name {
				completed = true
			}
		}
		if !completed {
			t.Fatalf("real %s did not complete successfully", name)
		}
	}
	joined := ""
	for _, event := range events {
		joined += string(event.Payload) + "\n"
	}
	for _, expected := range []string{"native-skill-confirmed", "WebSearch", "WebFetch"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("real events missing %s", expected)
		}
	}
	snapshot := c.snapshot(session)
	if len(snapshot.Runs) != 1 {
		t.Fatalf("expected one Run: %v", snapshot.Runs)
	}
	run, err := c.store.RunByID(context.Background(), domain.RunID(snapshot.Runs[0].ID))
	if err != nil || run.NativeSessionID == nil {
		t.Fatal("native thread not bound", err)
	}
	nativeID := *run.NativeSessionID
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Continue this same conversation. Read report.txt and append resumed-codex-confirmed. Do not repeat the other integration steps."}, nil, http.StatusCreated)
	c.waitRealCodexJobs(t, session, 2, true)
	run, err = c.store.RunByID(context.Background(), domain.RunID(snapshot.Runs[0].ID))
	if err != nil || run.NativeSessionID == nil || *run.NativeSessionID != nativeID {
		t.Fatal("resume changed the native conversation", err)
	}
	report, err = os.ReadFile(filepath.Join(cfg.Provider.DefaultWorkingDirectory, "report.txt"))
	if err != nil || !strings.Contains(string(report), "resumed-codex-confirmed") {
		t.Fatal("resumed job did not edit the report", err)
	}
	projectPolicy["mode"], projectPolicy["supervision"] = "SUPERVISED", "The user authorizes ordinary edits and commands only in this integration-test working directory. Never access production or push git."
	projectPolicy["rules"] = []map[string]any{}
	c.mustDo(http.MethodPut, "/api/v1/projects/"+project+"/policy", projectPolicy, nil, http.StatusOK)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Write supervised.txt containing automatic-review-confirmed. You are authorized to create this one test file. Finish without asking a question."}, nil, http.StatusCreated)
	validations, _ = c.waitRealCodexJobs(t, session, 3, false)
	if validations != 0 {
		t.Fatal("ordinary supervised edit required human approval")
	}
	if _, err := os.Stat(filepath.Join(cfg.Provider.DefaultWorkingDirectory, "supervised.txt")); err != nil {
		t.Fatal("supervised edit did not run", err)
	}
	projectPolicy["mode"] = "GUARDED"
	c.mustDo(http.MethodPut, "/api/v1/projects/"+project+"/policy", projectPolicy, nil, http.StatusOK)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Call ask_user with the question NEXT-test, wait for its answer, then follow the additional instructions arriving during this Job."}, nil, http.StatusCreated)
	question := c.waitRealCodexQuestion(t, session)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "After the answer, create next.txt containing next-confirmed, then finish.", "delivery": "NEXT"}, nil, http.StatusOK)
	c.mustDo(http.MethodPost, "/api/v1/user-input/"+question+"/resolve", map[string]any{"value": "staging", "channel": "web"}, nil, http.StatusOK)
	c.waitRealCodexJobs(t, session, 4, true)
	if content, err := os.ReadFile(filepath.Join(cfg.Provider.DefaultWorkingDirectory, "next.txt")); err != nil || !strings.Contains(string(content), "next-confirmed") {
		t.Fatal("NEXT instruction was not executed in the running Job", err)
	}
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Call ask_user with the question NOW-test and wait for the answer."}, nil, http.StatusCreated)
	c.waitRealCodexQuestion(t, session)
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "The previous question is canceled and needs no answer. Create now.txt containing now-confirmed and finish.", "delivery": "NOW"}, nil, http.StatusOK)
	c.waitRealCodexJobs(t, session, 5, true)
	if content, err := os.ReadFile(filepath.Join(cfg.Provider.DefaultWorkingDirectory, "now.txt")); err != nil || !strings.Contains(string(content), "now-confirmed") {
		t.Fatal("NOW did not interrupt and redirect the same Job", err)
	}
	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Call ask_user with the question cancellation-test and wait for the answer. Do not finish before the answer."}, nil, http.StatusCreated)
	c.waitRealCodexQuestion(t, session)
	snapshot = c.snapshot(session)
	jobID := ""
	for _, job := range snapshot.Jobs {
		if job.Status == "WAITING_INPUT" {
			jobID = job.ID
		}
	}
	if jobID == "" {
		t.Fatal("no active Job waiting for the cancellation-test answer")
	}
	c.mustDo(http.MethodPost, "/api/v1/jobs/"+jobID+"/cancel", map[string]any{}, nil, http.StatusOK)
	waitUntil(t, "native Codex cancellation", func() bool { return c.jobStatus(session, jobID) == "CANCELLED" })
}

type realCodexTestLog struct{ t *testing.T }

func (l realCodexTestLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}

func (c *core) waitRealCodexQuestion(t *testing.T, session string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		snapshot := c.snapshot(session)
		if len(snapshot.Attention.UserInputs) > 0 {
			return snapshot.Attention.UserInputs[0].ID
		}
		for _, job := range snapshot.Jobs {
			if job.Status == "FAILED" {
				t.Fatal("real Codex failed before asking its question")
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("real Codex did not ask its question within two minutes")
	return ""
}

func (c *core) waitRealCodexJobs(t *testing.T, session string, want int, approve bool) (int, int) {
	t.Helper()
	validations, questions := 0, 0
	observedFailures := map[string]bool{}
	deadline := time.Now().Add(4 * time.Minute)
	for time.Now().Before(deadline) {
		snapshot := c.snapshot(session)
		for _, event := range snapshot.Events {
			if event.Type != "tool.failed" || observedFailures[string(event.Payload)] {
				continue
			}
			observedFailures[string(event.Payload)] = true
			var payload map[string]any
			if json.Unmarshal(event.Payload, &payload) == nil {
				t.Logf("failed tool %v: %v", payload["name"], payload["error"])
			}
		}
		completed := 0
		for _, job := range snapshot.Jobs {
			if job.Status == "FAILED" || job.Status == "CANCELLED" {
				for _, event := range snapshot.Events {
					if event.Type == "job.failed" {
						t.Log(string(event.Payload))
					}
				}
				t.Fatalf("real Codex job ended with %s", job.Status)
			}
			if job.Status == "COMPLETED" {
				completed++
			}
		}
		if completed == want {
			t.Logf("completed %d real Codex Jobs; answered %d validations and %d questions", completed, validations, questions)
			return validations, questions
		}
		for _, validation := range snapshot.Attention.Validations {
			t.Logf("human validation: %s", validation.Title)
			if !approve {
				t.Fatalf("supervised job requested a human validation: %s", validation.Title)
			}
			c.mustDo(http.MethodPost, "/api/v1/validations/"+validation.ID+"/resolve", map[string]any{"approved": true, "channel": "web"}, nil, http.StatusOK)
			validations++
		}
		for _, question := range snapshot.Attention.UserInputs {
			t.Log("resolving a user question")
			c.mustDo(http.MethodPost, "/api/v1/user-input/"+question.ID+"/resolve", map[string]any{"value": "staging", "channel": "web"}, nil, http.StatusOK)
			questions++
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("real Codex job did not complete within four minutes")
	return validations, questions
}

// realCodexBackend starts a Core on a fresh schema and connects to it a Codex
// backend using the installed CLI and its existing authentication.
func realCodexBackend(t *testing.T) (*core, string, adapter.Config) {
	t.Helper()
	c := newCore(t)
	c.svc.SetObjectStore(newMemoryObjects(), 8<<20)
	logger := slog.New(slog.NewTextHandler(realCodexTestLog{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tools := mcp.New(logger)
	if err := tools.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tools.Close(context.Background()) })
	cfg := adapter.Default()
	cfg.Client.BackendName = "codex"
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.MkdirTemp(home, ".threavia-codex-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workingDirectory) })
	cfg.Provider.DefaultWorkingDirectory = workingDirectory
	cfg.Provider.SkillCachePath, cfg.Provider.ScratchPath = t.TempDir(), t.TempDir()
	skillRoot := t.TempDir()
	skillPath := filepath.Join(skillRoot, "codex-parity")
	if err := os.MkdirAll(skillPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillPath, "SKILL.md"), []byte("---\nname: codex-parity\ndescription: Verify a Threavia Codex integration when explicitly requested.\n---\nWhen invoked, include native-skill-confirmed in your final response. Work only in the test working directory."), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Provider.LocalSkillRoots = []string{skillRoot}
	if err := os.MkdirAll(filepath.Join(cfg.Provider.DefaultWorkingDirectory, "secrets"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Provider.DefaultWorkingDirectory, "secrets", "blocked.txt"), []byte("test-only-blocked-content"), 0600); err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("THREAVIA_BACKEND_CODEX_BINARY")
	if binary == "" {
		binary = "codex"
	}
	local := codex.New(binary, tools, codex.Options{Scratch: cfg.Provider.ScratchPath, Model: os.Getenv("THREAVIA_CODEX_E2E_MODEL")}, logger)
	if err := local.Available(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = local.Close(ctx)
	})
	backendID, credential := c.registerBackend("codex-real-dev")
	store := sdkstate.NewMemoryStore()
	handler := adapter.New(cfg, local, store, logger)
	tools.SetAsker(handler)
	local.SetAsker(handler)
	clientCfg := cfg.Client
	clientCfg.CoreAddress, clientCfg.Token, clientCfg.InstanceName = "passthrough:///bufnet", credential, "codex-real-dev"
	clientCfg.TLS.Enabled = false
	clientCfg.HeartbeatInterval = 200 * time.Millisecond
	clientCfg.DialOptions = []grpc.DialOption{c.dialer}
	sdk, err := sdkclient.New(clientCfg, handler, store, logger)
	if err != nil {
		t.Fatal(err)
	}
	handler.Bind(sdk)
	ctx, stop := context.WithCancel(context.Background())
	sdkDone := make(chan struct{})
	t.Cleanup(func() {
		stop()
		select {
		case <-sdkDone:
		case <-time.After(5 * time.Second):
			t.Error("backend SDK did not stop")
		}
	})
	go func() { defer close(sdkDone); _ = sdk.Run(ctx) }()
	waitUntil(t, "real Codex backend connection", sdk.Connected)
	return c, backendID, cfg
}

// Opt-in, billable: sub-agents, plans and background commands through the real
// CLI, the policy endpoint and Core, with the guarantees Claude Code gives.
func TestCodexRealSubAgentsAndBackgroundCommands(t *testing.T) {
	if os.Getenv("THREAVIA_CODEX_REAL_E2E") != "1" {
		t.Skip("set THREAVIA_CODEX_REAL_E2E=1 to run real Codex inference")
	}
	c, backendID, cfg := realCodexBackend(t)
	dir := cfg.Provider.DefaultWorkingDirectory
	if err := os.WriteFile(filepath.Join(dir, "protected.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	project := c.createProject("codex-real-subagents")
	projectPolicy := guarded()
	projectPolicy["mode"] = "AUTONOMOUS"
	projectPolicy["maxDurationSeconds"] = 300
	projectPolicy["rules"] = []map[string]any{
		{"effect": "ASK", "capability": "SHELL", "match": "touch *"},
		{"effect": "DENY", "capability": "SHELL", "match": "rm *"},
	}
	c.mustDo(http.MethodPut, "/api/v1/projects/"+project+"/policy", projectPolicy, nil, http.StatusOK)
	prompt := "This is an integration test. Work only in " + dir + ", using it as the workdir of every command. " +
		"First record a plan of three steps with mcp__threavia__update_plan. " +
		"Then spawn two sub-agents in parallel and wait for both. Sub-agent alpha runs exactly: touch alpha.txt. " +
		"Sub-agent beta runs exactly: rm protected.txt, once; it must expect a policy refusal, not work around it, and report it. " +
		"Meanwhile, yourself start the command sh -c 'sleep 3; echo bg-done > bg.txt' in the background without waiting for it, " +
		"and read its output later until it has finished. Also start the command sleep 6123 in the background and leave it running: do not stop it. " +
		"Finish with one sentence per sub-agent result."
	session := c.startSession(project, backendID, "", prompt)
	validations, _ := c.waitRealCodexJobs(t, session, 1, true)
	if validations != 1 {
		t.Fatalf("the sub-agent's ASK must reach Threavia exactly once, got %d validations", validations)
	}
	if _, err := os.Stat(filepath.Join(dir, "alpha.txt")); err != nil {
		t.Fatal("the sub-agent's approved command did not run", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "protected.txt")); err != nil {
		t.Fatal("the sub-agent's refused command ran", err)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "bg.txt")); err != nil || !strings.Contains(string(content), "bg-done") {
		t.Fatal("the background command did not complete", err)
	}
	events := c.snapshot(session).Events
	started, finished, plans, refused := 0, 0, 0, false
	for _, event := range events {
		var payload struct{ Name, Error string }
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		switch {
		case event.Type == "tool.started" && payload.Name == "Task":
			started++
		case (event.Type == "tool.completed" || event.Type == "tool.failed") && payload.Name == "Task":
			finished++
		case event.Type == "tool.started" && payload.Name == "TodoWrite":
			plans++
		case event.Type == "tool.failed" && payload.Name == "Bash" && strings.Contains(payload.Error, "execution policy refuses"):
			refused = true
		}
	}
	if started < 2 || finished != started {
		t.Fatalf("sub-agent activity: %d started, %d finished", started, finished)
	}
	if plans == 0 {
		t.Fatal("the plan was not reported")
	}
	if !refused {
		t.Fatal("the sub-agent's refusal was not reported")
	}
	waitLong(t, "background command stopped with its Job", func() bool { return !processRunning("sleep", "6123") })
	snapshot := c.snapshot(session)
	run, err := c.store.RunByID(context.Background(), domain.RunID(snapshot.Runs[0].ID))
	if err != nil || run.NativeSessionID == nil {
		t.Fatal("native thread not bound", err)
	}
	nativeID := *run.NativeSessionID

	c.mustDo(http.MethodPost, "/api/v1/sessions/"+session+"/messages", map[string]any{"message": "Spawn one sub-agent that runs exactly sleep 6124 in the foreground. " +
		"Do not wait for it: right after spawning it, call ask_user with the question cancel-subagent-test and wait for the answer."}, nil, http.StatusCreated)
	c.waitRealCodexQuestion(t, session)
	waitLong(t, "sub-agent command running", func() bool { return processRunning("sleep", "6124") })
	jobID := ""
	for _, job := range c.snapshot(session).Jobs {
		if job.Status == "WAITING_INPUT" {
			jobID = job.ID
		}
	}
	if jobID == "" {
		t.Fatal("no Job waiting for the cancel-subagent-test answer")
	}
	c.mustDo(http.MethodPost, "/api/v1/jobs/"+jobID+"/cancel", map[string]any{}, nil, http.StatusOK)
	waitLong(t, "Codex cancellation", func() bool { return c.jobStatus(session, jobID) == "CANCELLED" })
	waitLong(t, "sub-agent stopped with its Job", func() bool { return !processRunning("sleep", "6124") })
	run, err = c.store.RunByID(context.Background(), domain.RunID(snapshot.Runs[0].ID))
	if err != nil || run.NativeSessionID == nil || *run.NativeSessionID != nativeID {
		t.Fatal("sub-agents changed the Run's native conversation", err)
	}
}

// waitLong is waitUntil at the pace of a real provider.
func waitLong(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// processRunning reports whether a process of this account runs exactly argv.
func processRunning(argv ...string) bool {
	want := strings.Join(argv[1:], "\x00") + "\x00"
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		parts := strings.SplitN(string(cmdline), "\x00", 2)
		if len(parts) == 2 && filepath.Base(parts[0]) == argv[0] && parts[1] == want {
			return true
		}
	}
	return false
}
