package contractcompile_test

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// skippedSourceDirectories hold vendored or fixture code that is not the repository's own source.
var skippedSourceDirectories = map[string]bool{".git": true, "node_modules": true, "testdata": true, "vendor": true}

// walkRepositoryFiles visits the repository's own files that end in suffix. Inside a git
// work tree it visits what git tracks or would track (.gitignore honoured), so scratch
// copies under ignored directories such as .cache/ can never fail a contract; outside git
// it walks the directory tree.
func walkRepositoryFiles(root string, suffix string, visit func(path string) error) error {
	paths, ok := gitVisibleFiles(root, suffix)
	if !ok {
		return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if skippedSourceDirectories[entry.Name()] && path != root {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, suffix) {
				return nil
			}
			return visit(path)
		})
	}
	for _, path := range paths {
		if err := visit(path); err != nil {
			return err
		}
	}
	return nil
}

func gitVisibleFiles(root string, suffix string) ([]string, bool) {
	prefix, err := exec.Command("git", "-C", root, "rev-parse", "--show-prefix").Output()
	if err != nil || len(bytes.TrimSpace(prefix)) != 0 {
		return nil, false // not a git work tree, or root is only a subdirectory of one
	}
	listing, err := exec.Command("git", "-C", root, "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, false
	}
	var paths []string
	for _, rel := range strings.Split(string(listing), "\x00") {
		if rel == "" || !strings.HasSuffix(rel, suffix) || inSkippedSourceDirectory(rel) {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		if _, statErr := os.Stat(path); statErr != nil {
			continue // tracked but deleted in the work tree
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, true
}

func inSkippedSourceDirectory(rel string) bool {
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		if skippedSourceDirectories[part] {
			return true
		}
	}
	return false
}

var _ = Describe("repository source scan", func() {
	const labeledSuite = `package sample
import . "github.com/onsi/ginkgo/v2"
var _ = It("runs", Label("e2e"), func() {})
`

	newFixture := func() string {
		if _, err := exec.LookPath("git"); err != nil {
			Skip("git is not installed")
		}
		root := GinkgoT().TempDir()
		for path, content := range map[string]string{
			".gitignore":                    ".cache/\n",
			"tracked/a_test.go":             labeledSuite,
			".cache/scratch/copy/b_test.go": labeledSuite,
		} {
			full := filepath.Join(root, filepath.FromSlash(path))
			Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
			Expect(os.WriteFile(full, []byte(content), 0o644)).To(Succeed())
		}
		Expect(exec.Command("git", "-C", root, "init", "-q").Run()).To(Succeed())
		return root
	}

	It("skips labeled suites under ignored directories", func() {
		labelsByDir, _, err := testLabelsByDirectory(newFixture())
		Expect(err).NotTo(HaveOccurred())

		var dirs []string
		for dir := range labelsByDir {
			dirs = append(dirs, filepath.Base(dir))
		}
		Expect(dirs).To(ConsistOf("tracked"))
	})

	It("still checks the same suite in an unignored directory", func() {
		root := newFixture()
		Expect(os.WriteFile(filepath.Join(root, ".gitignore"), nil, 0o644)).To(Succeed())

		labelsByDir, _, err := testLabelsByDirectory(root)
		Expect(err).NotTo(HaveOccurred())

		var dirs []string
		for dir := range labelsByDir {
			dirs = append(dirs, filepath.Base(dir))
		}
		Expect(dirs).To(ConsistOf("tracked", "copy"))
	})
})
