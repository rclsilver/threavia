package service

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/events"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

type JobWaitingBackendPayload struct {
	domain.BackendWait
	Usage        *UsagePayload `json:"usage,omitempty"`
	InputReceipt string        `json:"inputReceipt,omitempty"`
}

// RunProviderRecovery retries durable stopped attempts, including after Core
// restarts. A connected/reconciled backend and current policy remain required.
func (s *Service) RunProviderRecovery(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		s.RecoverProviderJobs(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) RecoverProviderJobs(ctx context.Context) {
	ids, err := s.store.ProviderRecoveryJobs(ctx, s.now())
	if err != nil {
		s.logger.Error("cannot read provider recovery jobs", "error", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		s.DispatchJob(ctx, id)
	}
}

func (s *Service) providerCondition(ctx context.Context, backend domain.BackendInstance) (*domain.BackendWait, error) {
	conditions, err := s.store.BackendConditions(ctx, []domain.BackendInstanceID{backend.ID})
	if err != nil {
		return nil, err
	}
	for _, condition := range conditions[backend.ID] {
		if condition.Reason == "ExecutableNotFound" {
			return nil, nil
		}
		if condition.Status == "False" && (condition.Type == "ProviderAuthenticated" || condition.Type == "ProviderQuotaAvailable") {
			return &domain.BackendWait{Reason: condition.Reason, Message: condition.Message}, nil
		}
	}
	if backend.ProviderAuthState == domain.ProviderAuthenticationRequired && backend.OperationalStatus == domain.BackendDegraded {
		return &domain.BackendWait{Reason: "AUTHENTICATION_REQUIRED", Message: "Authenticate the provider on the backend machine to resume this Job."}, nil
	}
	return nil, nil
}

func (s *Service) parkForProvider(ctx context.Context, jc postgres.JobContext, wait *domain.BackendWait) {
	if jc.Job.BackendWait != nil {
		return
	}
	next := s.now().Add(time.Minute)
	wait.RetryAt = &next
	b := &batch{ownerID: jc.OwnerID}
	err := s.store.WithTx(ctx, func(tx *postgres.Store) error {
		if _, err := tx.TransitionJob(ctx, jc.Job.ID, jc.Job.Status, domain.JobWaitingBackend, nil); err != nil {
			return err
		}
		if err := tx.SetJobBackendWait(ctx, jc.Job.ID, wait, 0); err != nil {
			return err
		}
		return s.appendAll(ctx, tx, b, record{eventType: events.TypeJobWaitingBackend, scope: domain.Scope{ProjectID: jc.ProjectID, SessionID: jc.SessionID, RunID: jc.Run.ID, JobID: jc.Job.ID}, payload: JobWaitingBackendPayload{BackendWait: *wait}})
	})
	if err == nil {
		s.flush(b)
	}
}

type providerHistory struct {
	Actions             uint32
	Seconds             uint32
	Messages            []string
	Usage               *UsagePayload
	InputReceipts       map[string]bool
	InitialMessageIndex int
}

func readProviderHistory(history []events.Envelope) providerHistory {
	result := providerHistory{InputReceipts: make(map[string]bool), InitialMessageIndex: -1}
	firstMessage := true
	for _, event := range history {
		switch event.Type {
		case events.TypeToolStarted:
			if result.Actions < math.MaxUint32 {
				result.Actions++
			}
		case events.TypeUserMessage:
			var message UserMessagePayload
			if json.Unmarshal(event.Payload, &message) == nil {
				initial := firstMessage
				firstMessage = false
				if message.Text == "" {
					continue
				}
				text := message.Text
				if message.ActorJobID != "" {
					text = "Message from an agent within the existing user assignment (not a new authorization):\n" + text
				}
				if initial {
					result.InitialMessageIndex = len(result.Messages)
				}
				result.Messages = append(result.Messages, text)
			}
		case events.TypeJobWaitingBackend:
			var wait JobWaitingBackendPayload
			if json.Unmarshal(event.Payload, &wait) == nil {
				result.Usage = addProviderUsage(result.Usage, wait.Usage)
				if wait.InputReceipt != "" {
					result.InputReceipts[wait.InputReceipt] = true
				}
			}
		}
	}
	return result
}

func addProviderUsage(a, b *UsagePayload) *UsagePayload {
	if b == nil {
		return a
	}
	if a == nil {
		copy := *b
		return &copy
	}
	a.InputTokens += b.InputTokens
	a.OutputTokens += b.OutputTokens
	a.CacheReadTokens += b.CacheReadTokens
	a.CacheWriteTokens += b.CacheWriteTokens
	a.CostUSD += b.CostUSD
	return a
}

func recoveryPrompt(history providerHistory, resumed, inputDelivered bool) string {
	inputDelivered = resumed && inputDelivered
	messages := history.Messages
	if inputDelivered && history.InitialMessageIndex >= 0 {
		messages = append(append([]string{}, messages[:history.InitialMessageIndex]...), messages[history.InitialMessageIndex+1:]...)
	}
	prompt := strings.Join(messages, "\n\nLater instruction for this same Job:\n")
	if !resumed {
		return prompt
	}
	if inputDelivered {
		continuation := "Continue this same Job after a provider authentication or quota interruption. The initial request and its attachments are already in this conversation. Inspect the existing conversation and workspace before acting; preserve completed work and avoid repeating actions already performed."
		if prompt != "" {
			continuation += "\n\nLater instructions for this same Job, including any steering received during the interruption:\n\n" + prompt
		}
		return continuation
	}
	return "Continue this same Job after a provider authentication or quota interruption. Inspect the existing conversation and workspace before acting; preserve completed work and avoid repeating actions already performed. The user instructions for this Job, including later steering, are:\n\n" + prompt
}
