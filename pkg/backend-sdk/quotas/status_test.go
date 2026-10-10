package quotas

import (
	"math"
	"testing"
	"time"
)

func TestSnapshotPreservesZeroUnknownAndProviderThresholds(t *testing.T) {
	zero, threshold := 0.0, 0.91
	original := Status{Availability: "AVAILABLE", ObservedAt: time.Now().UTC(), Limits: []Limit{
		{ID: "primary", Label: "Five hours", PercentUsed: &zero, Thresholds: []Threshold{{Label: "Provider warning", Value: &threshold, Unit: "utilization"}}, Details: map[string]any{"tier": "custom"}},
		{ID: "weekly-model", Label: "Weekly model", Status: "provider-specific-status"},
	}}
	wire, err := original.Struct()
	if err != nil {
		t.Fatal(err)
	}
	actual, err := FromStruct(wire)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Limits[0].PercentUsed == nil || *actual.Limits[0].PercentUsed != 0 || actual.Limits[1].PercentUsed != nil {
		t.Fatal("zero and unknown were conflated")
	}
	if *actual.Limits[0].Thresholds[0].Value != threshold || actual.Limits[1].Status != "provider-specific-status" {
		t.Fatal("provider semantics were lost")
	}
}

func TestMalformedQuotaReportsAreRejected(t *testing.T) {
	invalid := math.Inf(1)
	for _, snapshot := range []Status{
		{Availability: "bogus", ObservedAt: time.Now()},
		{Availability: "AVAILABLE"},
		{Availability: "AVAILABLE", ObservedAt: time.Now(), Limits: []Limit{{ID: "duplicate", Label: "one"}, {ID: "duplicate", Label: "two"}}},
		{Availability: "AVAILABLE", ObservedAt: time.Now(), Limits: []Limit{{ID: "bad", Label: "infinite", PercentUsed: &invalid}}},
	} {
		if _, err := snapshot.Struct(); err == nil {
			t.Fatalf("accepted invalid snapshot: %+v", snapshot)
		}
	}
}
