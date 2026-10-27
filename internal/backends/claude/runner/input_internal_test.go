package runner

import (
	"bytes"
	"encoding/json"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

func TestInputAcknowledgementMustIncludeVisualContent(t *testing.T) {
	p := StartParams{Prompt: "image request", Attachments: []*backendv1.PromptAttachment{{MimeType: "image/png", Content: []byte{1, 2, 3}}}}
	var full bytes.Buffer
	if err := writeUserMessage(&full, supervised(p.Policy, p.Prompt), p.Attachments...); err != nil {
		t.Fatal(err)
	}
	if !acknowledgedInitialInput(full.Bytes(), p) {
		t.Fatal("complete input was not recognized")
	}
	var partial bytes.Buffer
	_ = writeUserMessage(&partial, supervised(p.Policy, p.Prompt))
	if acknowledgedInitialInput(partial.Bytes(), p) {
		t.Fatal("text-only acknowledgement discarded the image")
	}
	var data map[string]any
	_ = json.Unmarshal(full.Bytes(), &data)
	data["message"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"] = "another request"
	raw, _ := json.Marshal(data)
	if acknowledgedInitialInput(raw, p) {
		t.Fatal("another message acknowledged this input")
	}
}
