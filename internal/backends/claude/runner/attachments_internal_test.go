package runner

import (
	"bytes"
	"encoding/json"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

func TestUserImageUsesNativeVisualContent(t *testing.T) {
	var buffer bytes.Buffer
	err := writeUserMessage(&buffer, "Describe it", &backendv1.PromptAttachment{Filename: "image.png", MimeType: "image/png", Content: []byte{0, 1, 2}}, &backendv1.PromptAttachment{Filename: "audio.wav", MimeType: "audio/wav", Content: []byte("audio")})
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Message struct {
			Content []struct {
				Type   string
				Text   string
				Source struct {
					Type      string
					MediaType string `json:"media_type"`
					Data      string
				}
			}
		}
	}
	if err := json.Unmarshal(buffer.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	blocks := envelope.Message.Content
	if len(blocks) != 2 || blocks[0].Text != "Describe it" || blocks[1].Type != "image" || blocks[1].Source.MediaType != "image/png" || blocks[1].Source.Data != "AAEC" {
		t.Fatalf("native content = %s", buffer.String())
	}
}
