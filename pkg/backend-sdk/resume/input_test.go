package resume

import "testing"

func TestInputReceiptDoesNotCrossJobsOrConversations(t *testing.T) {
	got := InputReceipt("job", "native")
	if len(got) != 64 || got != InputReceipt("job", "native") || got == InputReceipt("other", "native") || got == InputReceipt("job", "fresh") || InputReceipt("job", "") != "" || InputReceipt("", "native") != "" {
		t.Fatal("input receipt lost its scope", got)
	}
}
