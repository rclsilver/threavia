package runner

import (
	"context"
	"testing"

	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
)

func TestNativeResumeFailuresOnlyInvalidateConfirmedLostThreads(t *testing.T) {
	for _, scenario := range []string{"resume-missing", "resume-network", "resume-auth"} {
		t.Run(scenario, func(t *testing.T) {
			c, params, sink := provider(t, scenario)
			params.NativeSessionID = "old"
			if err := c.Run(context.Background(), params, sink); err != nil {
				t.Fatal(err)
			}
			_, lost := resume.FailureReason(sink.code)
			if lost != (scenario == "resume-missing") {
				t.Fatalf("code=%s, detail=%s", sink.code, sink.summary)
			}
			if scenario == "resume-network" {
				if _, ok := resume.AttemptFailureReason(sink.code); !ok {
					t.Fatal("unclassified resume handshake failure", sink.code)
				}
			}
		})
	}
}
