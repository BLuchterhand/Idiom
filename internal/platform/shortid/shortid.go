package shortid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

var fallbackCounter atomic.Uint64

// New returns a compact process-local identifier. It prefers 8 bytes of
// crypto/rand entropy and falls back to a timestamp plus monotonic suffix if
// entropy is unavailable.
func New() string {
	return newWithReader(rand.Reader, time.Now)
}

func newWithReader(reader io.Reader, now func() time.Time) string {
	var buf [8]byte

	if _, err := io.ReadFull(reader, buf[:]); err != nil {
		if now == nil {
			now = time.Now
		}
		return fmt.Sprintf("ts%016x%04x", now().UnixNano(), fallbackCounter.Add(1)&0xffff)
	}

	return hex.EncodeToString(buf[:])
}
