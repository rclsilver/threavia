// Package bridge carries scoped backend/worker calls over an authenticated stream.
// It does not authenticate callers or select their permitted methods: the owner
// of each endpoint installs the handler for its one execution.
package bridge

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const MaxFrame = 64 << 20
const maxCalls = 32

var ErrClosed = errors.New("execution bridge closed")

type Handler func(context.Context, string, json.RawMessage) (any, error)
type frame struct {
	ID     string          `json:"id"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}
type outgoing struct {
	raw  []byte
	done chan error
}

type Peer struct {
	conn     io.ReadWriteCloser
	ctx      context.Context
	cancel   context.CancelFunc
	handle   Handler
	prefix   string
	next     atomic.Uint64
	writes   chan outgoing
	mu       sync.Mutex
	pending  map[string]chan frame
	active   map[string]context.CancelFunc
	terminal error
	done     chan struct{}
	slots    chan struct{}
	once     sync.Once
}

func New(ctx context.Context, conn io.ReadWriteCloser, handle Handler) *Peer {
	ctx, cancel := context.WithCancel(ctx)
	p := &Peer{conn: conn, ctx: ctx, cancel: cancel, handle: handle, prefix: uuid.NewString(), pending: map[string]chan frame{}, active: map[string]context.CancelFunc{}, done: make(chan struct{}), slots: make(chan struct{}, maxCalls), writes: make(chan outgoing, maxCalls*2)}
	go p.writer()
	go p.read()
	go func() {
		select {
		case <-ctx.Done():
			p.close(ctx.Err())
		case <-p.done:
		}
	}()
	return p
}
func (p *Peer) Done() <-chan struct{} { return p.done }
func (p *Peer) Err() error            { p.mu.Lock(); defer p.mu.Unlock(); return p.terminal }
func (p *Peer) Close() error          { p.close(ErrClosed); return nil }
func (p *Peer) close(err error) {
	p.once.Do(func() {
		p.mu.Lock()
		p.terminal = err
		p.mu.Unlock()
		p.cancel()
		if conn, ok := p.conn.(interface{ SetWriteDeadline(time.Time) error }); ok {
			_ = conn.SetWriteDeadline(time.Now())
		}
		_ = p.conn.Close()
		close(p.done)
	})
}

func (p *Peer) Call(ctx context.Context, method string, input, output any) error {
	if method == "" || method == "$cancel" {
		return errors.New("invalid execution method")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	id := fmt.Sprintf("%s-%d", p.prefix, p.next.Add(1))
	ch := make(chan frame, 1)
	p.mu.Lock()
	if p.terminal != nil {
		err = p.terminal
		p.mu.Unlock()
		return err
	}
	if len(p.pending) >= maxCalls {
		p.mu.Unlock()
		return errors.New("too many pending execution calls")
	}
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, id); p.mu.Unlock() }()
	if err = p.write(ctx, frame{ID: id, Method: method, Params: raw}); err != nil {
		return err
	}
	decode := func(f frame) error {
		if f.Error != "" {
			return errors.New(f.Error)
		}
		if output != nil {
			return json.Unmarshal(f.Result, output)
		}
		return nil
	}
	select {
	case f := <-ch:
		return decode(f)
	case <-ctx.Done():
		// The bounded writer owns all socket writes. A stalled reader must not
		// accumulate a goroutine for every cancelled request.
		p.enqueueCancel(id)
		return ctx.Err()
	case <-p.done:
		select {
		case f := <-ch:
			return decode(f)
		default:
			return p.Err()
		}
	}
}
func (p *Peer) write(ctx context.Context, f frame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(raw) > MaxFrame {
		return errors.New("execution frame exceeds 64 MiB")
	}
	item := outgoing{raw: raw, done: make(chan error, 1)}
	select {
	case p.writes <- item:
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return p.Err()
	}
	select {
	case err := <-item.done:
		return err
	case <-ctx.Done():
		// It is unknown whether a partial frame reached the peer. Fence the
		// stream rather than delivering this request after its cancellation.
		p.close(ctx.Err())
		return ctx.Err()
	case <-p.done:
		return p.Err()
	}
}
func (p *Peer) enqueueCancel(id string) {
	raw, _ := json.Marshal(frame{ID: id, Method: "$cancel"})
	select {
	case p.writes <- outgoing{raw: raw, done: make(chan error, 1)}:
	case <-p.done:
	default:
		p.close(errors.New("execution writer backpressure"))
	}
}
func (p *Peer) writer() {
	for {
		select {
		case <-p.done:
			return
		case item := <-p.writes:
			// Also bound response writes, which have no caller deadline. Close
			// unblocks Write on the supported network transports and net.Pipe.
			timer := time.AfterFunc(30*time.Second, func() { p.close(errors.New("execution write timeout")) })
			var header [4]byte
			binary.BigEndian.PutUint32(header[:], uint32(len(item.raw)))
			err := writeAll(p.conn, header[:])
			if err == nil {
				err = writeAll(p.conn, item.raw)
			}
			timer.Stop()
			item.done <- err
			if err != nil {
				p.close(err)
				return
			}
		}
	}
}
func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
func (p *Peer) read() {
	for {
		var h [4]byte
		if _, err := io.ReadFull(p.conn, h[:]); err != nil {
			p.close(err)
			return
		}
		n := binary.BigEndian.Uint32(h[:])
		if n == 0 || n > MaxFrame {
			p.close(errors.New("invalid execution frame size"))
			return
		}
		raw := make([]byte, int(n))
		if _, err := io.ReadFull(p.conn, raw); err != nil {
			p.close(err)
			return
		}
		var f frame
		if err := json.Unmarshal(raw, &f); err != nil || f.ID == "" {
			p.close(errors.New("invalid execution frame"))
			return
		}
		if f.Method == "" {
			p.mu.Lock()
			ch := p.pending[f.ID]
			p.mu.Unlock()
			if ch != nil {
				select {
				case ch <- f:
				default:
				}
			}
			continue
		}
		if f.Method == "$cancel" {
			p.mu.Lock()
			cancel := p.active[f.ID]
			p.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			continue
		}
		ctx, cancel := context.WithCancel(p.ctx)
		p.mu.Lock()
		_, duplicate := p.active[f.ID]
		if !duplicate {
			p.active[f.ID] = cancel
		}
		p.mu.Unlock()
		if duplicate {
			cancel()
			p.close(errors.New("duplicate execution request"))
			return
		}
		select {
		case p.slots <- struct{}{}:
			go p.dispatch(ctx, cancel, f)
		default:
			cancel()
			p.mu.Lock()
			delete(p.active, f.ID)
			p.mu.Unlock()
			p.close(errors.New("too many incoming execution calls"))
			return
		}
	}
}
func (p *Peer) dispatch(ctx context.Context, cancel context.CancelFunc, f frame) {
	defer func() {
		if recover() != nil {
			p.close(errors.New("execution handler failed"))
		}
	}()
	defer func() { cancel(); <-p.slots; p.mu.Lock(); delete(p.active, f.ID); p.mu.Unlock() }()
	result := frame{ID: f.ID}
	if p.handle == nil {
		result.Error = "execution method unavailable"
	} else {
		value, err := p.handle(ctx, f.Method, f.Params)
		if err == nil {
			result.Result, err = json.Marshal(value)
		}
		if err != nil {
			result.Error = err.Error()
		}
	}
	_ = p.write(p.ctx, result)
}
