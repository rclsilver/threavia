package bridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

func TestBidirectionalCallsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a, b := net.Pipe()
	p := New(ctx, a, func(_ context.Context, method string, input json.RawMessage) (any, error) {
		if method != "question" {
			return nil, errors.New("not allowed")
		}
		return "approved", nil
	})
	defer p.Close()
	stopped := make(chan struct{})
	var q *Peer
	q = New(ctx, b, func(ctx context.Context, method string, input json.RawMessage) (any, error) {
		switch method {
		case "start":
			var v string
			if err := q.Call(ctx, "question", nil, &v); err != nil {
				return nil, err
			}
			return v, nil
		case "wait":
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		}
		return nil, errors.New("not allowed")
	})
	defer q.Close()
	var result string
	if err := p.Call(ctx, "start", nil, &result); err != nil || result != "approved" {
		t.Fatalf("callback failed: %v %q", err, result)
	}
	short, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- p.Call(short, "wait", nil, nil) }()
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Ensure a cancellation received after dispatch reaches the remote handler.
	c2, stop2 := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop2()
	_ = p.Call(c2, "wait", nil, nil)
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("remote request was not cancelled")
	}
	if err := p.Call(ctx, "start", nil, &result); err != nil {
		t.Fatalf("cancel broke other calls: %v", err)
	}
}
func TestCloseReleasesPendingCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, b := net.Pipe()
	p := New(ctx, a, nil)
	defer p.Close()
	entered := make(chan struct{})
	q := New(ctx, b, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	done := make(chan error, 1)
	go func() { done <- p.Call(ctx, "wait", nil, nil) }()
	<-entered
	_ = q.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed stream returned success")
		}
	case <-ctx.Done():
		t.Fatal("pending call stuck")
	}
}
func TestRejectsOversizedFrameBeforeAllocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	a, b := net.Pipe()
	defer b.Close()
	p := New(ctx, a, nil)
	defer p.Close()
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], MaxFrame+1)
	if _, err := b.Write(h[:]); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Done():
		if p.Err() == nil {
			t.Fatal("missing protocol error")
		}
	case <-ctx.Done():
		t.Fatal("oversized frame accepted")
	}
}

func TestStalledReaderDoesNotTrapCancellation(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	p := New(context.Background(), a, nil)
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Call(ctx, "run", "never read", nil) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled write remained blocked")
	}
	select {
	case <-p.Done():
	case <-time.After(time.Second):
		t.Fatal("partially delivered request was not fenced")
	}
}
