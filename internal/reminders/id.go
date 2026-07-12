package reminders

import (
	"crypto/rand"
	"encoding/hex"
)

type ulidGenerator struct{}

func (ulidGenerator) New() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "rem-fallback"
	}
	return "rem-" + hex.EncodeToString(b[:])
}
