package app

import (
	"fmt"
	"path/filepath"

	"idiom/internal/platform/config"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
)

var _ = ginkgo.It("new suppresses default debug logger during tests", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()

	cfg := config.Default()
	cfg.Chat.DebugLogDir = filepath.Join("logs", "conversations")
	cfg.Chat.ProjectRoot = root
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)
	defer model.Close()

	gomega.Expect(model.debugLogger).To(gomega.BeNil(), fmt.Sprintf("default test debug logger should be suppressed, logger=%v path=%q", model.debugLogger, model.DebugLogPath()))
	gomega.Expect(model.DebugLogPath()).To(gomega.Equal(""), fmt.Sprintf("default test debug logger should be suppressed, logger=%v path=%q", model.debugLogger, model.DebugLogPath()))
	{
		matches, _ := filepath.Glob(filepath.Join(root, "logs", "conversations", "*.jsonl"))
		gomega.Expect(len(matches)).To(gomega.Equal(0), fmt.Sprintf("default test debug logger should not create conversation logs: %#v", matches))
	}
})

var _ = ginkgo.It("new suppresses empty debug logger during tests", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()

	cfg := config.Default()
	cfg.Chat.DebugLogDir = ""
	cfg.Chat.ProjectRoot = root
	cfg.Workspace.Root = root
	cfg.Workspace.CWD = root

	model := New(cfg)
	defer model.Close()

	gomega.Expect(model.debugLogger).To(gomega.BeNil(), fmt.Sprintf("empty test debug logger should be suppressed, logger=%v path=%q", model.debugLogger, model.DebugLogPath()))
	gomega.Expect(model.DebugLogPath()).To(gomega.Equal(""), fmt.Sprintf("empty test debug logger should be suppressed, logger=%v path=%q", model.debugLogger, model.DebugLogPath()))
	{
		matches, _ := filepath.Glob(filepath.Join(root, "logs", "conversations", "*.jsonl"))
		gomega.Expect(len(matches)).To(gomega.Equal(0), fmt.Sprintf("empty test debug logger should not fall back to default logs: %#v", matches))
	}
})

var _ = ginkgo.It("new keeps explicit debug logger during tests", func() {
	t := ginkgo.GinkgoT()
	cfg := config.Default()
	cfg.Chat.DebugLogDir = t.TempDir()

	model := New(cfg)
	defer model.Close()

	gomega.Expect(model.debugLogger).To(gomega.Not(gomega.BeNil()), fmt.Sprint("explicit test debug log dir should still create a logger"))
	gomega.Expect(model.DebugLogPath()).To(gomega.Not(gomega.Equal("")), fmt.Sprint("explicit test debug log dir should still create a logger"))
	gomega.Expect(filepath.IsAbs(model.DebugLogPath())).To(gomega.BeTrue(), fmt.Sprintf("explicit debug log path should be absolute, got %q", model.DebugLogPath()))
})
