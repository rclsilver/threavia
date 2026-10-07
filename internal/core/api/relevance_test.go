package api_test

import (
	"bufio"
	"context"
	"net/http"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// attentionItem is the part of a pending item these tests care about.
type attentionItem struct {
	ID            string `json:"id"`
	OriginChannel string `json:"originChannel"`
	Notify        bool   `json:"notify"`
}

type attentionResponse struct {
	Validations []attentionItem `json:"validations"`
	UserInputs  []attentionItem `json:"userInputs"`
}

// startSessionFrom performs the first send declaring which kind of client is
// making it.
func (c *core) startSessionFrom(projectID, backendID, originChannel, message string) string {
	c.t.Helper()

	var started startSessionResponse
	c.mustDo(http.MethodPost, "/api/v1/sessions/start", map[string]any{
		"projectId":         projectID,
		"backendInstanceId": backendID,
		"message":           message,
	}, &started, http.StatusCreated, [2]string{"X-Threavia-Channel", originChannel})
	return started.Session.ID
}

// watchOn holds a live SSE connection on one channel, which is what makes
// that client "watching" as far as Core is concerned.
func (c *core) watchOn(originChannel string) func() {
	c.t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.http.URL+"/api/v1/events", nil)
	if err != nil {
		cancel()
		c.t.Fatalf("building the stream request: %v", err)
	}
	req.Header.Set("X-Threavia-Channel", originChannel)

	resp, err := c.http.Client().Do(req)
	if err != nil {
		cancel()
		c.t.Fatalf("opening the stream: %v", err)
	}

	// Drained in the background: an unread stream eventually blocks the writer,
	// and this test is about the connection existing, not about its content.
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
		}
		_ = resp.Body.Close()
	}()

	return func() {
		cancel()
		_ = resp.Body.Close()
	}
}

// TestAttentionCarriesTheOriginatingChannel pins specification section 6: Core
// remembers which client started a piece of work, so a notifier can tell work
// someone is following from work they are not.
func TestAttentionCarriesTheOriginatingChannel(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	c.startSessionFrom(project, backendID, "android", "deploy the chart")
	start := receive(t, "the start command", backend.starts)

	ctx := context.Background()
	payload, err := structpb.NewStruct(map[string]any{"path": "/srv/app/main.go"})
	if err != nil {
		t.Fatalf("building the payload: %v", err)
	}
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.ValidationRequested(ctx, start.GetRunId(), start.GetJobId(),
			&backendv1.ValidationRequested{
				RequestId: "req-1", Title: "Write a file", RequestPayload: payload,
			})
	}))

	var attention attentionResponse
	waitUntil(t, "the validation to be pending", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/me/attention", nil, &attention, http.StatusOK)
		return len(attention.Validations) == 1
	})

	if attention.Validations[0].OriginChannel != "android" {
		t.Fatalf("originChannel = %q, want the client that started the work",
			attention.Validations[0].OriginChannel)
	}
	// Nothing is streaming on that channel, so the item is worth a push.
	if !attention.Validations[0].Notify {
		t.Fatal("an absent origin client must be worth notifying")
	}
}

// TestAWatchingClientIsNotWorthNotifying pins the other half of section 6: the
// client that started the work is receiving the live events, so a notification
// would only repeat what it already shows.
func TestAWatchingClientIsNotWorthNotifying(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	session := c.startSessionFrom(project, backendID, "web", "deploy the chart")
	start := receive(t, "the start command", backend.starts)

	stop := c.watchOn("web")
	defer stop()
	waitUntil(t, "the stream to be registered", func() bool {
		return c.svc.Broker().Subscribers() > 0
	})

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, start.GetRunId(), start.GetJobId(),
			&backendv1.UserInputRequested{
				RequestId: "req-1", Prompt: "Which namespace?", FreeText: true,
			})
	}))

	var attention attentionResponse
	waitUntil(t, "the question to be pending", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/me/attention?sessionId="+session, nil, &attention, http.StatusOK)
		return len(attention.UserInputs) == 1
	})

	if attention.UserInputs[0].OriginChannel != "web" {
		t.Fatalf("originChannel = %q, want web", attention.UserInputs[0].OriginChannel)
	}
	if attention.UserInputs[0].Notify {
		t.Fatal("a client already watching must not be told to ring")
	}
}

// TestAnUnknownChannelBecomesTheGenericOne pins that a missing or malformed
// header never fails a command.
func TestAnUnknownChannelBecomesTheGenericOne(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	c.startSessionFrom(project, backendID, "a channel with spaces", "deploy the chart")
	start := receive(t, "the start command", backend.starts)

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, start.GetRunId(), start.GetJobId(),
			&backendv1.UserInputRequested{RequestId: "req-1", Prompt: "Which namespace?"})
	}))

	var attention attentionResponse
	waitUntil(t, "the question to be pending", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/me/attention", nil, &attention, http.StatusOK)
		return len(attention.UserInputs) == 1
	})

	if attention.UserInputs[0].OriginChannel != "api" {
		t.Fatalf("originChannel = %q, want the generic channel", attention.UserInputs[0].OriginChannel)
	}
}

// TestAFreeTextQuestionNeedsNoChoices is a regression test: a question with an
// empty choice list used to be rejected by the database, so the backend replayed
// the event forever and the user was never asked anything.
func TestAFreeTextQuestionNeedsNoChoices(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	session := c.startSessionFrom(project, backendID, "web", "deploy the chart")
	start := receive(t, "the start command", backend.starts)

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.UserInputRequested(ctx, start.GetRunId(), start.GetJobId(),
			&backendv1.UserInputRequested{
				RequestId: "req-1", Prompt: "Which namespace?", FreeText: true,
			})
	}))

	waitUntil(t, "the free-text question to be pending", func() bool {
		var attention attentionResponse
		c.mustDo(http.MethodGet, "/api/v1/me/attention?sessionId="+session, nil, &attention, http.StatusOK)
		return len(attention.UserInputs) == 1
	})
	waitUntil(t, "the job to wait for input", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "WAITING_INPUT"
	})
}
