package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHookTransportFailsClosed(t *testing.T) {
	for _, tt := range []struct {
		name, input, body string
		status            int
		err               error
		blocked           bool
	}{
		{"allow", `{}`, `{}`, 200, nil, false},
		{"deny", `{}`, `{"decision":"block","reason":"refused"}`, 200, nil, true},
		{"disconnected", `{}`, "", 0, errors.New("connection refused"), true},
		{"ended job", `{}`, "not found", 404, nil, true},
		{"malformed response", `{}`, "invalid json", 200, nil, true},
		{"null response", `{}`, "null", 200, nil, true},
		{"malformed event", "invalid json", `{}`, 200, nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
				if tt.err != nil {
					return nil, tt.err
				}
				return &http.Response{StatusCode: tt.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tt.body))}, nil
			})}
			var out bytes.Buffer
			run(client, "http://127.0.0.1/policy/token", strings.NewReader(tt.input), &out)
			var result map[string]any
			if json.Unmarshal(out.Bytes(), &result) != nil || (result["decision"] == "block") != tt.blocked {
				t.Fatalf("unexpected hook result: %s", out.String())
			}
		})
	}
}
