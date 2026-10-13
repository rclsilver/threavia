package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

func TestAttachmentsStayInScratchAndKeepTheirBytes(t *testing.T) {
	scratch := t.TempDir()
	params := StartParams{JobID: "job", Prompt: "Inspect this", Attachments: []*backendv1.PromptAttachment{
		{Filename: "../../outside.png", MimeType: "image/png", Content: []byte{0, 1, 255}},
		{Filename: `C:\outside\voice.wav`, MimeType: "audio/wav", Content: []byte("audio")},
	}}
	if err := PrepareAttachments(&params, scratch); err != nil {
		t.Fatal(err)
	}
	for index, filename := range []string{"0-outside.png", "1-voice.wav"} {
		path := filepath.Join(scratch, "attachments", "job", filename)
		content, err := os.ReadFile(path)
		if err != nil || string(content) != string(params.Attachments[index].Content) {
			t.Fatalf("attachment bytes: %v, %v", content, err)
		}
		if !strings.Contains(params.Prompt, path) {
			t.Fatalf("prompt does not reference %s", path)
		}
	}
	if !IsVisualAttachment(params.Attachments[0]) || IsVisualAttachment(params.Attachments[1]) {
		t.Fatal("image classification")
	}
}

func TestAttachmentPreparationRefusesUnavailableScratchAndOversizedInput(t *testing.T) {
	params := StartParams{JobID: "job", Attachments: []*backendv1.PromptAttachment{{Filename: "image.png", Content: []byte("x")}}}
	if err := PrepareAttachments(&params, ""); err == nil {
		t.Fatal("missing scratch accepted")
	}
	params.Attachments[0].Content = make([]byte, (3<<20)+1)
	if err := PrepareAttachments(&params, t.TempDir()); err == nil {
		t.Fatal("oversized attachments accepted")
	}
}

func TestAttachmentsDoNotFollowLinksOutsideTheirDirectory(t *testing.T) {
	scratch := t.TempDir()
	dir := filepath.Join(scratch, "attachments", "job")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "0-image.png")); err != nil {
		t.Fatal(err)
	}
	params := StartParams{JobID: "job", Attachments: []*backendv1.PromptAttachment{{Filename: "image.png", Content: []byte("replace")}}}
	if err := PrepareAttachments(&params, scratch); err == nil {
		t.Fatal("attachment followed a link outside scratch")
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != "keep" {
		t.Fatalf("outside file changed: %q, %v", content, err)
	}
}
