package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
)

func inputImagesFingerprint(content []any) string {
	var urls []string
	for _, raw := range content {
		part, _ := raw.(map[string]any)
		if str(part, "type") == "image" {
			urls = append(urls, str(part, "url"))
		}
	}
	raw, _ := json.Marshal(urls)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Observe the provider's user-message echo, including during startup failures.
// A successful turn/start alone does not prove that input survived compaction.
func (live *execution) observeInitialInput(method string, payload map[string]any) {
	if method != "item/completed" {
		return
	}
	item, _ := payload["item"].(map[string]any)
	if str(item, "type") != "userMessage" || str(payload, "turnId") == "" {
		return
	}
	live.mu.Lock()
	defer live.mu.Unlock()
	if str(payload, "threadId") != live.threadID || live.inputText == "" {
		return
	}
	if live.inputTurnID != "" && str(payload, "turnId") != live.inputTurnID {
		return
	}
	content, _ := item["content"].([]any)
	if inputImagesFingerprint(content) != live.inputImages {
		return
	}
	for _, raw := range content {
		part, _ := raw.(map[string]any)
		if str(part, "type") == "text" && str(part, "text") == live.inputText {
			if live.inputEchoTurns == nil {
				live.inputEchoTurns = make(map[string]bool)
			}
			live.inputEchoTurns[str(payload, "turnId")] = true
			return
		}
	}
}

func (live *execution) inputReceipt(jobID string) string {
	live.mu.Lock()
	defer live.mu.Unlock()
	if live.inputTurnID == "" || !live.inputEchoTurns[live.inputTurnID] {
		return ""
	}
	return resume.InputReceipt(jobID, live.threadID)
}
