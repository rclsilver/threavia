package api_test

import (
	"net/http"
	"testing"
)

// TestABackendTakesTheNameItConnectsWith pins that renaming a backend in its
// configuration renames it in Core, without registering it again, and that a
// name another of the owner's backends holds keeps the old one rather than
// refusing the connection.
func TestABackendTakesTheNameItConnectsWith(t *testing.T) {
	t.Parallel()

	c := newCore(t)
	laptopID, laptopCredential := c.registerBackend("laptop")
	desktopID, desktopCredential := c.registerBackend("desktop")

	name := func(id string) string {
		var instance struct {
			Name              string `json:"name"`
			OperationalStatus string `json:"operationalStatus"`
		}
		c.mustDo(http.MethodGet, "/api/v1/backends/"+id, nil, &instance, http.StatusOK)
		if instance.OperationalStatus == "OFFLINE" {
			return ""
		}
		return instance.Name
	}

	// The harness connects every backend as "test-backend".
	c.connectBackend(laptopCredential)
	waitUntil(t, "the laptop to take its configured name", func() bool {
		return name(laptopID) == "test-backend"
	})

	c.connectBackend(desktopCredential)
	waitUntil(t, "the desktop to connect", func() bool {
		return name(desktopID) != ""
	})
	if got := name(desktopID); got != "desktop" {
		t.Fatalf("desktop name = %q, want its registered one kept: the laptop holds the new one", got)
	}
}
