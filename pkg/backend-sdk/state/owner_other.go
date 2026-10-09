//go:build !unix

package state

import "os"

// ownedByUs cannot tell without a Unix owner, and a directory that cannot be
// shown to be ours is left as it is.
func ownedByUs(os.FileInfo) bool { return false }
