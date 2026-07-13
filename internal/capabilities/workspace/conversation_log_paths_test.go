package workspace

import (
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("conversation log paths", func() {
	ginkgo.It("is default readable conversation log path", func() {
		home := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("HOME", home)
		ginkgo.GinkgoT().Setenv("USERPROFILE", home)

		globalLog := filepath.Join(home, ".idiom", "logs", "conversations", "session.jsonl")
		codexSession := filepath.Join(home, ".codex", "sessions", "2026", "06", "28", "rollout-2026-06-28T12-00-00-session.jsonl")
		codexArchivedSession := filepath.Join(home, ".codex", "archived_sessions", "rollout-2026-06-28T12-00-00-session.jsonl")

		tests := []struct {
			path string
			want bool
		}{
			{path: "logs/conversations/session.jsonl", want: true},
			{path: "./logs/conversations/session.jsonl", want: true},
			{path: ".idiom/logs/conversations/session.jsonl", want: true},
			{path: globalLog, want: true},
			{path: codexSession, want: true},
			{path: codexArchivedSession, want: true},
			{path: "logs/telemetry/session.jsonl", want: false},
			{path: ".idiom/config.json", want: false},
			{path: filepath.Join(home, ".idiom", "config.json"), want: false},
			{path: filepath.Join(home, ".codex", "config.toml"), want: false},
			{path: filepath.Join(home, ".codex", "sessions", "not-a-session.txt"), want: false},
		}

		for _, tt := range tests {
			gomega.Expect(IsDefaultReadableConversationLogPath(tt.path)).To(
				gomega.Equal(tt.want),
				"IsDefaultReadableConversationLogPath(%q)",
				tt.path,
			)
		}
	})
})
