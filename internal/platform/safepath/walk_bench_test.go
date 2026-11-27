package safepath

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	"github.com/onsi/gomega/gmeasure"
)

// Synthetic tree sized like the workspace measured in G882: 4447 entries in
// 256 directories. 16 top directories of 15 subdirectories each give 256
// directories; 16 files in each gives 4096 files, 4352 entries in all.
const (
	benchTopDirs    = 16
	benchSubDirs    = 15
	benchFilesInDir = 16
	benchSamples    = 20
)

func writeBenchTree(root string) int {
	entries := 0
	writeFiles := func(dir string) {
		for i := range benchFilesInDir {
			gomega.Expect(os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%02d.cpp", i)), []byte("int x;\n"), 0o600)).To(gomega.Succeed())
			entries++
		}
	}
	for top := range benchTopDirs {
		topDir := filepath.Join(root, fmt.Sprintf("top%02d", top))
		gomega.Expect(os.Mkdir(topDir, 0o700)).To(gomega.Succeed())
		entries++
		writeFiles(topDir)
		for sub := range benchSubDirs {
			subDir := filepath.Join(topDir, fmt.Sprintf("sub%02d", sub))
			gomega.Expect(os.Mkdir(subDir, 0o700)).To(gomega.Succeed())
			entries++
			writeFiles(subDir)
		}
	}
	return entries
}

func countWalk(root string, want int, walk func(root string, fn fs.WalkDirFunc) error) {
	seen := -1 // the root itself
	err := walk(root, func(_ string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		seen++
		return nil
	})
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(seen).To(gomega.Equal(want), "walk entry count")
}

// The experiment only runs when the "bench" label is asked for, e.g.
// `go test ./internal/platform/safepath -ginkgo.label-filter=bench -ginkgo.v`.
var _ = ginkgo.Describe("safepath walk benchmark", ginkgo.Serial, ginkgo.Label("bench"), func() {
	ginkgo.It("compares WalkDirUnderRoot with filepath.WalkDir on the same tree", func() {
		if !ginkgo.Label("bench").MatchesLabelFilter(ginkgo.GinkgoLabelFilter()) || ginkgo.GinkgoLabelFilter() == "" {
			ginkgo.Skip("benchmark: run with -ginkgo.label-filter=bench")
		}

		root := ginkgo.GinkgoT().TempDir()
		want := writeBenchTree(root)

		experiment := gmeasure.NewExperiment("walk of a 4352-entry tree")
		experiment.Sample(func(int) {
			experiment.MeasureDuration("WalkDirUnderRoot", func() {
				countWalk(root, want, func(root string, fn fs.WalkDirFunc) error {
					return WalkDirUnderRoot(root, root, "bench", fn)
				})
			})
			experiment.MeasureDuration("filepath.WalkDir", func() {
				countWalk(root, want, filepath.WalkDir)
			})
		}, gmeasure.SamplingConfig{N: benchSamples})

		safe := experiment.GetStats("WalkDirUnderRoot").DurationFor(gmeasure.StatMedian)
		plain := experiment.GetStats("filepath.WalkDir").DurationFor(gmeasure.StatMedian)
		ginkgo.AddReportEntry(experiment.Name, experiment)
		ginkgo.GinkgoWriter.Printf("median WalkDirUnderRoot %v, filepath.WalkDir %v\n", safe, plain)
		gomega.Expect(safe).To(gomega.BeNumerically(">", time.Duration(0)))
		gomega.Expect(plain).To(gomega.BeNumerically(">", time.Duration(0)))
	})
})
