package commandexec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OS sandbox profiles", func() {
	It("validates absolute roots and network modes", func() {
		Expect((SandboxProfile{Enforcement: SandboxEnforced, ReadRoots: []string{"relative"}}).Validate()).To(MatchError(ContainSubstring("absolute")))
		Expect((SandboxProfile{Enforcement: SandboxEnforced, Network: "unknown"}).Validate()).To(MatchError(ContainSubstring("network mode")))
		Expect((SandboxProfile{Enforcement: SandboxEnforced, ReadRoots: []string{"/tmp"}, Network: SandboxNetworkDeny}).Validate()).To(Succeed())
	})

	It("runs an admitted command through the enforced host sandbox when available", func() {
		if !SandboxSupported() {
			Skip("host sandbox launcher is unavailable")
		}
		root := GinkgoT().TempDir()
		allowed := filepath.Join(root, "allowed.txt")
		outside := filepath.Join(root, "outside.txt")
		Expect(os.WriteFile(allowed, []byte("allowed"), 0o600)).To(Succeed())
		Expect(os.WriteFile(outside, []byte("outside"), 0o600)).To(Succeed())
		profile := &SandboxProfile{Enforcement: SandboxEnforced, ReadRoots: []string{root}, WriteRoots: []string{root}, Network: SandboxNetworkDeny}
		command := "/bin/sh"
		if runtime.GOOS == "linux" {
			command = "sh"
		}
		result := Run(context.Background(), Spec{Mode: ModeArgv, Name: command, Args: []string{"-c", "cat " + allowed + "; (echo denied > /tmp/idiom-sandbox-denied || true)"}, Dir: root, Sandbox: profile, MaxOutputBytes: 1024})

		Expect(result.Err).NotTo(HaveOccurred())
		Expect(result.Stdout).To(ContainSubstring("allowed"))
		Expect(result.Stderr).To(ContainSubstring("Operation not permitted"))
	})

	It("fails closed for enforced profiles when the launcher is unavailable", func() {
		if SandboxSupported() {
			Skip("host sandbox launcher is available")
		}
		result := Run(context.Background(), Spec{Mode: ModeArgv, Name: "true", Sandbox: &SandboxProfile{Enforcement: SandboxEnforced, ReadRoots: []string{"/tmp"}}})
		Expect(result.Err).To(MatchError(ContainSubstring("enforced sandbox is unavailable")))
	})
})
