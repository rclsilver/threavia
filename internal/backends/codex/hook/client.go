// Package hook is the backend binary's small Codex command-hook entry point.
package hook

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// Run emits an explicit blocking response on every transport or decoding
// failure. Codex treats ordinary hook errors as nonblocking, so errors must
// never escape this entry point as an ordinary exit code 1.
func Run(endpoint string, input io.Reader, output io.Writer) {
	run(http.DefaultClient, endpoint, input, output)
}

func run(client *http.Client, endpoint string, input io.Reader, output io.Writer) {
	deny := func() {
		_ = json.NewEncoder(output).Encode(map[string]any{"decision": "block", "reason": "Threavia could not obtain a policy decision."})
	}
	body, err := io.ReadAll(io.LimitReader(input, (8<<20)+1))
	if err != nil || len(body) > 8<<20 || !json.Valid(body) {
		deny()
		return
	}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		deny()
		return
	}
	defer resp.Body.Close()
	var result map[string]any
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result) != nil || result == nil {
		deny()
		return
	}
	_ = json.NewEncoder(output).Encode(result)
}
