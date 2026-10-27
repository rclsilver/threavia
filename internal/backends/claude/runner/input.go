package runner

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// A replay acknowledges the initial input only when its content includes the
// exact text and visual blocks handed to stdin. Ignore envelope metadata.
func acknowledgedInitialInput(raw []byte, params StartParams) bool {
	var actual, expected struct {
		Message struct {
			Content any `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(raw, &actual) != nil || actual.Message.Content == nil {
		return false
	}
	var sent bytes.Buffer
	if writeUserMessage(&sent, supervised(params.Policy, params.Prompt), params.Attachments...) != nil || json.Unmarshal(sent.Bytes(), &expected) != nil {
		return false
	}
	return reflect.DeepEqual(actual.Message.Content, expected.Message.Content)
}
