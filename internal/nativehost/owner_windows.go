//go:build windows

package nativehost

import "os"

func ownedByUser(info os.FileInfo) bool { return false }
