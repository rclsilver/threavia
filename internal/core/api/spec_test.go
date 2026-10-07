package api

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	contract "github.com/rclsilver/threavia/api"
)

// document is the part of the OpenAPI file these tests read.
//
// The path item holds both operations and a shared `parameters` list, so the
// value is untyped and the list is skipped where operations are collected.
type document struct {
	OpenAPI    string                    `yaml:"openapi"`
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas map[string]any `yaml:"schemas"`
	} `yaml:"components"`
}

func load(t *testing.T) document {
	t.Helper()

	var parsed document
	if err := yaml.Unmarshal(contract.OpenAPI, &parsed); err != nil {
		t.Fatalf("the api document does not parse: %v", err)
	}
	return parsed
}

// operations returns every "METHOD /path" the document describes, in the shape
// the Go router uses.
func (d document) operations() []string {
	var out []string
	for path, methods := range d.Paths {
		for method := range methods {
			if method == "parameters" {
				continue
			}
			out = append(out, strings.ToUpper(method)+" "+path)
		}
	}
	slices.Sort(out)
	return out
}

// registered returns every route the router actually serves.
func registered(t *testing.T) []string {
	t.Helper()

	h := &handler{}
	mux := http.NewServeMux()
	h.registerOperational(mux)
	h.registerSpec(mux)
	h.registerRegistration(mux)
	h.registerProjects(mux)
	h.registerBackends(mux)
	h.registerSessions(mux)
	h.registerJobs(mux)
	h.registerAttention(mux)
	h.registerKnowledge(mux)
	h.registerPolicy(mux)
	h.registerArtifacts(mux)
	h.registerSkills(mux)
	h.registerStream(mux)

	routes := h.Routes()
	slices.Sort(routes)
	return routes
}

// TestEveryRouteIsInTheOpenAPIDocument is what makes the contract a contract.
//
// It fails in both directions on purpose. A route the server serves and the
// document omits is invisible to every generated client, which is how an API
// quietly grows a surface nobody can use. A path the document describes and the
// server does not serve is a promise to callers that answers 501.
func TestEveryRouteIsInTheOpenAPIDocument(t *testing.T) {
	t.Parallel()

	described := load(t).operations()
	served := registered(t)

	for _, route := range served {
		if !slices.Contains(described, route) {
			t.Errorf("the server serves %q and the api document does not describe it", route)
		}
	}
	for _, route := range described {
		if !slices.Contains(served, route) {
			t.Errorf("the api document describes %q and the server does not serve it", route)
		}
	}
}

// TestTheDocumentIsServableAsJSON pins that what a code generator fetches is
// valid JSON, not a YAML file with the wrong content type.
func TestTheDocumentIsServableAsJSON(t *testing.T) {
	t.Parallel()

	encoded, err := specJSON()
	if err != nil {
		t.Fatalf("rendering the document as json: %v", err)
	}
	if !strings.HasPrefix(string(encoded), `{"components":`) && !strings.Contains(string(encoded), `"openapi":"3.1.0"`) {
		t.Fatalf("the rendered document does not look like the openapi object: %.120s", encoded)
	}
}

// TestTheDocumentDescribesTheDomain is a coarse guard against a schema being
// dropped: every type a client needs to render the product has to be there.
func TestTheDocumentDescribesTheDomain(t *testing.T) {
	t.Parallel()

	schemas := load(t).Components.Schemas
	for _, name := range []string{
		"Project", "Session", "Run", "Job", "Event", "Snapshot",
		"Attention", "ValidationRequest", "UserInputRequest",
		"ExecutionPolicy", "KnownDirectory", "KnownDirectoryBinding",
		"BackendInstance", "Condition", "Handoff",
		"Task", "Decision", "Artifact", "Skill", "BackendSkill", "AuditEntry",
	} {
		if _, ok := schemas[name]; !ok {
			t.Errorf("the api document has no %s schema", name)
		}
	}
}
