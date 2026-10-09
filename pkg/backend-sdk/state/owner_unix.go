//go:build unix

package state

import (
	"os"
	"syscall"
)

// ownedByUs says whether the account this process runs as owns the file.
func ownedByUs(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
