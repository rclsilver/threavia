package runner

import (
	"testing"

	"github.com/rclsilver/threavia/pkg/backend-sdk/resume"
)

func TestInitialInputReceiptRequiresThisThreadsAcknowledgedTurn(t *testing.T) {
	live := &execution{threadID: "root", inputText: "original with attachment", inputImages: inputImagesFingerprint(nil)}
	echo := func(thread, turn, text string) map[string]any {
		return map[string]any{"threadId": thread, "turnId": turn, "item": map[string]any{"type": "userMessage", "content": []any{map[string]any{"type": "text", "text": text}}}}
	}
	// Even an accepted turn can fail before user input has been inserted.
	live.inputTurnID = "turn"
	if got := live.inputReceipt("job"); got != "" {
		t.Fatal(got)
	}
	live.observeInitialInput("item/started", echo("root", "turn", live.inputText))
	live.observeInitialInput("item/completed", echo("child", "turn", live.inputText))
	live.observeInitialInput("item/completed", echo("root", "old", live.inputText))
	live.observeInitialInput("item/completed", echo("root", "turn", "other input"))
	if got := live.inputReceipt("job"); got != "" {
		t.Fatal("an unrelated/unconfirmed input became a receipt", got)
	}
	// The echo may arrive before turn/start returns its identifier.
	live.inputTurnID = ""
	live.observeInitialInput("item/completed", echo("root", "first", live.inputText))
	live.inputTurnID = "first"
	if got := live.inputReceipt("job"); got != resume.InputReceipt("job", "root") {
		t.Fatal(got)
	}
}
