package runner

import (
	"context"
	"testing"
)

func TestSessionPreferencesOverrideBackendDefaultsOnStartAndResume(t *testing.T) {
	for _, scenario := range []string{"session-model", "session-model-resume"} {
		t.Run(scenario, func(t *testing.T) {
			c, p, sink := provider(t, scenario)
			p.Model = "session-model"
			p.Effort = "high"
			if scenario == "session-model-resume" {
				p.NativeSessionID = "thread-1"
			}
			if err := c.Run(context.Background(), p, sink); err != nil {
				t.Fatal(err)
			}
			if sink.code != "" {
				t.Fatalf("provider failed: %s", sink.code)
			}
		})
	}
}
