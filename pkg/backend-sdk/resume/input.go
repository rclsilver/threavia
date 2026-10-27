package resume

import (
	"crypto/sha256"
	"encoding/hex"
)

// InputReceipt identifies a Job's initial input in one native conversation.
// It is only reported after a provider acknowledges that input, never merely
// because a process or turn started. The native identifier stays private.
func InputReceipt(jobID, nativeSessionID string) string {
	if jobID == "" || nativeSessionID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(jobID + "\x00" + nativeSessionID))
	return hex.EncodeToString(sum[:])
}
