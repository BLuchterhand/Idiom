package sidecarvalidation

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
)

func sidecarValidationRepoRoot() string {
	ginkgo.GinkgoHelper()

	dir, err := os.Getwd()
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("get cwd: %v", err))

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		gomega.Expect(parent).NotTo(gomega.BeEquivalentTo(dir), fmt.Sprintf("repo root not found from cwd"))

		dir = parent
	}
}
