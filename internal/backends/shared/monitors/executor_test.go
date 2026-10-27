package monitors

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/rclsilver/threavia/pkg/monitor"
)

type pendingExecutor struct{ entered chan struct{} }

func (*pendingExecutor) Available() error { return nil }
func (e *pendingExecutor) Start(ctx context.Context, _ monitor.Snapshot, _ string, _, _ bool, _, _ io.Writer) (Process, error) {
	close(e.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestRemoteMonitorCanStopWhileSchedulingWithoutWakingModel(t *testing.T) {
	e := &pendingExecutor{entered: make(chan struct{})}
	m := NewWithExecutor(filepath.Join(t.TempDir(), "monitors.json"), e)
	s := snapshot("pending")
	done := make(chan error, 1)
	go func() { done <- m.Start(s, "/workspace", false, false) }()
	select {
	case <-e.entered:
	case <-time.After(time.Second):
		t.Fatal("launcher not entered")
	}
	values, err := m.Snapshots()
	if err != nil || len(values) != 1 || values[0].State != "STARTING" {
		t.Fatal("pending state not visible", err)
	}
	if err = m.Stop(s.ID, s.RunID); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("cancelled acquisition succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Stop blocked behind scheduling")
	}
	values, err = m.Snapshots()
	if err != nil || len(values) != 1 || values[0].State != "STOPPED" || values[0].Notify {
		t.Fatalf("manual pending stop woke model: %+v %v", values, err)
	}
}
