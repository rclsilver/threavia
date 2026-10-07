package api_test

import (
	"net/http"
	"testing"
)

// decision is what the client reads back from the decision endpoints.
type decision struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Supersedes string `json:"supersedes"`
}

// decisions lists what a Project remembers, superseded ones included.
func (c *core) decisions(projectID string) []decision {
	c.t.Helper()
	var listed struct {
		Items []decision `json:"items"`
	}
	c.mustDo(http.MethodGet, "/api/v1/projects/"+projectID+"/decisions?includeSuperseded=true",
		nil, &listed, http.StatusOK)
	return listed.Items
}

// TestDeletingADecisionMakesWhatItReplacedCurrentAgain covers the one thing
// deleting project memory must not do: leave the Project with a record it no
// longer reads and nothing in its place.
func TestDeletingADecisionMakesWhatItReplacedCurrentAgain(t *testing.T) {
	c := newCore(t)
	projectID := c.createProject("homelab")

	var first decision
	c.mustDo(http.MethodPost, "/api/v1/projects/"+projectID+"/decisions",
		map[string]any{"title": "Deploy with Puppet", "importance": "IMPORTANT"},
		&first, http.StatusCreated)

	var second decision
	c.mustDo(http.MethodPost, "/api/v1/projects/"+projectID+"/decisions",
		map[string]any{"title": "Deploy with Helm", "supersedes": first.ID},
		&second, http.StatusCreated)

	superseded := c.decisions(projectID)
	if len(superseded) != 2 {
		t.Fatalf("%d decisions, want both", len(superseded))
	}
	for _, entry := range superseded {
		if entry.ID == first.ID && entry.Status != "SUPERSEDED" {
			t.Fatalf("the replaced decision is %s, want SUPERSEDED", entry.Status)
		}
	}

	// The replacement turns out to have been a mistake.
	c.mustDo(http.MethodDelete, "/api/v1/decisions/"+second.ID, nil, nil, http.StatusNoContent)

	left := c.decisions(projectID)
	if len(left) != 1 || left[0].ID != first.ID {
		t.Fatalf("decisions = %+v, want only the first", left)
	}
	if left[0].Status != "ACTIVE" {
		t.Fatalf("the replaced decision is %s, want it current again now that nothing replaces it",
			left[0].Status)
	}

	c.mustDo(http.MethodDelete, "/api/v1/decisions/"+second.ID, nil, nil, http.StatusNotFound)
}

// TestDeletingAReplacedDecisionLeavesTheReplacement pins the other direction:
// the newer Decision keeps its own record and simply stops pointing anywhere.
func TestDeletingAReplacedDecisionLeavesTheReplacement(t *testing.T) {
	c := newCore(t)
	projectID := c.createProject("homelab")

	var first, second decision
	c.mustDo(http.MethodPost, "/api/v1/projects/"+projectID+"/decisions",
		map[string]any{"title": "Deploy with Puppet"}, &first, http.StatusCreated)
	c.mustDo(http.MethodPost, "/api/v1/projects/"+projectID+"/decisions",
		map[string]any{"title": "Deploy with Helm", "supersedes": first.ID},
		&second, http.StatusCreated)

	c.mustDo(http.MethodDelete, "/api/v1/decisions/"+first.ID, nil, nil, http.StatusNoContent)

	left := c.decisions(projectID)
	if len(left) != 1 || left[0].ID != second.ID {
		t.Fatalf("decisions = %+v, want only the replacement", left)
	}
	if left[0].Status != "ACTIVE" {
		t.Fatalf("the replacement is %s, want ACTIVE", left[0].Status)
	}
	if left[0].Supersedes != "" {
		t.Fatalf("supersedes = %q, want nothing: what it replaced is gone", left[0].Supersedes)
	}
}
