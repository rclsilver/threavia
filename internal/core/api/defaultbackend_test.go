package api_test

import (
	"testing"

	"github.com/rclsilver/threavia/internal/core/domain"
)

func TestDefaultBackendIsSharedByClientsAndIsolatedByAccount(t *testing.T) {
	c, _, _, _ := setup(t)
	secondID, _ := c.registerBackend("Second")
	set := func(id any, status int) {
		c.mustDo("PUT", "/api/v1/me/default-backend", map[string]any{"backendInstanceId": id}, nil, status)
	}
	read := func() string {
		var list struct {
			Items []domain.BackendInstance `json:"items"`
		}
		c.mustDo("GET", "/api/v1/backends", nil, &list, 200)
		found := ""
		for _, backend := range list.Items {
			if backend.IsDefault {
				if found != "" {
					t.Fatal("multiple defaults")
				}
				found = string(backend.ID)
			}
		}
		return found
	}
	set(c.backendID, 204)
	if read() != c.backendID {
		t.Fatal("default not persisted")
	}
	set(secondID, 204)
	if read() != secondID {
		t.Fatal("default not replaced")
	}
	stranger := c.asUser("stranger")
	stranger.mustDo("PUT", "/api/v1/me/default-backend", map[string]any{"backendInstanceId": secondID}, nil, 404)
	if read() != secondID {
		t.Fatal("foreign choice erased the default")
	}
	set("invalid", 400)
	c.mustDo("PUT", "/api/v1/me/default-backend", map[string]any{}, nil, 400)
	set(42, 400)
	if read() != secondID {
		t.Fatal("invalid choice erased the default")
	}
	set(nil, 204)
	if read() != "" {
		t.Fatal("default not cleared")
	}
	set(secondID, 204)
	c.mustDo("POST", "/api/v1/backends/"+secondID+"/revoke", nil, nil, 204)
	if read() != "" {
		t.Fatal("revoked backend retained as default")
	}
	set(secondID, 409)
}
