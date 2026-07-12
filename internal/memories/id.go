package memories

import (
	"crypto/rand"
	"encoding/hex"
)

// ulidGenerator is a tiny, dependency-free ID generator. It returns
// 16 bytes of randomness encoded as a hex string, which is good
// enough for in-session memory IDs (we don't need monotonic
// ordering, just uniqueness within a session).
type ulidGenerator struct{}

func (ulidGenerator) New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail on a working OS; if it
		// does, fall back to a fixed placeholder so the caller
		// still gets a non-empty string.
		return "mem-fallback"
	}
	return "mem-" + hex.EncodeToString(b[:])
}
