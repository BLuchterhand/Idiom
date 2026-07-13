package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"

	"idiom/internal/contracts/toolfacts"
)

var _ = ginkgo.Describe("verification scope development workflow", ginkgo.Label("development-scenario", "e2e", "integration", "smoke"), func() {
	ginkgo.It("keeps Git patch hygiene exact before a nested Go test covers its package", func() {
		if _, err := exec.LookPath("go"); err != nil {
			ginkgo.Skip("go binary not available")
		}
		if _, err := exec.LookPath("/bin/sh"); err != nil {
			ginkgo.Skip("POSIX shell not available")
		}

		manager, root := gitWorkspace()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/verification-scope\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "internal", "inventory", "inventory.go"), `package inventory

func Available(onHand, reserved int) int {
	return onHand - reserved
}
`)
		writeWorkspaceFile(filepath.Join(root, "internal", "inventory", "inventory_test.go"), `package inventory

import "testing"

func TestAvailableInventory(t *testing.T) {
	if got := Available(5, 2); got != 3 {
		t.Fatalf("Available() = %d, want 3", got)
	}
}
`)
		writeWorkspaceFile(filepath.Join(root, "internal", "checkout", "checkout.go"), `package checkout

func ShippingCents() int {
	return 500
}
`)
		runWorkspaceGit(root, "add", "go.mod", "internal")
		runWorkspaceGit(root, "commit", "-q", "-m", "add inventory and checkout packages")

		writeWorkspaceFile(filepath.Join(root, "internal", "inventory", "inventory.go"), `package inventory

func Available(onHand, reserved int) int {
	if reserved > onHand {
		return 0
	}
	return onHand - reserved
}
`)
		writeWorkspaceFile(filepath.Join(root, "internal", "checkout", "checkout.go"), `package checkout

func ShippingCents() int {
	return 650
}
`)
		runWorkspaceGit(root, "add", "internal/checkout/checkout.go")
		writeWorkspaceFile(filepath.Join(root, "internal", "inventory", "inventory_edge_test.go"), `package inventory

import "testing"

func TestAvailableInventoryNeverBecomesNegative(t *testing.T) {
	if got := Available(2, 5); got != 0 {
		t.Fatalf("Available() = %d, want 0", got)
	}
}
`)

		status := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameGitStatus})
		gomega.Expect(status.Error).To(gomega.BeEmpty(), status.Output)
		gomega.Expect(status.Output).To(gomega.ContainSubstring(" M internal/inventory/inventory.go"))
		gomega.Expect(status.Output).To(gomega.ContainSubstring("M  internal/checkout/checkout.go"))
		gomega.Expect(status.Output).To(gomega.ContainSubstring("?? internal/inventory/inventory_edge_test.go"))

		pendingPaths := []string{
			"internal/inventory/inventory.go",
			"internal/checkout/checkout.go",
			"internal/inventory/inventory_edge_test.go",
		}
		gitCheck := manager.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameShell,
			Command: "git diff --check -- internal/inventory/inventory.go internal/checkout/checkout.go internal/inventory/inventory_edge_test.go",
		})
		gomega.Expect(gitCheck.Error).To(gomega.BeEmpty(), gitCheck.Output)
		gomega.Expect(gitCheck.Facts).NotTo(gomega.BeNil())
		gomega.Expect(gitCheck.Facts.ShellWorkdir).To(gomega.Equal("."))
		gomega.Expect(gitCheck.Facts.VerificationEvidence).To(gomega.Equal([]toolfacts.VerificationEvidence{{
			Kind:  toolfacts.VerificationEvidenceGitDiffCheck,
			Paths: []string{"internal/inventory/inventory.go"},
		}}))

		gitObservation := AnalyzePostWriteVerificationResult(gitCheck, pendingPaths)
		gomega.Expect(gitObservation.Attempted).To(gomega.BeTrue())
		gomega.Expect(gitObservation.Successful).To(gomega.BeTrue(), fmt.Sprintf("Git observation = %#v", gitObservation))
		gomega.Expect(gitObservation.PatchHygieneOnly).To(gomega.BeTrue())
		gomega.Expect(gitObservation.Coverage).To(gomega.Equal(VerificationCoverageExactPaths))
		gomega.Expect(gitObservation.Ecosystem).To(gomega.Equal(VerificationEcosystemCrossArtifact))
		gomega.Expect(gitObservation.ScopePaths).To(gomega.Equal([]string{"internal/inventory/inventory.go"}))
		gomega.Expect(gitObservation.CoveredPendingPaths).To(gomega.Equal([]string{"internal/inventory/inventory.go"}))
		gomega.Expect(gitObservation.CoveredPendingPaths).NotTo(gomega.ContainElement("internal/checkout/checkout.go"))
		gomega.Expect(gitObservation.CoveredPendingPaths).NotTo(gomega.ContainElement("internal/inventory/inventory_edge_test.go"))

		untrackedCheck := manager.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameShell,
			Command: "git diff --check -- internal/inventory/inventory_edge_test.go",
		})
		gomega.Expect(untrackedCheck.Error).To(gomega.BeEmpty(), untrackedCheck.Output)
		gomega.Expect(untrackedCheck.Facts).NotTo(gomega.BeNil())
		gomega.Expect(untrackedCheck.Facts.VerificationEvidence).To(gomega.BeEmpty())
		untrackedObservation := AnalyzePostWriteVerificationResult(untrackedCheck, []string{"internal/inventory/inventory_edge_test.go"})
		gomega.Expect(untrackedObservation.Attempted).To(gomega.BeTrue())
		gomega.Expect(untrackedObservation.Successful).To(gomega.BeFalse())
		gomega.Expect(untrackedObservation.CoveredPendingPaths).To(gomega.BeEmpty())

		goTest := manager.Execute(context.Background(), ToolRequest{
			Tool:    ToolNameShell,
			Command: "go test -run '^TestAvailableInventoryNeverBecomesNegative$' .",
			Workdir: "internal/inventory",
		})
		gomega.Expect(goTest.Error).To(gomega.BeEmpty(), goTest.Output)
		gomega.Expect(goTest.Output).To(gomega.ContainSubstring("example.com/verification-scope/internal/inventory"))
		gomega.Expect(goTest.Facts).NotTo(gomega.BeNil())
		gomega.Expect(goTest.Facts.ShellCommand).To(gomega.Equal("go test -run '^TestAvailableInventoryNeverBecomesNegative$' ."))
		gomega.Expect(goTest.Facts.ShellWorkdir).To(gomega.Equal("internal/inventory"))

		goObservation := AnalyzePostWriteVerificationResult(goTest, pendingPaths)
		gomega.Expect(goObservation.Attempted).To(gomega.BeTrue())
		gomega.Expect(goObservation.Successful).To(gomega.BeTrue(), fmt.Sprintf("Go observation = %#v", goObservation))
		gomega.Expect(goObservation.Verification).To(gomega.Equal(ShellVerificationTest))
		gomega.Expect(goObservation.BuildOrTest).To(gomega.BeTrue())
		gomega.Expect(goObservation.Coverage).To(gomega.Equal(VerificationCoveragePathScope))
		gomega.Expect(goObservation.Ecosystem).To(gomega.Equal(VerificationEcosystemGo))
		gomega.Expect(goObservation.Workdir).To(gomega.Equal("internal/inventory"))
		gomega.Expect(goObservation.ScopePaths).To(gomega.Equal([]string{"internal/inventory"}))
		gomega.Expect(goObservation.CoveredPendingPaths).To(gomega.Equal([]string{
			"internal/inventory/inventory.go",
			"internal/inventory/inventory_edge_test.go",
		}))
		gomega.Expect(goObservation.CoveredPendingPaths).NotTo(gomega.ContainElement("internal/checkout/checkout.go"))
	})
})
