package readapprovalcache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/contracts/readapproval"
)

var _ = ginkgo.Describe("File-backed read approval cache", func() {
	ginkgo.It("misses for an unknown session", func() {
		decision, found, err := NewFileCache(ginkgo.GinkgoT().TempDir()).Lookup("missing-session", "docs/foo.md")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeFalse())
		gomega.Expect(decision).To(gomega.Equal(readapproval.Decision{}))
	})

	ginkgo.It("round-trips a decision", func() {
		cache := NewFileCache(ginkgo.GinkgoT().TempDir())
		decision := approvedDecision("docs/design-intent-loop-mutation-cycle.md")
		gomega.Expect(cache.Record("session-1", decision)).To(gomega.Succeed())

		got, found, err := cache.Lookup("session-1", decision.Path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(got.Outcome).To(gomega.Equal(decision.Outcome))
		gomega.Expect(got.Path).To(gomega.Equal(decision.Path))
	})

	ginkgo.It("persists decisions across cache instances", func() {
		root := ginkgo.GinkgoT().TempDir()
		decision := approvedDecision("docs/persistent.md")
		gomega.Expect(NewFileCache(root).Record("session-2", decision)).To(gomega.Succeed())
		gomega.Expect(filepath.Join(root, ".idiom", "read-approvals", "sessions", "session-2", "read-approvals.json")).To(gomega.BeAnExistingFile())

		got, found, err := NewFileCache(root).Lookup("session-2", decision.Path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(got.Outcome).To(gomega.Equal(decision.Outcome))
		gomega.Expect(got.Path).To(gomega.Equal(decision.Path))
	})

	ginkgo.It("does not publish a decision to memory when persistence fails", func() {
		cache := NewFileCache(ginkgo.GinkgoT().TempDir())
		const sessionID = "persist-failure-session"
		existing := approvedDecision("docs/existing.md")
		gomega.Expect(cache.Record(sessionID, existing)).To(gomega.Succeed())
		gomega.Expect(os.Remove(cache.sessionFile(sessionID))).To(gomega.Succeed())
		gomega.Expect(os.Mkdir(cache.sessionFile(sessionID), 0o700)).To(gomega.Succeed())

		unpersisted := approvedDecision("docs/unpersisted.md")
		gomega.Expect(cache.Record(sessionID, unpersisted)).To(gomega.HaveOccurred())
		_, found, err := cache.Lookup(sessionID, unpersisted.Path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeFalse())

		got, found, err := cache.Lookup(sessionID, existing.Path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(got.Outcome).To(gomega.Equal(existing.Outcome))
	})

	ginkgo.It("persists the stable file decision schema", func() {
		cache := NewFileCache(ginkgo.GinkgoT().TempDir())
		decision := approvedDecision("docs/shape.md")
		decision.At = time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
		gomega.Expect(cache.Record("shape-session", decision)).To(gomega.Succeed())

		data, err := os.ReadFile(cache.sessionFile("shape-session"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		var raw map[string]any
		gomega.Expect(json.Unmarshal(data, &raw)).To(gomega.Succeed())
		approved, ok := raw["approved"].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue())
		entry, ok := approved[decision.Path].(map[string]any)
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(entry).To(gomega.HaveKeyWithValue("outcome", readapproval.OutcomeApproved.String()))
		gomega.Expect(entry).To(gomega.HaveKeyWithValue("path", decision.Path))
		gomega.Expect(entry).NotTo(gomega.HaveKey("Outcome"))
	})

	ginkgo.It("returns sorted approved and denied path lists", func() {
		cache := NewFileCache(ginkgo.GinkgoT().TempDir())
		const sessionID = "session-3"
		for _, path := range []string{"docs/c.md", "docs/a.md", "docs/b.md"} {
			gomega.Expect(cache.Record(sessionID, approvedDecision(path))).To(gomega.Succeed())
		}
		gomega.Expect(cache.Record(sessionID, readapproval.Decision{
			Outcome: readapproval.OutcomeDenied,
			Path:    "docs/x.md",
			Source:  readapproval.SourceOperator,
		})).To(gomega.Succeed())

		gomega.Expect(cache.ApprovedPaths(sessionID)).To(gomega.Equal([]string{"docs/a.md", "docs/b.md", "docs/c.md"}))
		gomega.Expect(cache.DeniedPaths(sessionID)).To(gomega.Equal([]string{"docs/x.md"}))
	})

	ginkgo.It("isolates sessions", func() {
		cache := NewFileCache(ginkgo.GinkgoT().TempDir())
		gomega.Expect(cache.Record("session-a", readapproval.Decision{Outcome: readapproval.OutcomeApproved, Path: "docs/shared.md"})).To(gomega.Succeed())
		gomega.Expect(cache.Record("session-b", readapproval.Decision{Outcome: readapproval.OutcomeDenied, Path: "docs/shared.md"})).To(gomega.Succeed())

		approved, found, err := cache.Lookup("session-a", "docs/shared.md")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(approved.Outcome).To(gomega.Equal(readapproval.OutcomeApproved))
		denied, found, err := cache.Lookup("session-b", "docs/shared.md")
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(found).To(gomega.BeTrue())
		gomega.Expect(denied.Outcome).To(gomega.Equal(readapproval.OutcomeDenied))
	})

	ginkgo.It("rejects empty session IDs and paths", func() {
		cache := NewFileCache(ginkgo.GinkgoT().TempDir())
		gomega.Expect(cache.Record("", readapproval.Decision{Outcome: readapproval.OutcomeApproved, Path: "docs/x.md"})).To(gomega.HaveOccurred())
		gomega.Expect(cache.Record("session-x", readapproval.Decision{Outcome: readapproval.OutcomeApproved})).To(gomega.HaveOccurred())
	})

	ginkgo.DescribeTable("rejects unsafe session IDs across cache operations",
		func(sessionID string) {
			cache := NewFileCache(ginkgo.GinkgoT().TempDir())
			gomega.Expect(cache.Record(sessionID, approvedDecision("docs/x.md"))).To(gomega.HaveOccurred())
			_, found, err := cache.Lookup(sessionID, "docs/x.md")
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(found).To(gomega.BeFalse())
			gomega.Expect(cache.ApprovedPaths(sessionID)).To(gomega.BeEmpty())
		},
		ginkgo.Entry("parent traversal", "../escape"),
		ginkgo.Entry("nested path", "nested/session"),
		ginkgo.Entry("Windows nested path", `nested\session`),
		ginkgo.Entry("absolute path", "/absolute"),
		ginkgo.Entry("parent directory", ".."),
		ginkgo.Entry("current directory", "."),
	)

	ginkgo.DescribeTable("rejects inconsistent persisted records",
		func(sessionID string, record fileRecord) {
			cache := NewFileCache(ginkgo.GinkgoT().TempDir())
			writeReadApprovalRecordForTest(cache, sessionID, record)
			_, found, err := cache.Lookup(sessionID, "docs/x.md")
			gomega.Expect(err).To(gomega.HaveOccurred())
			gomega.Expect(found).To(gomega.BeFalse())
		},
		ginkgo.Entry("unsupported schema", "schema-session", fileRecord{
			Schema: "future.schema.v2", Session: "schema-session",
			Approved: map[string]fileDecision{"docs/x.md": {Outcome: readapproval.OutcomeApproved.String(), Path: "docs/x.md"}},
		}),
		ginkgo.Entry("session mismatch", "requested-session", fileRecord{
			Schema: fileRecordSchemaVersion, Session: "other-session",
			Approved: map[string]fileDecision{"docs/x.md": {Outcome: readapproval.OutcomeApproved.String(), Path: "docs/x.md"}},
		}),
		ginkgo.Entry("approved bucket with denied outcome", "inconsistent-session", fileRecord{
			Schema: fileRecordSchemaVersion, Session: "inconsistent-session",
			Approved: map[string]fileDecision{"docs/x.md": {Outcome: readapproval.OutcomeDenied.String(), Path: "docs/x.md"}},
		}),
		ginkgo.Entry("path mismatch", "path-mismatch-session", fileRecord{
			Schema: fileRecordSchemaVersion, Session: "path-mismatch-session",
			Approved: map[string]fileDecision{"docs/x.md": {Outcome: readapproval.OutcomeApproved.String(), Path: "docs/y.md"}},
		}),
	)

	ginkgo.It("supports concurrent records, lookups, and path-list reads", func() {
		cache := NewFileCache(ginkgo.GinkgoT().TempDir())
		const sessionID = "concurrent-session"
		errorsCh := make(chan error, 64)
		var wait sync.WaitGroup
		for index := range 32 {
			wait.Add(1)
			go func() {
				defer wait.Done()
				path := fmt.Sprintf("file-%02d.go", index)
				if err := cache.Record(sessionID, readapproval.Decision{
					Outcome: readapproval.OutcomeApproved,
					Path:    path,
					Reason:  "test",
					At:      time.Now(),
				}); err != nil {
					errorsCh <- fmt.Errorf("record %s: %w", path, err)
					return
				}
				if _, found, err := cache.Lookup(sessionID, path); err != nil || !found {
					errorsCh <- fmt.Errorf("lookup %s: found=%t err=%v", path, found, err)
				}
				_ = cache.ApprovedPaths(sessionID)
				_ = cache.DeniedPaths(sessionID)
			}()
		}
		wait.Wait()
		close(errorsCh)
		for err := range errorsCh {
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
		}
		gomega.Expect(cache.ApprovedPaths(sessionID)).To(gomega.HaveLen(32))
	})
})

func approvedDecision(path string) readapproval.Decision {
	return readapproval.Decision{
		Outcome: readapproval.OutcomeApproved,
		Reason:  readapproval.ReasonOperatorApprovedLive,
		Source:  readapproval.SourceOperator,
		At:      time.Now().UTC().Truncate(time.Second),
		Path:    path,
	}
}

func writeReadApprovalRecordForTest(cache *FileCache, sessionID string, record fileRecord) {
	gomega.Expect(os.MkdirAll(cache.sessionDir(sessionID), 0o700)).To(gomega.Succeed())
	data, err := json.Marshal(record)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(os.WriteFile(cache.sessionFile(sessionID), data, 0o600)).To(gomega.Succeed())
}
