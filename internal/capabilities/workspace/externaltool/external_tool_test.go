package externaltool

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("External tool arguments", func() {
	ginkgo.DescribeTable("substitutes placeholders",
		func(template string, input map[string]any, want []string) {
			got := SubstituteArgs(template, input)
			if len(want) == 0 {
				gomega.Expect(got).To(gomega.BeEmpty())
				return
			}
			gomega.Expect(got).To(gomega.Equal(want))
		},
		ginkgo.Entry("drops a missing whole-token placeholder", "{optional}", map[string]any{}, []string{}),
		ginkgo.Entry("expands a whole-token []any value", "{files}", map[string]any{
			"files": []any{"native/src/a.cpp", "native/src/b.cpp"},
		}, []string{"native/src/a.cpp", "native/src/b.cpp"}),
		ginkgo.Entry("expands a whole-token string slice", "{files}", map[string]any{
			"files": []string{"native/src/a.cpp", "native/src/b.cpp"},
		}, []string{"native/src/a.cpp", "native/src/b.cpp"}),
		ginkgo.Entry("serializes an embedded array without splitting the argument", "--files={files}", map[string]any{
			"files": []any{"native/src/a.cpp", "native/src/b.cpp"},
		}, []string{`--files=["native/src/a.cpp","native/src/b.cpp"]`}),
		ginkgo.Entry("includes a truthy conditional", "{?terse:--terse}", map[string]any{"terse": true}, []string{"--terse"}),
		ginkgo.Entry("drops a false conditional", "{?json:--json}", map[string]any{"terse": true}, []string{}),
	)
})

var _ = ginkgo.Describe("External tool registry", func() {
	ginkgo.It("classifies only read-only discovery tools", func() {
		reg, err := NewRegistry([]Tool{
			{Name: "lookup", Command: "true", ReadOnly: true, Discovery: true},
			{Name: "queue_run", Command: "true", ReadOnly: false, Discovery: true},
			{Name: "format", Command: "true", ReadOnly: true, Discovery: false},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		gomega.Expect(reg.IsReadOnlyDiscovery("lookup")).To(gomega.BeTrue())
		gomega.Expect(reg.IsReadOnlyDiscovery("queue_run")).To(gomega.BeFalse())
		gomega.Expect(reg.IsReadOnlyDiscovery("format")).To(gomega.BeFalse())
		gomega.Expect(reg.IsReadOnlyDiscovery("missing")).To(gomega.BeFalse())

		tool, ok := reg.Tool("lookup")
		gomega.Expect(ok).To(gomega.BeTrue())
		gomega.Expect(tool.ReadOnly).To(gomega.BeTrue())
		gomega.Expect(tool.Discovery).To(gomega.BeTrue())
	})

	ginkgo.It("filters missing relative and absolute commands", func() {
		root := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.MkdirAll(filepath.Join(root, "scripts"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(
			filepath.Join(root, "scripts", "ok.sh"),
			[]byte("#!/bin/sh\necho ok\n"),
			0o755,
		)).To(gomega.Succeed())

		reg, err := NewRegistry([]Tool{
			{Name: "ok_relative", Command: "./scripts/ok.sh"},
			{Name: "missing_absolute", Command: filepath.Join(root, "missing.sh")},
			{Name: "missing_relative", Command: "./scripts/missing.sh"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		names := reg.AvailableNames(root)
		gomega.Expect(names).To(gomega.ContainElement("ok_relative"))
		gomega.Expect(names).NotTo(gomega.ContainElements("missing_absolute", "missing_relative"))

		for _, missing := range []string{"missing_absolute", "missing_relative"} {
			ok, reason := reg.Availability(missing, root)
			gomega.Expect(ok).To(gomega.BeFalse())
			gomega.Expect(reason).NotTo(gomega.BeEmpty())
		}
	})

	ginkgo.It("accepts a bare executable found on PATH", func() {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("executable shell fixture uses POSIX mode bits")
		}

		binDir := ginkgo.GinkgoT().TempDir()
		gomega.Expect(os.WriteFile(
			filepath.Join(binDir, "idiom-external-ok"),
			[]byte("#!/bin/sh\necho ok\n"),
			0o755,
		)).To(gomega.Succeed())
		ginkgo.GinkgoT().Setenv("PATH", binDir)

		reg, err := NewRegistry([]Tool{
			{Name: "ok_path", Command: "idiom-external-ok"},
			{Name: "missing_path", Command: "idiom-external-missing"},
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		names := reg.AvailableNames(ginkgo.GinkgoT().TempDir())
		gomega.Expect(names).To(gomega.ContainElement("ok_path"))
		gomega.Expect(names).NotTo(gomega.ContainElement("missing_path"))

		ok, reason := reg.Availability("missing_path", "")
		gomega.Expect(ok).To(gomega.BeFalse())
		gomega.Expect(reason).To(gomega.Equal("command not found on PATH"))
	})
})
