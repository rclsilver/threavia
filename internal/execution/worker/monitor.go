package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync/atomic"
	"time"

	"github.com/rclsilver/threavia/internal/execution/bridge"
	"github.com/rclsilver/threavia/internal/execution/protocol"
)

func serveMonitor(ctx context.Context, conn io.ReadWriteCloser, network bool, _ *slog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	l := newLease()
	var started atomic.Bool
	ready := make(chan struct{})
	var peer *bridge.Peer
	peer = bridge.New(ctx, conn, func(callCtx context.Context, method string, raw json.RawMessage) (any, error) {
		if method == "lease" {
			return nil, l.renew(raw)
		}
		if method != "monitor" {
			return nil, errors.New("operation unavailable on monitor worker")
		}
		var input protocol.MonitorStart
		if json.Unmarshal(raw, &input) != nil || len(input.Command) == 0 || len(input.Command) > 16384 {
			return nil, errors.New("invalid monitor command")
		}
		if started.Swap(true) {
			return nil, errors.New("monitor already started")
		}
		select {
		case <-ready:
		case <-callCtx.Done():
			return nil, callCtx.Err()
		}
		binary, err := os.Executable()
		if err != nil {
			return nil, err
		}
		mode := "offline"
		if network {
			mode = "network"
		}
		cmd := exec.CommandContext(callCtx, binary, "--kubernetes-monitor-command", mode, input.Command)
		cmd.WaitDelay = 2 * time.Second
		cmd.Stdout = &monitorWriter{ctx: callCtx, peer: peer}
		cmd.Stderr = &monitorWriter{ctx: callCtx, peer: peer, stderr: true}
		// The helper inherits no connected descriptors. Its own seccomp filter
		// removes network access; the parent alone keeps the control connection.
		return nil, cmd.Run()
	})
	close(ready)
	defer peer.Close()
	go l.watch(ctx, peer)
	<-peer.Done()
	cancel()
	return peer.Err()
}

type monitorWriter struct {
	ctx    context.Context
	peer   *bridge.Peer
	stderr bool
}

func (w *monitorWriter) Write(raw []byte) (int, error) {
	total := 0
	for len(raw) > 0 {
		n := len(raw)
		if n > 32<<10 {
			n = 32 << 10
		}
		if err := w.peer.Call(w.ctx, "output", protocol.MonitorOutput{Data: raw[:n], Stderr: w.stderr}, nil); err != nil {
			return total, err
		}
		total += n
		raw = raw[n:]
	}
	return total, nil
}
