package api

import (
	"net/http"
	"reflect"
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
	h.registerSchedules(mux)
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

	encoded, err := newSpecDocument("test").json()
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

// TestTheServedDocumentCarriesTheBuildVersion covers the one thing the file in
// the repository cannot say: which build is answering. A number written in the
// file would be a line someone has to remember to change on release day.
func TestTheServedDocumentCarriesTheBuildVersion(t *testing.T) {
	t.Parallel()

	spec := newSpecDocument("0.4.2")

	for name, render := range map[string]func() ([]byte, error){
		"yaml": spec.yaml,
		"json": spec.json,
	} {
		rendered, err := render()
		if err != nil {
			t.Fatalf("rendering the %s document: %v", name, err)
		}

		var parsed struct {
			Info struct {
				Title   string `yaml:"title" json:"title"`
				Version string `yaml:"version" json:"version"`
			} `yaml:"info" json:"info"`
		}
		// YAML is a superset of JSON, so one parser reads both renderings.
		if err := yaml.Unmarshal(rendered, &parsed); err != nil {
			t.Fatalf("the %s document does not parse: %v", name, err)
		}
		if parsed.Info.Version != "0.4.2" {
			t.Errorf("%s info.version = %q, want the build version", name, parsed.Info.Version)
		}
		if parsed.Info.Title != "Threavia Core" {
			t.Errorf("%s info.title = %q, want the document to be otherwise untouched",
				name, parsed.Info.Title)
		}
	}
}

// TestTheServedDocumentIsStillTheOneWritten pins that filling the version in
// does not quietly rewrite the rest. The document is read by people as well as
// by generators, so its comments and its shape are part of it.
func TestTheServedDocumentIsStillTheOneWritten(t *testing.T) {
	t.Parallel()

	rendered, err := newSpecDocument("0.4.2").yaml()
	if err != nil {
		t.Fatalf("rendering the document: %v", err)
	}

	// The whole document, compared as data: one field differs and nothing else
	// does. Comparing the bytes instead would pin the encoder's spacing, which
	// is not a promise this makes.
	var source, served map[string]any
	if err := yaml.Unmarshal(contract.OpenAPI, &source); err != nil {
		t.Fatalf("the source document does not parse: %v", err)
	}
	if err := yaml.Unmarshal(rendered, &served); err != nil {
		t.Fatalf("the served document does not parse: %v", err)
	}
	source["info"].(map[string]any)["version"] = "0.4.2"
	if !reflect.DeepEqual(source, served) {
		t.Error("the served document differs from the one in the repository by more than its version")
	}

	// And it is still the document as written: a comment only survives a
	// round trip through the node tree, and the comments are half of why this
	// file is readable.
	if !strings.Contains(string(rendered), "Current state, never a count of unread events") {
		t.Error("the served document lost the comments the source carries")
	}
}
