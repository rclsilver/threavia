package adapter

import (
	"context"
	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
	"github.com/rclsilver/threavia/internal/backends/shared/policy"
	"github.com/rclsilver/threavia/pkg/mcpconfig"
	"io"
	"log/slog"
	"testing"
)

func TestPreauthorizedMCPWritesKeepExplicitAskDenyAndNetworkRules(t *testing.T) {
	for _, mode := range []backendv1.ExecutionMode{backendv1.ExecutionMode_EXECUTION_MODE_INTERACTIVE, backendv1.ExecutionMode_EXECUTION_MODE_GUARDED, backendv1.ExecutionMode_EXECUTION_MODE_AUTONOMOUS} {
		t.Run(mode.String(), func(t *testing.T) {
			p := policy.Policy{Mode: mode, AllowNetwork: true}
			a := &Adapter{waiters: map[string]*waiter{}, jobs: map[string]*jobState{"job": {policy: p, requests: map[string]struct{}{}}}, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			v := mcpconfig.Server{Name: "dev", Transport: "http", Tools: []string{"create", "delete"}, WriteTools: []string{"create"}}
			calls := 0
			call := func() (any, error) { calls++; return "ok", nil }
			if _, err := a.callExternalMCP(context.Background(), "job", v, "create", map[string]any{}, call); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("preauthorized write was not called")
			}
			if _, err := a.callExternalMCP(context.Background(), "job", v, "delete", map[string]any{}, call); err == nil {
				t.Fatal("unselected write ran without approval")
			}
			for _, effect := range []backendv1.PermissionEffect{backendv1.PermissionEffect_PERMISSION_EFFECT_DENY, backendv1.PermissionEffect_PERMISSION_EFFECT_ASK} {
				a.jobs["job"].policy.Rules = []policy.Rule{{Effect: effect, Capability: backendv1.PermissionCapability_PERMISSION_CAPABILITY_TOOL, Match: "mcp__dev__create"}}
				if _, err := a.callExternalMCP(context.Background(), "job", v, "create", map[string]any{}, call); err == nil {
					t.Fatal("explicit policy was bypassed")
				}
			}
			a.jobs["job"].policy = p
			a.jobs["job"].policy.AllowNetwork = false
			if _, err := a.callExternalMCP(context.Background(), "job", v, "create", map[string]any{}, call); err == nil {
				t.Fatal("network prohibition was bypassed")
			}
			if calls != 1 {
				t.Fatalf("refused invocation reached server: %d", calls)
			}
		})
	}
}
