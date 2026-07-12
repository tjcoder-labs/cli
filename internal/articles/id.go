package articles

import (
	"crypto/rand"
	"encoding/hex"
)

type ulidGenerator struct{}

func (ulidGenerator) New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "art-fallback"
	}
	return "art-" + hex.EncodeToString(b[:])
}
