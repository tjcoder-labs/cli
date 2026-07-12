package tasks

import (
	"encoding/binary"
	"sync"
	"time"
)

// IDGenerator produces new task IDs. The default implementation is a
// monotonic ULID (26 chars, lexically sortable by creation time).
type IDGenerator interface {
	New() string
}

// DefaultIDGenerator is the package-level default. Safe for concurrent
// use.
var DefaultIDGenerator IDGenerator = &ulidGenerator{}

// NewID returns a freshly-generated task ID using the default
// generator. Provided as a convenience for callers that don't need to
// inject their own generator.
func NewID() string {
	return DefaultIDGenerator.New()
}

type ulidGenerator struct {
	mu        sync.Mutex
	lastTime  int64
	increment uint64
}

// New returns a 26-character ULID-style ID. The format is identical to
// the standard ULID (Crockford base32) so external tools that
// understand ULIDs will sort these correctly.
func (g *ulidGenerator) New() string {
	now := time.Now().UTC()
	ts := now.UnixMilli()

	g.mu.Lock()
	if ts <= g.lastTime {
		g.increment++
	} else {
		g.increment = 0
		g.lastTime = ts
	}
	inc := g.increment
	g.mu.Unlock()

	var b [16]byte
	binary.BigEndian.PutUint64(b[0:8], uint64(ts))
	binary.BigEndian.PutUint64(b[8:16], inc)
	return encodeULID(b[:])
}

const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func encodeULID(b []byte) string {
	out := make([]byte, 26)
	for i := 0; i < 13; i++ {
		src := i * 5
		// Consume 5-bit groups, big-endian, across the 16-byte input.
		var val uint32
		var bits uint
		bitCursor := uint(src * 8)
		for bits < 5 {
			byteIdx := bitCursor / 8
			bitOffset := bitCursor % 8
			if byteIdx >= uint(len(b)) {
				break
			}
			avail := uint(8 - bitOffset)
			take := avail
			if take > 5-bits {
				take = 5 - bits
			}
			mask := byte((1 << take) - 1)
			shift := uint(avail - take)
			val = (val << take) | uint32((b[byteIdx]>>shift)&mask)
			bits += take
			bitCursor += take
		}
		out[i] = ulidAlphabet[val&0x1F]
	}
	return string(out)
}

// String-form helper used in a couple of tests; not exported.
func _idToString(b []byte) string { return encodeULID(b) }
