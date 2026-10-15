package runner

import (
	"context"
	"encoding/json"
	"os/exec"
)

// Authenticated asks the installed CLI; credentials stay in its own account.
// A configured credential is not proof that the next provider request succeeds.
func (c *Claude) Authenticated(ctx context.Context) (bool, bool) {
	output, err := exec.CommandContext(ctx, c.binary, "auth", "status", "--json").Output()
	var status struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if json.Unmarshal(output, &status) != nil || status.LoggedIn == nil {
		return false, false
	}
	if err != nil && *status.LoggedIn {
		return false, false
	}
	return *status.LoggedIn, true
}
