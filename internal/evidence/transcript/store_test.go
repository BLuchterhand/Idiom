package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Markdown transcripts", func() {
	ginkgo.It("writes role headings and message bodies", func() {
		dir := filepath.Join(ginkgo.GinkgoT().TempDir(), "transcripts")
		path, err := SaveMarkdown(dir, []Message{
			{Role: "user", Content: "Please inspect evidence."},
			{Role: "assistant", Content: "The evidence is grounded."},
			{Content: "Untitled message."},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(path).To(gomega.HavePrefix(dir + string(os.PathSeparator)))
		gomega.Expect(path).To(gomega.HaveSuffix(".md"))

		data, err := os.ReadFile(path)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		body := string(data)
		for _, want := range []string{
			"# Idiom Transcript",
			"## User\n\nPlease inspect evidence.",
			"## Assistant\n\nThe evidence is grounded.",
			"## Message\n\nUntitled message.",
		} {
			gomega.Expect(body).To(gomega.ContainSubstring(want))
		}
	})

	ginkgo.It("preserves every transcript saved at the same instant", func() {
		dir := filepath.Join(ginkgo.GinkgoT().TempDir(), "transcripts")
		savedAt := time.Date(2026, time.July, 12, 9, 30, 45, 123456789, time.UTC)

		firstPath, err := saveMarkdown(dir, []Message{{Role: "user", Content: "first transcript"}}, savedAt)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		secondPath, err := saveMarkdown(dir, []Message{{Role: "user", Content: "second transcript"}}, savedAt)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		gomega.Expect(secondPath).NotTo(gomega.Equal(firstPath))
		for path, content := range map[string]string{
			firstPath:  "first transcript",
			secondPath: "second transcript",
		} {
			gomega.Expect(filepath.Base(path)).To(gomega.MatchRegexp(`^20260712-093045\.123456789-[0-9a-f]{32}\.md$`))
			body, readErr := os.ReadFile(path)
			gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(body)).To(gomega.ContainSubstring(content))
		}

		entries, err := os.ReadDir(dir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(2))
	})

	ginkgo.It("publishes every concurrent save as one complete file", ginkgo.Label(
		"development-scenario",
		"e2e",
		"integration",
		"smoke",
		"race-contract",
	), func() {
		const saveCount = 48

		type saveResult struct {
			path    string
			content string
			err     error
		}

		dir := filepath.Join(ginkgo.GinkgoT().TempDir(), "transcripts")
		start := make(chan struct{})
		results := make(chan saveResult, saveCount)
		var saves sync.WaitGroup
		saves.Add(saveCount)
		for index := range saveCount {
			go func() {
				defer saves.Done()
				<-start

				content := fmt.Sprintf("concurrent transcript %02d", index)
				path, err := SaveMarkdown(dir, []Message{{Role: "assistant", Content: content}})
				results <- saveResult{path: path, content: content, err: err}
			}()
		}

		close(start)
		saves.Wait()
		close(results)

		paths := make(map[string]string, saveCount)
		for result := range results {
			gomega.Expect(result.err).NotTo(gomega.HaveOccurred())
			gomega.Expect(paths).NotTo(gomega.HaveKey(result.path))
			paths[result.path] = result.content
		}
		gomega.Expect(paths).To(gomega.HaveLen(saveCount))

		entries, err := os.ReadDir(dir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(saveCount))
		for _, entry := range entries {
			gomega.Expect(entry.Name()).To(gomega.MatchRegexp(`\.md$`))
			gomega.Expect(entry.Name()).NotTo(gomega.HavePrefix("."))
		}
		for path, content := range paths {
			body, readErr := os.ReadFile(path)
			gomega.Expect(readErr).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(body)).To(gomega.And(
				gomega.HavePrefix("# Idiom Transcript\n\n"),
				gomega.ContainSubstring("## Assistant\n\n"+content+"\n\n"),
			))
		}
	})

	ginkgo.DescribeTable("normalizes role titles",
		func(role, want string) {
			gomega.Expect(transcriptTitle(role)).To(gomega.Equal(want))
		},
		ginkgo.Entry("trimmed role", " assistant", "Assistant"),
		ginkgo.Entry("empty role", "", ""),
	)
})
