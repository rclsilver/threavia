package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"go.yaml.in/yaml/v3"

	contract "github.com/rclsilver/threavia/api"
)

// registerSpec serves the API contract.
//
// Unauthenticated on purpose: the document describes the shape of the API and
// carries nothing a caller could not learn by reading an error message. A
// client generator, a Swagger UI or a person looking for the right route should
// not need a credential to find out what exists.
func (h *handler) registerSpec(mux *http.ServeMux) {
	h.open(mux, "GET /api/spec.yaml", func(w http.ResponseWriter, _ *http.Request) {
		document, err := h.spec.yaml()
		if err != nil {
			h.logger.Error("cannot render the api document", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(document)
	})

	h.open(mux, "GET /api/spec.json", func(w http.ResponseWriter, _ *http.Request) {
		document, err := h.spec.json()
		if err != nil {
			h.logger.Error("cannot render the api document", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(document)
	})
}

// specDocument renders the contract in both formats, with the version of the
// running build written into it.
//
// The file in the repository carries no release number, because a number in a
// file is one someone has to remember to change and nobody does. The binary
// already knows which build it is — the linker wrote it in — and that is the
// only answer that cannot drift from what is actually serving.
//
// Rendering happens once per format and is remembered: the bytes are the same
// on every request, and parsing the document is not free.
type specDocument struct {
	version string
	asYAML  func() ([]byte, error)
	asJSON  func() ([]byte, error)
}

func newSpecDocument(version string) *specDocument {
	spec := &specDocument{version: version}
	spec.asYAML = sync.OnceValues(spec.renderYAML)
	spec.asJSON = sync.OnceValues(spec.renderJSON)
	return spec
}

func (s *specDocument) yaml() ([]byte, error) { return s.asYAML() }
func (s *specDocument) json() ([]byte, error) { return s.asJSON() }

func (s *specDocument) renderYAML() ([]byte, error) {
	root, err := s.parsed()
	if err != nil {
		return nil, err
	}

	// A node tree carries the comments and the flow style of the source, so what
	// is served is the document as written, with one scalar replaced. The
	// document is also read by people.
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		return nil, fmt.Errorf("render the api document as yaml: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("render the api document as yaml: %w", err)
	}
	return out.Bytes(), nil
}

func (s *specDocument) renderJSON() ([]byte, error) {
	root, err := s.parsed()
	if err != nil {
		return nil, err
	}

	var document any
	if err := root.Decode(&document); err != nil {
		return nil, fmt.Errorf("read the api document: %w", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("render the api document as json: %w", err)
	}
	return encoded, nil
}

// parsed is the embedded document with info.version set to this build.
func (s *specDocument) parsed() (*yaml.Node, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(contract.OpenAPI, &root); err != nil {
		return nil, fmt.Errorf("parse the api document: %w", err)
	}

	version := field(field(documentRoot(&root), "info"), "version")
	if version == nil {
		return nil, fmt.Errorf("the api document has no info.version to fill in")
	}
	version.Value = s.version
	// A version that looked like a number in the source would come back
	// unquoted and stop being a string.
	version.Tag = "!!str"
	version.Style = yaml.DoubleQuotedStyle

	return &root, nil
}

// documentRoot unwraps the document node a parse returns.
func documentRoot(node *yaml.Node) *yaml.Node {
	if node != nil && node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return node.Content[0]
	}
	return node
}

// field returns the value node of a mapping key, or nil.
func field(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}
