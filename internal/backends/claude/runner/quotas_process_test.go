package runner_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestClaudeQuotaReaderDoesNotStartAModelTurn(t *testing.T) {
	binary, args := fakeClaude(t, `
case "$first" in *'"initialize"'*) ;; *) exit 9 ;; esac
echo '{"type":"control_response","response":{"subtype":"success","request_id":"quotas-init","response":{}}}'
IFS= read -r usage
case "$usage" in *'"get_usage"'*) ;; *) exit 9 ;; esac
echo '{"type":"control_response","response":{"subtype":"success","request_id":"quotas-usage","response":{"rate_limits_available":true,"rate_limits":{"limits":[{"kind":"weekly_scoped","group":"weekly","percent":2,"scope":{"model":{"display_name":"Fable"}}}]}}}}'
`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot := newRunner(t, binary).Quotas(ctx)
	if snapshot.Availability != "AVAILABLE" || len(snapshot.Limits) != 1 || snapshot.Limits[0].Label != "Fable" {
		t.Fatalf("invalid usage control: %+v", snapshot)
	}
	flags := strings.Join(readArgs(t, args), " ")
	if !strings.Contains(flags, "--no-session-persistence") || !strings.Contains(flags, "--strict-mcp-config") {
		t.Fatal("quota reader can persist a session or load MCP servers")
	}
}

func TestClaudeQuotaReaderHonoursCancellation(t *testing.T) {
	binary, _ := fakeClaude(t, `IFS= read -r waiting`)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	snapshot := newRunner(t, binary).Quotas(ctx)
	if snapshot.Availability != "UNAVAILABLE" || time.Since(started) > time.Second {
		t.Fatal("quota reader did not stop on cancellation")
	}
}
