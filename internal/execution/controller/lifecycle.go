package controller

import "context"

func (c *Controller) background(fn func()) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() { defer c.wg.Done(); fn() }()
}

// Close revokes admission and closes every stream, then waits for journal
// operations owned by the controller. The next boot reconciles physical exits;
// shutdown never fabricates an exit confirmation or replays a command.
func (c *Controller) Close(ctx context.Context) error {
	c.mu.Lock()
	c.closed = true
	c.ready = false
	c.cancel()
	rooms := make([]*room, 0, len(c.rooms))
	for _, r := range c.rooms {
		rooms = append(rooms, r)
	}
	c.mu.Unlock()
	for _, r := range rooms {
		r.close()
	}
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
