//go:build !windows

package nativehost

import (
	"os"
	"syscall"
)

func ownedByUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid())
}
