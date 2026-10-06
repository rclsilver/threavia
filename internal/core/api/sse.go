package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/rclsilver/threavia/internal/core/auth"
	"github.com/rclsilver/threavia/internal/core/domain"
)

// sseKeepalive bounds how long the stream stays silent. Comment frames keep
// proxies from closing an idle connection.
const sseKeepalive = 20 * time.Second

// catchUpPage is how many missed events are replayed per database round trip.
const catchUpPage = 200

func (h *handler) registerStream(mux *http.ServeMux) {
	h.handle(mux, "GET /api/v1/events", h.stream)
}

// stream is the global SSE endpoint of specification section 5.
//
// One stream per user carries every Session, so a client holds a single cursor
// rather than one per Session. Reconnection replays from that cursor, which is
// why a dropped connection never loses history: the database is the source of
// truth and the in-process broker is only the fast path.
func (h *handler) stream(w http.ResponseWriter, r *http.Request, identity auth.Identity) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming_unsupported", "streaming is not supported")
		return
	}

	cursor := resumeCursor(r)

	// Subscribing before the catch-up read is what closes the gap: anything
	// committed while we page through history is already buffered.
	sub, cancel := h.svc.Broker().Subscribe(identity.UserID)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	cursor, ok = h.catchUp(w, flusher, r, identity, cursor)
	if !ok {
		return
	}

	keepalive := time.NewTicker(sseKeepalive)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return

		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()

		case envelope, open := <-sub.Events():
			if !open {
				return
			}

			// An ephemeral signal carries no sequence: it is streamed for
			// liveness and never advances the cursor.
			if envelope.Sequence == 0 {
				if !writeEvent(w, flusher, "ephemeral", envelope) {
					return
				}
				continue
			}

			if sub.Lagged() {
				// Events were dropped for this client; the database is the
				// source of truth, so replay from the cursor instead.
				sub.ClearLagged()
				if cursor, ok = h.catchUp(w, flusher, r, identity, cursor); !ok {
					return
				}
				continue
			}

			if envelope.Sequence <= cursor {
				continue // Already delivered by the catch-up.
			}
			if !writeEvent(w, flusher, "event", envelope) {
				return
			}
			cursor = envelope.Sequence
		}
	}
}

// catchUp replays everything the client missed, in order, and returns the new
// cursor.
func (h *handler) catchUp(w http.ResponseWriter, flusher http.Flusher, r *http.Request, identity auth.Identity, cursor domain.Sequence) (domain.Sequence, bool) {
	for {
		missed, err := h.svc.EventsAfter(r.Context(), identity, cursor, catchUpPage)
		if err != nil {
			h.logger.Error("cannot replay missed events", "error", err)
			return cursor, false
		}
		for _, envelope := range missed {
			if !writeEvent(w, flusher, "event", envelope) {
				return cursor, false
			}
			cursor = envelope.Sequence
		}
		if len(missed) < catchUpPage {
			return cursor, true
		}
	}
}

// resumeCursor reads the client position, from the Last-Event-ID header a
// browser EventSource resends automatically, or from an explicit query
// parameter.
func resumeCursor(r *http.Request) domain.Sequence {
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value >= 0 {
			return domain.Sequence(value)
		}
	}
	return querySequence(r, "after")
}

// writeEvent emits one SSE frame. The id field is the global sequence, so a
// browser resumes exactly where it stopped.
func writeEvent(w http.ResponseWriter, flusher http.Flusher, name string, envelope any) bool {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return false
	}

	if sequenced, ok := envelope.(interface{ EventID() string }); ok {
		if _, err := fmt.Fprintf(w, "id: %s\n", sequenced.EventID()); err != nil {
			return false
		}
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, encoded); err != nil {
		return false
	}
	flusher.Flush()
	return true
}
