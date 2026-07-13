package cmdtelemetry

import (
	"encoding/json"
	"os"
	"strings"
	"syscall"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("CLI telemetry", func() {
	ginkgo.It("records the resolved command name", func() {
		recorder := OpenRecorder("tool:pwd", []string{"tool", "pwd"}, RecorderOptions{Dir: ginkgo.GinkgoT().TempDir()})
		gomega.Expect(recorder).NotTo(gomega.BeNil())
		gomega.Expect(recorder.CloseWithFields(0, nil)).To(gomega.Succeed())

		records := readCLITelemetryRecords(recorder.Path())
		gomega.Expect(len(records)).To(gomega.BeNumerically(">=", 2))
		for _, record := range records[:2] {
			gomega.Expect(record).To(gomega.HaveKeyWithValue("command", "tool:pwd"))
		}
	})

	ginkgo.DescribeTable("maps process signals to shell exit codes",
		func(signal os.Signal, want int) {
			gomega.Expect(SignalExitCode(signal)).To(gomega.Equal(want))
		},
		ginkgo.Entry("interrupt", os.Interrupt, 130),
		ginkgo.Entry("terminate", syscall.SIGTERM, 143),
	)

	ginkgo.It("stops without consuming normal recorder closeout", func() {
		recorder := OpenRecorder("normal-stop", nil, RecorderOptions{Dir: ginkgo.GinkgoT().TempDir()})
		gomega.Expect(recorder).NotTo(gomega.BeNil())

		handler := newSignalHandler(recorder, make(chan os.Signal, 1), func() {})
		_, handled := handler.StopAndWait()
		gomega.Expect(handled).To(gomega.BeFalse())
		gomega.Expect(recorder.CloseWithFields(0, nil)).To(gomega.Succeed())

		exits := cliTelemetryProcessExits(readCLITelemetryRecords(recorder.Path()))
		gomega.Expect(exits).To(gomega.HaveLen(1))
		gomega.Expect(exits[0]).To(gomega.HaveKeyWithValue("exit_code", float64(0)))
	})

	ginkgo.It("lets a buffered signal win a concurrent normal stop exactly once", ginkgo.Label("race-contract"), func() {
		recorder := OpenRecorder("signal-stop-race", nil, RecorderOptions{Dir: ginkgo.GinkgoT().TempDir()})
		gomega.Expect(recorder).NotTo(gomega.BeNil())

		signals := make(chan os.Signal, 1)
		signals <- syscall.SIGTERM
		handler := newSignalHandler(recorder, signals, func() {})

		result, handled := handler.StopAndWait()
		gomega.Expect(handled).To(gomega.BeTrue())
		gomega.Expect(result.Signal).To(gomega.Equal(syscall.SIGTERM))
		gomega.Expect(result.ExitCode).To(gomega.Equal(143))
		gomega.Expect(result.CloseError).NotTo(gomega.HaveOccurred())

		// A losing normal-return path is harmless and cannot append another
		// process-exit record after the signal closed the recorder.
		gomega.Expect(recorder.CloseWithFields(0, nil)).To(gomega.Succeed())
		exits := cliTelemetryProcessExits(readCLITelemetryRecords(recorder.Path()))
		gomega.Expect(exits).To(gomega.HaveLen(1))
		gomega.Expect(exits[0]).To(gomega.And(
			gomega.HaveKeyWithValue("exit_code", float64(143)),
			gomega.HaveKeyWithValue("reason", "signal"),
			gomega.HaveKeyWithValue("signal", syscall.SIGTERM.String()),
		))
	})
})

func cliTelemetryProcessExits(records []map[string]any) []map[string]any {
	exits := make([]map[string]any, 0, 1)
	for _, record := range records {
		if record["type"] == "process_exit" {
			exits = append(exits, record)
		}
	}

	return exits
}

func readCLITelemetryRecords(path string) []map[string]any {
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
