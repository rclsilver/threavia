package api

import (
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
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(contract.OpenAPI)
	})

	h.open(mux, "GET /api/spec.json", func(w http.ResponseWriter, _ *http.Request) {
		document, err := specJSON()
		if err != nil {
			h.logger.Error("cannot render the api document", "error", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal error")
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = w.Write(document)
	})
}

// specJSON converts the embedded document once and remembers the result. It is
// the same bytes on every request, and the conversion is not free.
var specJSON = sync.OnceValues(func() ([]byte, error) {
	var document any
	if err := yaml.Unmarshal(contract.OpenAPI, &document); err != nil {
		return nil, fmt.Errorf("parse the api document: %w", err)
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("render the api document as json: %w", err)
	}
	return encoded, nil
})
