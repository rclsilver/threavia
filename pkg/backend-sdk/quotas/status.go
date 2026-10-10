// Package quotas carries provider-independent quota snapshots. Values omitted
// by a provider stay absent; limits and thresholds are never assumed shared.
package quotas

import (
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/protobuf/types/known/structpb"
)

type Status struct {
	Availability string         `json:"availability"`
	ObservedAt   time.Time      `json:"observedAt"`
	Message      string         `json:"message,omitempty"`
	Limits       []Limit        `json:"limits"`
	Details      map[string]any `json:"details,omitempty"`
}

type Limit struct {
	ID            string         `json:"id"`
	Label         string         `json:"label"`
	Status        string         `json:"status,omitempty"`
	Used          *float64       `json:"used,omitempty"`
	Limit         *float64       `json:"limit,omitempty"`
	Remaining     *float64       `json:"remaining,omitempty"`
	Unit          string         `json:"unit,omitempty"`
	PercentUsed   *float64       `json:"percentUsed,omitempty"`
	WindowSeconds int64          `json:"windowSeconds,omitempty"`
	ResetsAt      *time.Time     `json:"resetsAt,omitempty"`
	Thresholds    []Threshold    `json:"thresholds,omitempty"`
	Details       map[string]any `json:"details,omitempty"`
}

type Threshold struct {
	Label  string   `json:"label"`
	Value  *float64 `json:"value,omitempty"`
	Unit   string   `json:"unit,omitempty"`
	Status string   `json:"status,omitempty"`
}

func Unavailable(message string) Status {
	return Status{Availability: "UNAVAILABLE", ObservedAt: time.Now().UTC(), Message: message, Limits: []Limit{}}
}

// Struct validates a bounded, JSON-safe report before sending or storing it.
func (s Status) Struct() (*structpb.Struct, error) {
	if s.Availability != "AVAILABLE" && s.Availability != "UNAVAILABLE" && s.Availability != "UNSUPPORTED" {
		return nil, fmt.Errorf("invalid quota availability")
	}
	if s.ObservedAt.IsZero() || len(s.Limits) > 128 {
		return nil, fmt.Errorf("invalid quota snapshot")
	}
	seen := map[string]bool{}
	for _, limit := range s.Limits {
		if limit.ID == "" || limit.Label == "" || seen[limit.ID] || limit.WindowSeconds < 0 ||
			(limit.PercentUsed != nil && *limit.PercentUsed < 0) {
			return nil, fmt.Errorf("invalid quota limit")
		}
		seen[limit.ID] = true
	}
	if s.Limits == nil {
		s.Limits = []Limit{}
	}
	data, err := json.Marshal(s)
	if err != nil || len(data) > 128<<10 {
		return nil, fmt.Errorf("invalid or oversized quota snapshot")
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return structpb.NewStruct(value)
}

func FromStruct(value *structpb.Struct) (Status, error) {
	var snapshot Status
	if value == nil {
		return snapshot, fmt.Errorf("missing quota snapshot")
	}
	data, err := json.Marshal(value.AsMap())
	if err != nil || len(data) > 128<<10 {
		return snapshot, fmt.Errorf("invalid quota snapshot")
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return snapshot, err
	}
	_, err = snapshot.Struct()
	return snapshot, err
}
