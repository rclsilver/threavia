package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	backendv1 "github.com/rclsilver/threavia/gen/threavia/backend/v1"
)

// PrepareAttachments materializes user-supplied files in the Run's readable
// scratch directory. Images are also sent as native visual input by providers.
func PrepareAttachments(params *StartParams, scratch string) error {
	if len(params.Attachments) == 0 {
		return nil
	}
	if scratch == "" {
		return fmt.Errorf("attachments require a configured scratch directory")
	}
	total := 0
	if len(params.Attachments) > 8 {
		return fmt.Errorf("too many attachments")
	}
	for _, attachment := range params.Attachments {
		total += len(attachment.GetContent())
		if total > 3<<20 {
			return fmt.Errorf("attachments exceed 3 MiB")
		}
	}
	dir := filepath.Join(scratch, "attachments", params.JobID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	params.Prompt += "\n\nAttached files (user-provided content):"
	for index, attachment := range params.Attachments {
		name := filepath.Base(strings.ReplaceAll(attachment.GetFilename(), "\\", "/"))
		if name == "." || name == ".." || name == "" {
			name = "attachment"
		}
		name = fmt.Sprintf("%d-%s", index, name)
		path := filepath.Join(dir, name)
		if err := root.WriteFile(name, attachment.GetContent(), 0600); err != nil {
			return err
		}
		params.Prompt += "\n" + strconv.Quote(path) + " (" + attachment.GetMimeType() + ")"
	}
	return nil
}

func IsVisualAttachment(attachment *backendv1.PromptAttachment) bool {
	switch strings.ToLower(attachment.GetMimeType()) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
