package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Telemetry recording", func() {
	ginkgo.It("writes lifecycle records and redacts sensitive process arguments", func() {
		dir := ginkgo.GinkgoT().TempDir()
		recorder, err := Open(Options{
			Dir:           dir,
			Command:       "implement",
			Args:          []string{"implement", "--prompt", "change the secret thing", "--config", "config.json"},
			WorkspaceRoot: "/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(recorder).NotTo(gomega.BeNil())
		gomega.Expect(recorder.Log("custom", map[string]any{"ok": true})).To(gomega.Succeed())
		gomega.Expect(recorder.CloseWithFields(0, nil)).To(gomega.Succeed())

		entries, err := os.ReadDir(dir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(1))
		records := readTelemetryRecords(filepath.Join(dir, entries[0].Name()))
		gomega.Expect(records).To(gomega.HaveLen(4))
		gomega.Expect(records[0]).To(gomega.HaveKeyWithValue("type", "session_start"))
		gomega.Expect(records[1]).To(gomega.HaveKeyWithValue("type", "process_start"))
		gomega.Expect(records[3]).To(gomega.HaveKeyWithValue("type", "process_exit"))

		args, ok := records[1]["args"].([]any)
		gomega.Expect(ok).To(gomega.BeTrue())
		renderedArgs := strings.TrimSpace(toJSON(args))
		gomega.Expect(renderedArgs).NotTo(gomega.ContainSubstring("secret thing"))
		gomega.Expect(renderedArgs).To(gomega.ContainSubstring("redacted len=23"))
	})

	ginkgo.It("keeps short ordinary arguments and redacts sensitive or oversized values", func() {
		longValue := strings.Repeat("x", 241)
		rendered := toJSON(sanitizeArgs([]string{
			"",
			"--config= config.json ",
			"--description=" + longValue,
			"--prompt=change the private thing",
			"--task",
			"write a detailed private brief",
		}))

		gomega.Expect(rendered).To(gomega.ContainSubstring("--config=config.json"))
		gomega.Expect(rendered).NotTo(gomega.ContainSubstring(longValue))
		gomega.Expect(rendered).NotTo(gomega.ContainSubstring("change the private thing"))
		gomega.Expect(rendered).NotTo(gomega.ContainSubstring("write a detailed private brief"))
		gomega.Expect(strings.Count(rendered, "redacted len=")).To(gomega.Equal(3))
	})

	ginkgo.It("honors the telemetry directory environment override and sanitizes the command name", func() {
		dir := ginkgo.GinkgoT().TempDir()
		ginkgo.GinkgoT().Setenv("IDIOM_TELEMETRY_DIR", dir)

		recorder, err := Open(Options{
			Dir:     filepath.Join(ginkgo.GinkgoT().TempDir(), "ignored"),
			Command: "My Command!*",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(recorder).NotTo(gomega.BeNil())
		gomega.Expect(recorder.CloseWithFields(0, nil)).To(gomega.Succeed())
		gomega.Expect(recorder.Path()).To(gomega.HavePrefix(dir + string(os.PathSeparator)))
		gomega.Expect(filepath.Base(recorder.Path())).To(gomega.ContainSubstring("my-command"))
	})

	ginkgo.It("stamps configured versions on session and process starts", func() {
		dir := ginkgo.GinkgoT().TempDir()
		recorder, err := Open(Options{
			Dir:           dir,
			Command:       "version-stamp-test",
			Args:          []string{"version-stamp-test"},
			WorkspaceRoot: "/workspace",
			Version:       "v9.9.9-stamp",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(recorder.CloseWithFields(0, nil)).To(gomega.Succeed())

		entries, err := os.ReadDir(dir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		records := readTelemetryRecords(filepath.Join(dir, entries[0].Name()))
		gomega.Expect(records[0]).To(gomega.And(
			gomega.HaveKeyWithValue("type", "session_start"),
			gomega.HaveKeyWithValue("version", "v9.9.9-stamp"),
		))
		gomega.Expect(records[1]).To(gomega.And(
			gomega.HaveKeyWithValue("type", "process_start"),
			gomega.HaveKeyWithValue("version", "v9.9.9-stamp"),
		))
	})

	ginkgo.It("omits the version field when no version is configured", func() {
		dir := ginkgo.GinkgoT().TempDir()
		recorder, err := Open(Options{
			Dir:           dir,
			Command:       "no-version-test",
			Args:          []string{"no-version-test"},
			WorkspaceRoot: "/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(recorder.CloseWithFields(0, nil)).To(gomega.Succeed())

		entries, err := os.ReadDir(dir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		records := readTelemetryRecords(filepath.Join(dir, entries[0].Name()))
		gomega.Expect(records[0]).NotTo(gomega.HaveKey("version"))
		gomega.Expect(records[1]).NotTo(gomega.HaveKey("version"))
	})

	ginkgo.It("writes process-exit fields only once across repeated closes", func() {
		dir := ginkgo.GinkgoT().TempDir()
		recorder, err := Open(Options{Dir: dir, Command: "implement", WorkspaceRoot: "/workspace"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(recorder.CloseWithFields(143, map[string]any{
			"reason": "signal",
			"signal": "terminated",
		})).To(gomega.Succeed())
		gomega.Expect(recorder.CloseWithFields(0, nil)).To(gomega.Succeed())

		entries, err := os.ReadDir(dir)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(entries).To(gomega.HaveLen(1))
		records := readTelemetryRecords(filepath.Join(dir, entries[0].Name()))
		var processExits []map[string]any
		for _, record := range records {
			if record["type"] == "process_exit" {
				processExits = append(processExits, record)
			}
		}
		gomega.Expect(processExits).To(gomega.HaveLen(1))
		gomega.Expect(processExits[0]).To(gomega.And(
			gomega.HaveKeyWithValue("exit_code", float64(143)),
			gomega.HaveKeyWithValue("reason", "signal"),
			gomega.HaveKeyWithValue("signal", "terminated"),
		))
	})

	ginkgo.It("runs registered closeouts exactly once", func() {
		recorder, err := Open(Options{
			Dir:           ginkgo.GinkgoT().TempDir(),
			Command:       "implement",
			WorkspaceRoot: "/workspace",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		closeouts := 0
		recorder.RegisterCloseout(func() { closeouts++ })
		gomega.Expect(recorder.CloseWithFields(143, map[string]any{"reason": "signal"})).To(gomega.Succeed())
		gomega.Expect(recorder.CloseWithFields(0, nil)).To(gomega.Succeed())
		gomega.Expect(closeouts).To(gomega.Equal(1))
	})

	ginkgo.It("returns no recorder when telemetry is disabled", func() {
		ginkgo.GinkgoT().Setenv("IDIOM_TELEMETRY", "0")
		recorder, err := Open(Options{Dir: ginkgo.GinkgoT().TempDir(), Command: "tool"})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(recorder).To(gomega.BeNil())
	})
})

func readTelemetryRecords(path string) []map[string]any {
	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var record map[string]any
		gomega.Expect(json.Unmarshal([]byte(line), &record)).To(gomega.Succeed())
		records = append(records, record)
	}
	return records
}

func toJSON(value any) string {
	data, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	return string(data)
}
