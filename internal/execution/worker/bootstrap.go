package worker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/rclsilver/threavia/internal/execution/transport"
)

// Bootstrap handles only worker mode, before any backend configuration or Core
// credentials are loaded. Hook modes still use their provider's normal entry.
func Bootstrap(version string) (bool, error) {
	if len(os.Args) < 2 {
		return false, nil
	}
	if os.Args[1] == "--kubernetes-monitor-command" {
		return true, monitorCommand(os.Args[2:])
	}
	if os.Args[1] != "--kubernetes-worker" {
		return false, nil
	}
	if len(os.Args) != 2 {
		return true, errors.New("worker mode takes no additional arguments")
	}
	if err := harden(); err != nil {
		return true, err
	}
	get := func(key string) string { return os.Getenv("THREAVIA_EXECUTION_" + key) }
	id, run, job, kind := get("ID"), get("RUN_ID"), get("JOB_ID"), get("KIND")
	if uuid.Validate(id) != nil || kind != "probe" && (uuid.Validate(run) != nil || uuid.Validate(job) != nil) {
		return true, errors.New("invalid worker identity")
	}
	if kind != "probe" && kind != "job" && kind != "monitor" {
		return true, errors.New("invalid worker kind")
	}
	deadline, err := time.Parse(time.RFC3339Nano, get("DEADLINE"))
	if err != nil || !deadline.After(time.Now()) {
		return true, errors.New("invalid worker deadline")
	}
	network, err := strconv.ParseBool(get("NETWORK"))
	if err != nil {
		return true, errors.New("invalid worker network permission")
	}
	t, err := transport.New(get("ENDPOINT"), get("CA"))
	if err != nil {
		return true, err
	}
	h := http.Header{}
	token, session, pod := get("TOKEN"), get("SESSION"), get("POD_UID")
	if len(token) < 32 || uuid.Validate(session) != nil || pod == "" {
		return true, errors.New("missing scoped worker credential")
	}
	h.Set("Authorization", "Bearer "+token)
	h.Set("X-Threavia-Pod-UID", pod)
	provider, binary, scratch := get("PROVIDER"), get("BINARY"), get("SCRATCH")
	var readable []string
	if err = json.Unmarshal([]byte(get("READABLE")), &readable); err != nil {
		return true, errors.New("invalid worker readable roots")
	}
	// The provider and its children never inherit attachment credentials.
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "THREAVIA_EXECUTION_") && name != "THREAVIA_EXECUTION_SCRATCH" {
			_ = os.Unsetenv(name)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	attach, stopAttach := context.WithTimeout(ctx, 2*time.Minute)
	defer stopAttach()
	var connErr error
	// Pod identity may be persisted just after the container starts. Retrying
	// attachment carries no command; the successful stream remains single-use.
	for {
		conn, err := t.Dial(attach, "/v1/workers/"+session+"/stream", h)
		if err == nil {
			defer conn.Close()
			logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
			if kind == "monitor" {
				return true, serveMonitor(ctx, conn, network, logger)
			}
			return true, Serve(ctx, conn, Options{Provider: provider, Binary: binary, Scratch: scratch, Readable: readable, Version: version, APIKey: os.Getenv("OPENAI_API_KEY"), RunID: run, JobID: job, Probe: kind == "probe"}, logger)
		}
		connErr = err
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-attach.Done():
			timer.Stop()
			return true, connErr
		case <-timer.C:
		}
	}
}
