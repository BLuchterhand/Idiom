package jsonl

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("JSONL scanning", func() {
	ginkgo.It("visits multiple records in file order", func() {
		path := writeJSONL(
			`{"id":1,"name":"alice"}`,
			`{"id":2,"name":"bob"}`,
			`{"id":3,"name":"carol"}`,
		)

		var records []json.RawMessage
		gomega.Expect(ScanFile(path, func(raw json.RawMessage) error {
			records = append(records, raw)
			return nil
		})).To(gomega.Succeed())
		gomega.Expect(records).To(gomega.HaveLen(3))

		for index, want := range []string{"alice", "bob", "carol"} {
			var record struct {
				Name string `json:"name"`
			}
			gomega.Expect(json.Unmarshal(records[index], &record)).To(gomega.Succeed())
			gomega.Expect(record.Name).To(gomega.Equal(want))
		}
	})

	ginkgo.It("does not invoke the callback for an empty file", func() {
		called := false
		gomega.Expect(ScanFile(writeJSONL(), func(json.RawMessage) error {
			called = true
			return nil
		})).To(gomega.Succeed())
		gomega.Expect(called).To(gomega.BeFalse())
	})

	ginkgo.DescribeTable("skips blank lines",
		func(blankLines []string) {
			lines := append([]string{`{"a":1}`}, blankLines...)
			lines = append(lines, `{"a":2}`, `{"a":3}`)
			count := 0
			gomega.Expect(ScanFile(writeJSONL(lines...), func(json.RawMessage) error {
				count++
				return nil
			})).To(gomega.Succeed())
			gomega.Expect(count).To(gomega.Equal(3))
		},
		ginkgo.Entry("empty lines", []string{"", "", ""}),
		ginkgo.Entry("whitespace-only lines", []string{"   ", "\t\t", "  \t  "}),
	)

	ginkgo.It("returns an error when the file does not exist", func() {
		err := ScanFile("/nonexistent/path/file.jsonl", func(json.RawMessage) error { return nil })
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("stops immediately when the callback returns an error", func() {
		sentinel := errors.New("stop")
		path := writeJSONL(`{"i":1}`, `{"i":2}`, `{"i":3}`)
		count := 0

		err := ScanFile(path, func(raw json.RawMessage) error {
			count++
			var record struct {
				I int `json:"i"`
			}
			if err := json.Unmarshal(raw, &record); err != nil {
				return err
			}
			if record.I == 2 {
				return sentinel
			}
			return nil
		})

		gomega.Expect(err).To(gomega.MatchError(sentinel))
		gomega.Expect(count).To(gomega.Equal(2))
	})

	ginkgo.It("scans a record larger than the default scanner buffer", func() {
		big := strings.Repeat("x", 200*1024)
		var raw json.RawMessage
		gomega.Expect(ScanFile(writeJSONL(`{"data":"`+big+`"}`), func(record json.RawMessage) error {
			raw = record
			return nil
		})).To(gomega.Succeed())

		var record struct {
			Data string `json:"data"`
		}
		gomega.Expect(json.Unmarshal(raw, &record)).To(gomega.Succeed())
		gomega.Expect(record.Data).To(gomega.HaveLen(len(big)))
	})

	ginkgo.It("preserves nested JSON values", func() {
		path := writeJSONL(
			`{"outer":{"inner":{"value":42}},"arr":[1,2,3]}`,
			`{"meta":{"tags":["a","b"],"nested":{"deep":true}}}`,
		)
		var records []json.RawMessage
		gomega.Expect(ScanFile(path, func(raw json.RawMessage) error {
			records = append(records, raw)
			return nil
		})).To(gomega.Succeed())
		gomega.Expect(records).To(gomega.HaveLen(2))

		var first struct {
			Outer struct {
				Inner struct {
					Value int `json:"value"`
				} `json:"inner"`
			} `json:"outer"`
			Arr []int `json:"arr"`
		}
		gomega.Expect(json.Unmarshal(records[0], &first)).To(gomega.Succeed())
		gomega.Expect(first.Outer.Inner.Value).To(gomega.Equal(42))
		gomega.Expect(first.Arr).To(gomega.Equal([]int{1, 2, 3}))
	})

	ginkgo.It("scans a single record exactly once", func() {
		count := 0
		var raw json.RawMessage
		gomega.Expect(ScanFile(writeJSONL(`{"only":true}`), func(record json.RawMessage) error {
			count++
			raw = record
			return nil
		})).To(gomega.Succeed())
		gomega.Expect(count).To(gomega.Equal(1))

		var record struct {
			Only bool `json:"only"`
		}
		gomega.Expect(json.Unmarshal(raw, &record)).To(gomega.Succeed())
		gomega.Expect(record.Only).To(gomega.BeTrue())
	})

	ginkgo.It("rejects a record over a custom maximum line size", func() {
		big := strings.Repeat("x", 5*1024*1024)
		err := ScanFile(writeJSONL(`{"data":"`+big+`"}`), func(json.RawMessage) error { return nil }, 1024*1024)
		gomega.Expect(err).To(gomega.HaveOccurred())
	})

	ginkgo.It("accepts a record within a custom maximum line size", func() {
		big := strings.Repeat("x", 512)
		count := 0
		gomega.Expect(ScanFile(writeJSONL(`{"data":"`+big+`"}`), func(json.RawMessage) error {
			count++
			return nil
		}, 4*1024*1024)).To(gomega.Succeed())
		gomega.Expect(count).To(gomega.Equal(1))
	})
})

func writeJSONL(lines ...string) string {
	path := filepath.Join(ginkgo.GinkgoT().TempDir(), "test.jsonl")
	gomega.Expect(os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)).To(gomega.Succeed())
	return path
}
