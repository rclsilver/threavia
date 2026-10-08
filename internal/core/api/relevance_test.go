package api_test

import (
	"bufio"
	"context"
	"net/http"
	"testing"
	"time"

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
	return c.watchWith(map[string]string{"X-Threavia-Channel": originChannel})
}

// watchWith holds a live SSE connection opened with the given headers: a
// channel, a client instance, a presence.
func (c *core) watchWith(headers map[string]string) func() {
	c.t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.http.URL+"/api/v1/events", nil)
	if err != nil {
		cancel()
		c.t.Fatalf("building the stream request: %v", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

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

// TestTheAuditTrailNamesTheClient pins that a privileged change records where it
// came from, which is what makes the trail of section 23 answer "who, from
// where" rather than only "who".
func TestTheAuditTrailNamesTheClient(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	c.connectBackend(credential)

	session := c.startSessionFrom(project, backendID, "vscode", "deploy the chart")

	c.mustDo(http.MethodPut, "/api/v1/sessions/"+session+"/policy",
		map[string]any{"mode": "GUARDED", "allowGitPush": true, "maxActions": 20},
		nil, http.StatusOK, [2]string{"X-Threavia-Channel", "vscode"})

	var audit struct {
		Items []struct {
			Action    string    `json:"action"`
			Channel   string    `json:"channel"`
			CreatedAt time.Time `json:"createdAt"`
		} `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/me/audit", nil, &audit, http.StatusOK)

	if len(audit.Items) != 1 || audit.Items[0].Action != "execution_policy.set" {
		t.Fatalf("audit = %+v, want the policy change", audit.Items)
	}
	if audit.Items[0].Channel != "vscode" {
		t.Fatalf("channel = %q, want the client that made the change", audit.Items[0].Channel)
	}
	// A timestamp a client can parse, which a bespoke SQL format was not.
	if audit.Items[0].CreatedAt.IsZero() {
		t.Fatal("the entry must carry a readable timestamp")
	}
}

// TestABackendExplainsItsState pins specification section 7: the operational
// status says a backend is degraded, and a condition says which thing is wrong.
// Before this, the only place that answer existed was a line in the Core log.
func TestABackendExplainsItsState(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	backendID, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	if err := backend.sdk.SendStatus(context.Background(),
		backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_DEGRADED,
		backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATION_REQUIRED,
		&backendv1.Condition{
			Type:    "ProviderAvailable",
			Status:  "False",
			Reason:  "ExecutableNotFound",
			Message: `"claude" not found`,
		}); err != nil {
		t.Fatalf("reporting the status: %v", err)
	}

	var instance struct {
		OperationalStatus string `json:"operationalStatus"`
		Conditions        []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
	}
	waitUntil(t, "the condition to be recorded", func() bool {
		c.mustDo(http.MethodGet, "/api/v1/backends/"+backendID, nil, &instance, http.StatusOK)
		return len(instance.Conditions) == 1
	})

	if instance.OperationalStatus != "DEGRADED" {
		t.Fatalf("status = %q, want DEGRADED", instance.OperationalStatus)
	}
	if instance.Conditions[0].Reason != "ExecutableNotFound" {
		t.Fatalf("condition = %+v, want the reported reason", instance.Conditions[0])
	}

	// A resolved problem stops being shown: this is current state, not a log.
	if err := backend.sdk.SendStatus(context.Background(),
		backendv1.BackendOperationalStatus_BACKEND_OPERATIONAL_STATUS_READY,
		backendv1.ProviderAuthState_PROVIDER_AUTH_STATE_AUTHENTICATED); err != nil {
		t.Fatalf("reporting the recovery: %v", err)
	}
	waitUntil(t, "the condition to clear", func() bool {
		// A fresh value each time: an absent JSON field leaves the previous one
		// in place, which would make this poll never see the change.
		var recovered struct {
			Conditions []struct {
				Type string `json:"type"`
			} `json:"conditions"`
		}
		c.mustDo(http.MethodGet, "/api/v1/backends/"+backendID, nil, &recovered, http.StatusOK)
		return len(recovered.Conditions) == 0
	})
}

// TestUsageReachesTheTimeline pins the other end of the accounting: what a
// backend reported about a Job has to survive into the event a client renders,
// or the timeline can only ever say that something finished.
func TestUsageReachesTheTimeline(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	project := c.createProject("homelab")
	backendID, credential := c.registerBackend("laptop")
	backend := c.connectBackend(credential)

	session := c.startSessionFrom(project, backendID, "web", "deploy the chart")
	start := receive(t, "the job to start", backend.starts)

	ctx := context.Background()
	backend.emit(t, ctx, backend.mustEvent(t, ctx, func() (*backendv1.JobEvent, error) {
		return backend.events.JobCompleted(ctx, start.GetRunId(), start.GetJobId(), "done",
			&backendv1.Usage{
				InputTokens:      2,
				OutputTokens:     90,
				CacheReadTokens:  23357,
				CacheWriteTokens: 305,
				CostUsd:          0.1018,
			})
	}))

	waitUntil(t, "the job to finish", func() bool {
		return c.jobStatus(session, start.GetJobId()) == "COMPLETED"
	})

	var history struct {
		Items []struct {
			Type    string `json:"type"`
			Payload struct {
				Usage *struct {
					InputTokens     uint64  `json:"inputTokens"`
					OutputTokens    uint64  `json:"outputTokens"`
					CacheReadTokens uint64  `json:"cacheReadTokens"`
					CostUSD         float64 `json:"costUsd"`
				} `json:"usage"`
			} `json:"payload"`
		} `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/sessions/"+session+"/events", nil, &history, http.StatusOK)

	for _, event := range history.Items {
		if event.Type != "job.completed" {
			continue
		}
		usage := event.Payload.Usage
		if usage == nil {
			t.Fatal("the completed job carries no usage")
		}
		if usage.OutputTokens != 90 || usage.CacheReadTokens != 23357 {
			t.Fatalf("usage = %+v, want what the backend reported", usage)
		}
		if usage.CostUSD != 0.1018 {
			t.Fatalf("cost = %v, want 0.1018", usage.CostUSD)
		}
		return
	}
	t.Fatal("no job.completed event in the timeline")
}
