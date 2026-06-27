package shortid

import (
	"errors"
	"io"
	"regexp"
	"testing"
	"time"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestNewWithReaderReturnsEightByteHex(t *testing.T) {
	id := newWithReader(
		io.LimitReader(&repeatReader{value: 0xab}, 8),
		func() time.Time { return time.Unix(0, 1) },
	)

	if id != "abababababababab" {
		t.Fatalf("id = %q, want 8-byte hex", id)
	}
}

func TestNewWithReaderFallbackUsesTimestampShape(t *testing.T) {
	id := newWithReader(errReader{}, func() time.Time { return time.Unix(0, 0x1234) })
	if matched, err := regexp.MatchString(`^ts0000000000001234[0-9a-f]{4}$`, id); err != nil || !matched {
		t.Fatalf("fallback id = %q, want timestamp plus counter hex shape", id)
	}
}

func TestNewFormatAndLocalUniqueness(t *testing.T) {
	pattern := regexp.MustCompile(`^(?:[0-9a-f]{16}|ts[0-9a-f]{20})$`)
	seen := map[string]struct{}{}

	for i := 0; i < 32; i++ {
		id := New()
		if !pattern.MatchString(id) {
			t.Fatalf("id %q does not match short id shape", id)
		}
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
}

type repeatReader struct {
	value byte
}

func (r *repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.value
	}
	return len(p), nil
}
