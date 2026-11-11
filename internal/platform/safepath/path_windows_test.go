package safepath

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("Windows path spellings", func() {
	ginkgo.It("normalizes drive case, extended prefixes, Git Bash paths, and slash styles", func() {
		root := `C:\work\repo`
		want := "folder/file.gd" // Rel returns slash-separated workspace paths (filepath.ToSlash), as on main
		paths := []string{
			`C:\work\repo\folder\file.gd`,
			`C:/work/repo/folder/file.gd`,
			`c:\WORK\REPO\folder\file.gd`,
			`\\?\C:\work\repo\folder\file.gd`,
			`/c/work/repo/folder/file.gd`,
		}
		for _, path := range paths {
			rel, ok := Rel(root, path)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("path %q should be in root", path))
			gomega.Expect(rel).To(gomega.Equal(want), fmt.Sprintf("path %q should normalize to a workspace relative path", path))
		}
		uncPath, err := CleanPathChecked(`\\?\UNC\server\share\work\repo\folder\file.gd`)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(uncPath).To(gomega.Equal(`\\server\share\work\repo\folder\file.gd`))
	})

	ginkgo.It("rejects unsupported device and volume namespaces before containment", func() {
		root := `C:\work\repo`
		unsupported := []string{
			`\\.\C:\work\repo\file.gd`,
			`\\?\Volume{12345678-1234-1234-1234-123456789abc}\work\repo\file.gd`,
			`\\?\GLOBALROOT\Device\HarddiskVolume1\work\repo\file.gd`,
		}
		for _, path := range unsupported {
			_, _, err := UnderRoot(root, path, "test path", Options{AllowMissing: true})
			gomega.Expect(err).To(gomega.HaveOccurred(), fmt.Sprintf("reject namespace %q", path))
		}
	})

	ginkgo.It("resolves existing path forms including a .git worktree file", func() {
		root := ginkgo.GinkgoT().TempDir()
		if filepath.VolumeName(root) == "" {
			ginkgo.Skip("temporary directory is not on a drive volume")
		}
		mustWriteFile(filepath.Join(root, ".git"), "gitdir: C:/Users/test/repo/.git/worktrees/example\n")
		file := filepath.Join(root, "scripts", "example.gd")
		mustWriteFile(file, "extends Node\n")
		requireCanonicalWindowsFilesystem(root)

		drive := filepath.VolumeName(file)
		shortDrive := strings.ToLower(drive) + file[len(drive):]
		gitBash := "/" + strings.ToLower(drive[:1]) + strings.ReplaceAll(file[len(drive):], `\`, "/")
		paths := []string{
			filepath.ToSlash(file),
			shortDrive,
			`\\?\` + file,
			gitBash,
		}
		for _, path := range paths {
			_, rel, err := UnderRoot(root, path, "test path", Options{})
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("resolve %q", path))
			gomega.Expect(rel).To(gomega.Equal("scripts/example.gd"))
		}
	})

	ginkgo.It("expands an existing 8.3 short path before checking containment", func() {
		root := ginkgo.GinkgoT().TempDir()
		file := filepath.Join(root, "long-directory-name", "example.gd")
		mustWriteFile(file, "extends Node\n")
		requireCanonicalWindowsFilesystem(root)

		short, ok := windowsShortPath(file)
		if !ok || strings.EqualFold(short, file) {
			ginkgo.Skip("8.3 short names are disabled for this volume")
		}

		_, rel, err := UnderRoot(root, short, "test path", Options{})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(rel).To(gomega.Equal("long-directory-name/example.gd"))
	})

	ginkgo.It("rejects junction traversal outside the workspace", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		mustWriteFile(filepath.Join(outside, "secret.gd"), "extends Node\n")
		requireCanonicalWindowsFilesystem(root)

		junction := filepath.Join(root, "linked")
		command := `mklink /J "` + junction + `" "` + outside + `"`
		if output, err := exec.Command("cmd.exe", "/c", command).CombinedOutput(); err != nil {
			ginkgo.Skip(fmt.Sprintf("could not create a junction: %s", strings.TrimSpace(string(output))))
		}

		_, _, err := UnderRoot(root, filepath.Join(junction, "secret.gd"), "test path", Options{})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("symlink target escapes workspace root")))
		err = RejectSymlinkAncestors(root, filepath.Join(junction, "secret.gd"), "write path")
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("must not traverse symlinked directories")))
	})
})

func requireCanonicalWindowsFilesystem(root string) {
	ginkgo.GinkgoHelper()
	if _, err := CanonicalExistingPath(root); err != nil {
		if os.IsPermission(err) {
			ginkgo.Skip("filesystem canonicalization is denied in this environment")
		}
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), "canonicalize test root")
	}
}

func windowsShortPath(path string) (string, bool) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", false
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetShortPathNameW")
	buffer := make([]uint16, 512)
	length, _, _ := proc.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if length == 0 || length >= uintptr(len(buffer)) {
		return "", false
	}
	return syscall.UTF16ToString(buffer[:length]), true
}
