package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/rclsilver/threavia/internal/backends/shared/monitors"
	"github.com/rclsilver/threavia/internal/execution/bridge"
	"github.com/rclsilver/threavia/internal/execution/kube"
	"github.com/rclsilver/threavia/internal/execution/protocol"
	"github.com/rclsilver/threavia/pkg/monitor"
)

type monitorProcess struct{ done chan error }

func (p *monitorProcess) Wait() error { return <-p.done }
func (c *Client) Start(ctx context.Context, s monitor.Snapshot, dir string, writable, network bool, stdout, stderr io.Writer) (monitors.Process, error) {
	start, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	session, err := c.open(start, kube.Request{ID: s.ID, JobID: s.JobID, RunID: s.RunID, Kind: "monitor", Command: s.Command, Directory: dir, Writable: writable, Network: network, ExpiresAt: s.ExpiresAt})
	if err != nil {
		return nil, err
	}
	p := &monitorProcess{done: make(chan error, 1)}
	peer := bridge.New(context.WithoutCancel(ctx), session, func(_ context.Context, method string, raw json.RawMessage) (any, error) {
		if method != "output" {
			return nil, errors.New("unknown monitor callback")
		}
		var output protocol.MonitorOutput
		if err := json.Unmarshal(raw, &output); err != nil {
			return nil, err
		}
		w := stdout
		if output.Stderr {
			w = stderr
		}
		_, err := w.Write(output.Data)
		return nil, err
	})
	go func() {
		defer peer.Close()
		defer session.Close()
		defer protocol.KeepAlive(context.WithoutCancel(ctx), peer)()
		err := peer.Call(ctx, "monitor", protocol.MonitorStart{Command: s.Command}, nil)
		for {
			stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			stopErr := session.Stop(stop)
			cancel()
			if stopErr == nil {
				p.done <- err
				return
			}
			if c.ctx.Err() != nil {
				p.done <- monitors.ErrStopUnconfirmed
				return
			}
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-c.ctx.Done():
				timer.Stop()
				p.done <- monitors.ErrStopUnconfirmed
				return
			case <-timer.C:
			}
		}
	}()
	return p, nil
}
