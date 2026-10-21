package runner

import (
	"context"
	"testing"
	"time"

	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
)

func TestNativeResumeWithLargeHistory(t *testing.T) {
	c, params, sink := provider(t, "resume-large")
	params.NativeSessionID = "thread-1"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Run(ctx, params, sink); err != nil {
		t.Fatal(err)
	}
	if sink.code != "" || sink.native != "thread-1" {
		t.Fatalf("large history could not resume: native=%s code=%s summary=%s", sink.native, sink.code, sink.summary)
	}
	for _, event := range sink.events {
		if event == "completed" {
			return
		}
	}
	t.Fatalf("resumed turn did not complete: %v", sink.events)
}

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
