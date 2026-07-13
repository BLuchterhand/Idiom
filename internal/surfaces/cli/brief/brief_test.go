package brief

import (
	"bytes"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

var _ = ginkgo.Describe("brief", func() {
	ginkgo.It("run brief preview", func() {
		tmpDir := ginkgo.GinkgoT().TempDir()
		briefPath := filepath.Join(tmpDir, "test-brief.md")
		briefContent := `# Test Brief

## Problem

A test problem.

## Files To Modify

| File | Change |
|------|--------|
| "a.go" | change a |
| "b.go" | change b |

## Verified Current State

Some state.

## Required Behavior

1. Step one.
2. Step two.

## Acceptance

test done.
`
		gomega.Expect(os.WriteFile(briefPath, []byte(briefContent), 0o644)).To(gomega.Succeed())

		var stdout, stderr bytes.Buffer

		exit := runBriefPreview([]string{briefPath}, &stdout, &stderr, nil)
		gomega.Expect(exit).To(gomega.Equal(0), fmt.Sprintf("expected exit 0, got %d: %s", exit, stderr.String()))

		out := stdout.String()
		gomega.Expect(out).To(gomega.ContainSubstring("Files To Modify: 2"), fmt.Sprintf("expected 'Files To Modify: 2' in output, got:\n%s", out))
		gomega.Expect(out).To(gomega.ContainSubstring("Lint: OK"), fmt.Sprintf("expected 'Lint: OK' in output, got:\n%s", out))

	})
})
