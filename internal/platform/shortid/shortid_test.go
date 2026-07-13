package shortid

import (
	"errors"
	"io"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

var _ = ginkgo.Describe("Short IDs", func() {
	ginkgo.It("encodes eight bytes of entropy as hexadecimal", func() {
		id := newWithReader(
			io.LimitReader(&repeatReader{value: 0xab}, 8),
			func() time.Time { return time.Unix(0, 1) },
		)

		gomega.Expect(id).To(gomega.Equal("abababababababab"))
	})

	ginkgo.It("falls back to a timestamp and counter when entropy is unavailable", func() {
		id := newWithReader(errReader{}, func() time.Time { return time.Unix(0, 0x1234) })
		gomega.Expect(id).To(gomega.MatchRegexp(`^ts0000000000001234[0-9a-f]{4}$`))
	})

	ginkgo.It("generates locally unique IDs with the documented shape", func() {
		pattern := `^(?:[0-9a-f]{16}|ts[0-9a-f]{20})$`
		seen := map[string]struct{}{}

		for range 32 {
			id := New()
			gomega.Expect(id).To(gomega.MatchRegexp(pattern))
			gomega.Expect(seen).NotTo(gomega.HaveKey(id))
			seen[id] = struct{}{}
		}
	})
})

type repeatReader struct {
	value byte
}

func (r *repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.value
	}
	return len(p), nil
}
