package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAnUnbuiltBinarySaysSo pins that a binary with no client reports it rather
// than serving a blank page or a bare 404. A developer who ran `go build`
// without building the client should learn that from the page.
func TestAnUnbuiltBinarySaysSo(t *testing.T) {
	t.Parallel()

	if Built() {
		t.Skip("a client is built into this test binary, so there is no unbuilt state to check")
	}

	handler, err := Handler()
	if err != nil {
		t.Fatalf("building the handler: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "make web") {
		t.Fatalf("the page must say how to build the client: %q", recorder.Body.String())
	}
}

// TestAClientRouteReachesTheApplication pins that a reload deep in the client
// still loads it. The browser routes on history, so /sessions/<id> is a path
// this server has never heard of and must still answer with the application.
func TestAClientRouteReachesTheApplication(t *testing.T) {
	t.Parallel()

	if !Built() {
		t.Skip("no client is built into this test binary; run make web first")
	}

	handler, err := Handler()
	if err != nil {
		t.Fatalf("building the handler: %v", err)
	}

	for _, path := range []string{"/", "/sessions/3f8c1b2e-0000-4000-8000-000000000001", "/projects/x"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), "<div id=\"root\"") {
			t.Errorf("GET %s did not return the application document", path)
		}
	}
}

// TestTheServiceWorkerIsNeverCached pins what keeps an installed client
// current: the worker and the manifest keep their names across builds, so a
// cached copy would pin the previous release on every phone.
func TestTheServiceWorkerIsNeverCached(t *testing.T) {
	t.Parallel()

	if !Built() {
		t.Skip("no client is built into this test binary; run make web first")
	}
	if _, err := fs.Stat(assets, "ui/dist/sw.js"); err != nil {
		t.Skip("the embedded client predates the service worker; run make web first")
	}

	handler, err := Handler()
	if err != nil {
		t.Fatalf("building the handler: %v", err)
	}
	for path, contentType := range map[string]string{
		"/sw.js":                "javascript",
		"/manifest.webmanifest": "application/manifest+json",
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, recorder.Code)
		}
		if got := recorder.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s Cache-Control = %q, want no-cache", path, got)
		}
		if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, contentType) {
			t.Errorf("GET %s Content-Type = %q, want %s", path, got, contentType)
		}
	}
}
