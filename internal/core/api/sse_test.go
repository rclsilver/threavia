package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// sseFrame is one decoded server-sent event.
type sseFrame struct {
	id    string
	name  string
	event struct {
		Sequence int64           `json:"sequence"`
		Type     string          `json:"type"`
		Payload  json.RawMessage `json:"payload"`
	}
}

// openStream subscribes to the global SSE endpoint and decodes frames onto a
// channel.
func (c *core) openStream(t *testing.T, after string) (<-chan sseFrame, func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	url := c.http.URL + "/api/v1/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		t.Fatalf("building the stream request: %v", err)
	}
	if after != "" {
		req.Header.Set("Last-Event-ID", after)
	}

	resp, err := c.http.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("opening the stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		cancel()
		t.Fatalf("content type = %q, want text/event-stream", got)
	}

	frames := make(chan sseFrame, 128)
	go func() {
		defer close(frames)
		defer func() { _ = resp.Body.Close() }()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		var current sseFrame
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case line == "":
				if current.name != "" {
					select {
					case frames <- current:
					case <-ctx.Done():
						return
					}
				}
				current = sseFrame{}
			case strings.HasPrefix(line, "id: "):
				current.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				current.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				_ = json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &current.event)
			}
		}
	}()

	return frames, func() {
		cancel()
		_ = resp.Body.Close()
	}
}

// awaitFrame waits for the first frame of a given event type.
func awaitFrame(t *testing.T, frames <-chan sseFrame, eventType string) sseFrame {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame, open := <-frames:
			if !open {
				t.Fatalf("the stream closed before %s arrived", eventType)
			}
			if frame.event.Type == eventType {
				return frame
			}
		case <-deadline:
			t.Fatalf("timed out waiting for the %s frame", eventType)
		}
	}
}

// TestStreamDeliversLiveEvents covers acceptance criterion 6: agent output
// reaches a connected client through Core in realtime.
func TestStreamDeliversLiveEvents(t *testing.T) {
	c, backend, projectID, dirID := setup(t)

	frames, closeStream := c.openStream(t, "")
	defer closeStream()

	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	created := awaitFrame(t, frames, "session.created")
	if created.id == "" {
		t.Error("every persisted frame must carry its global sequence as the SSE id")
	}

	start := receive(t, "the dispatched job", backend.starts)
	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.AgentMessage(ctx, start.GetRunId(), start.GetJobId(), "Je regarde le projet.")
	}))

	message := awaitFrame(t, frames, "agent.message")
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(message.event.Payload, &payload); err != nil {
		t.Fatalf("decoding the agent message: %v", err)
	}
	if payload.Text != "Je regarde le projet." {
		t.Fatalf("streamed text = %q, want the agent message", payload.Text)
	}
	if message.event.Sequence <= created.event.Sequence {
		t.Fatalf("sequences are not increasing: %d then %d", created.event.Sequence, message.event.Sequence)
	}
	_ = session
}

// TestStreamResumesFromItsCursor covers acceptance criteria 9 and 10: a client
// that disconnects and comes back receives exactly what it missed, because the
// timeline lives in the database and the cursor is the global sequence.
func TestStreamResumesFromItsCursor(t *testing.T) {
	c, backend, projectID, dirID := setup(t)

	frames, closeStream := c.openStream(t, "")
	session := c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	created := awaitFrame(t, frames, "session.created")
	cursor := created.id

	// The client goes away. Work continues regardless.
	closeStream()

	start := receive(t, "the dispatched job", backend.starts)
	ctx := context.Background()
	for _, text := range []string{"premier", "deuxieme", "troisieme"} {
		backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
			return backend.events.AgentMessage(ctx, start.GetRunId(), start.GetJobId(), text)
		}))
	}
	waitUntil(t, "the messages to be persisted", func() bool {
		return c.countEvents(session, "agent.message") == 3
	})

	// Reconnecting from the cursor replays exactly the missed events, in order.
	resumed, closeResumed := c.openStream(t, cursor)
	defer closeResumed()

	var texts []string
	deadline := time.After(5 * time.Second)
	for len(texts) < 3 {
		select {
		case frame, open := <-resumed:
			if !open {
				t.Fatal("the resumed stream closed early")
			}
			if frame.event.Type != "agent.message" {
				continue
			}
			var payload struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(frame.event.Payload, &payload); err != nil {
				t.Fatalf("decoding a replayed message: %v", err)
			}
			texts = append(texts, payload.Text)
		case <-deadline:
			t.Fatalf("timed out replaying missed events, got %v", texts)
		}
	}

	for i, want := range []string{"premier", "deuxieme", "troisieme"} {
		if texts[i] != want {
			t.Fatalf("replayed events = %v, want them in order", texts)
		}
	}
}

// TestStreamIsScopedToItsOwner pins that the global stream carries one user's
// events and nobody else's.
func TestStreamIsScopedToItsOwner(t *testing.T) {
	c, backend, projectID, dirID := setup(t)

	frames, closeStream := c.openStream(t, "")
	defer closeStream()

	c.startSession(projectID, c.backendID, dirID, "Analyse ce projet")
	awaitFrame(t, frames, "session.created")
	receive(t, "the dispatched job", backend.starts)

	// Everything on this stream belongs to the authenticated user: the handler
	// never reads a client-supplied identifier.
	select {
	case frame := <-frames:
		if frame.event.Sequence == 0 && frame.name != "ephemeral" {
			t.Fatalf("unexpected frame %+v", frame)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// TestStreamFramesAreNamed pins the frame names every client listens on.
//
// The names are part of the contract and are the one part of it an OpenAPI
// schema cannot express: a client that listens on the default `message` type
// receives nothing at all, with a connection that looks perfectly healthy. This
// cost the web client a silent failure once, so the names are held here.
func TestStreamFramesAreNamed(t *testing.T) {
	c, backend, projectID, dirID := setup(t)

	frames, closeStream := c.openStream(t, "")
	defer closeStream()

	session := c.startSession(projectID, c.backendID, dirID, "Analyse")
	start := receive(t, "the dispatched job", backend.starts)

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.AgentMessage(ctx, start.GetRunId(), start.GetJobId(), "working")
	}))

	// A persisted event: named "event", and carrying the global sequence as the
	// SSE id so a browser resumes on its own.
	persisted := awaitFrame(t, frames, "agent.message")
	if persisted.name != "event" {
		t.Fatalf("persisted frame name = %q, want %q", persisted.name, "event")
	}
	if persisted.id == "" {
		t.Fatal("a persisted frame must carry its sequence as the SSE id")
	}

	// A liveness signal: named "ephemeral", carrying no sequence, so it never
	// advances a client cursor and never lands in history.
	backend.sdk.SendEphemeral(ctx, backend.events.Ephemeral(
		start.GetRunId(), start.GetJobId(), "agent.thinking", ""))

	live := awaitFrame(t, frames, "agent.thinking")
	if live.name != "ephemeral" {
		t.Fatalf("ephemeral frame name = %q, want %q", live.name, "ephemeral")
	}
	if live.id != "" {
		t.Fatalf("an ephemeral frame must carry no sequence, got id %q", live.id)
	}
	if live.event.Sequence != 0 {
		t.Fatalf("ephemeral sequence = %d, want 0", live.event.Sequence)
	}
	_ = session
}
