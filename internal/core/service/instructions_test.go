package service

import (
	"strings"
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
	"github.com/rclsilver/threavia/internal/core/storage/postgres"
)

// TestAssembledSupervisionDoesNotRankTheMessage pins that the reviewer is not
// told the message outranks the standing instructions: the backends prefix the
// supervision to every user message, so "anything appended" would be the
// message itself.
func TestAssembledSupervisionDoesNotRankTheMessage(t *testing.T) {
	t.Parallel()

	got := assembleInstructions(postgres.JobLayers{
		ProjectName: "p", BackendName: "b",
		Global:  domain.Instructions{Agent: "A1", Supervision: "S1"},
		Project: domain.Instructions{Agent: "A2", Supervision: "S2"},
	})
	if strings.Contains(got.Supervision, "Anything appended") || !strings.HasPrefix(got.Supervision, domain.LayeredSupervisionPreamble) {
		t.Errorf("supervision = %q, want the supervision preamble", got.Supervision)
	}
	if !strings.HasPrefix(got.Agent, domain.LayeredPreamble) {
		t.Errorf("agent = %q, want the agent preamble, which covers LOCAL_INSTRUCTIONS", got.Agent)
	}
}
