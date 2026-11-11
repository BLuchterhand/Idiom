package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Atomic file writes", func() {
	ginkgo.It("preserves an existing private mode", func() {
		path := filepath.Join(ginkgo.GinkgoT().TempDir(), "private.txt")
		gomega.Expect(os.WriteFile(path, []byte("old"), 0o600)).To(gomega.Succeed())
		gomega.Expect(WriteFile(path, []byte("new"), 0o644)).To(gomega.Succeed())
		expectFileMode(path, 0o600)
	})

	ginkgo.It("preserves an existing executable mode", func() {
		if runtime.GOOS == "windows" {
			ginkgo.Skip("Windows does not preserve POSIX execute bits")
		}

		path := filepath.Join(ginkgo.GinkgoT().TempDir(), "script.sh")
		gomega.Expect(os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.Chmod(path, 0o755)).To(gomega.Succeed())
		gomega.Expect(WriteFile(path, []byte("#!/bin/sh\necho ok\n"), 0o644)).To(gomega.Succeed())
		expectFileMode(path, 0o755)
	})

	ginkgo.It("uses the requested mode for a new file", func() {
		path := filepath.Join(ginkgo.GinkgoT().TempDir(), "new.txt")
		gomega.Expect(WriteFile(path, []byte("new"), 0o640)).To(gomega.Succeed())
		expectFileMode(path, 0o640)
	})

	ginkgo.It("treats an unsupported committed-directory sync as successful", func() {
		gomega.Expect(syncCommittedDirectory("unused", func(string) error {
			return syscall.EINVAL
		})).To(gomega.Succeed())
	})

	ginkgo.It("treats Windows access denied on directory sync as unsupported", func() {
		if runtime.GOOS != "windows" {
			ginkgo.Skip("Windows directory sync behavior")
		}

		gomega.Expect(syncCommittedDirectory("unused", func(string) error {
			return syscall.Errno(5)
		})).To(gomega.Succeed())
	})

	ginkgo.It("returns operational committed-directory sync failures as post-commit errors", func() {
		cause := errors.New("disk disappeared")
		err := syncCommittedDirectory("unused", func(string) error { return cause })
		gomega.Expect(err).To(gomega.HaveOccurred())
		gomega.Expect(errors.Is(err, cause)).To(gomega.BeTrue())

		var postCommitErr *PostCommitError
		gomega.Expect(errors.As(err, &postCommitErr)).To(gomega.BeTrue())
		gomega.Expect(postCommitErr.Committed()).To(gomega.BeTrue())
	})
})

func expectFileMode(path string, want os.FileMode) {
	info, err := os.Stat(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	gomega.Expect(info.Mode().Perm()).To(gomega.Equal(want))
}
