package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/events"
)

func TestRecoveryOmitsOnlyConfirmedInitialInput(t *testing.T) {
	for _, initial := range []string{"Original task", ""} {
		t.Run(initial, func(t *testing.T) {
			message := func(text string) events.Envelope {
				raw, _ := json.Marshal(UserMessagePayload{Text: text})
				return events.Envelope{Type: events.TypeUserMessage, Payload: raw}
			}
			wait := events.Envelope{Type: events.TypeJobWaitingBackend, Payload: json.RawMessage(`{"inputReceipt":"receipt","usage":{"inputTokens":7}}`)}
			history := readProviderHistory([]events.Envelope{message(initial), wait, message("Later instruction"), wait})
			if !history.InputReceipts["receipt"] || history.Usage.InputTokens != 14 {
				t.Fatal(history)
			}
			for _, delivered := range []bool{false, true} {
				prompt := recoveryPrompt(history, true, delivered)
				if !strings.Contains(prompt, "Later instruction") || (initial != "" && strings.Contains(prompt, initial) == delivered) {
					t.Fatalf("delivered=%v: %s", delivered, prompt)
				}
			}
			if got := recoveryPrompt(history, false, true); initial != "" && !strings.Contains(got, initial) {
				t.Fatal("a new conversation lost its initial instructions", got)
			}
		})
	}
}
