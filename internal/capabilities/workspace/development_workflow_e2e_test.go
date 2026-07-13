package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("workspace development workflows", ginkgo.Label("development-scenario", "e2e", "integration", "smoke"), func() {
	ginkgo.It("indexes eight repository languages before mutating and verifying only the Go refactor", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeWorkspaceFile(filepath.Join(root, "go.mod"), "module example.com/commerce\n\ngo 1.22\n")
		writeWorkspaceFile(filepath.Join(root, "internal", "pricing", "price.go"), `package pricing

func CalculateTotal(cents int) int {
	return cents + 100
}
`)
		writeWorkspaceFile(filepath.Join(root, "internal", "checkout", "service.go"), `package checkout

import "example.com/commerce/internal/pricing"

func Checkout(cents int) int {
	return pricing.CalculateTotal(cents)
}
`)
		writeWorkspaceFile(filepath.Join(root, "internal", "checkout", "service_test.go"), `package checkout

import "testing"

func TestCheckout(t *testing.T) {
	if got := Checkout(400); got != 500 {
		t.Fatalf("Checkout() = %d, want 500", got)
	}
}
`)
		writeWorkspaceFile(filepath.Join(root, "scripts", "release.py"), `def calculate_total(cents: int) -> int:
    return cents + 100
`)
		writeWorkspaceFile(filepath.Join(root, "web", "release.ts"), `export function calculateTotal(cents: number): number {
  return cents + 100;
}
`)
		writeWorkspaceFile(filepath.Join(root, "web", "release.js"), `export function calculateTotal(cents) {
  return cents + 100;
}
`)
		writeWorkspaceFile(filepath.Join(root, "native", "release.cpp"), `int calculate_total(int cents) {
  return cents + 100;
}
`)
		writeWorkspaceFile(filepath.Join(root, "cli", "release.rs"), `pub fn calculate_total(cents: i32) -> i32 {
    cents + 100
}
`)
		writeWorkspaceFile(filepath.Join(root, "service", "Release.java"), `package service;

final class Release {
    static int calculateTotal(int cents) {
        return cents + 100;
    }
}
`)
		writeWorkspaceFile(filepath.Join(root, "game", "release.gd"), `func calculate_total(cents: int) -> int:
    return cents + 100
`)
		workspaceConfig := config.Workspace{
			Root:                  root,
			CWD:                   root,
			Shell:                 "/bin/sh",
			CommandTimeoutSeconds: 30,
			MaxOutputBytes:        1 << 20,
			MaxFileBytes:          1 << 20,
			MaxDirectoryEntries:   500,
			EnableShell:           true,
			EnableWrites:          true,
			ExternalTools: []config.ExternalToolDef{{
				Name:           "contract_check",
				Description:    "Run a configured Go contract command",
				Command:        "go",
				Args:           []string{"{subcommand}"},
				TimeoutSeconds: 10,
				InputSchema:    json.RawMessage(`{"type":"object","required":["subcommand"],"properties":{"subcommand":{"type":"string"}}}`),
			}},
		}
		manager := NewManager(workspaceConfig)
		applicationConfig := config.Default()
		applicationConfig.Workspace = workspaceConfig
		configuredSurface := ConfigureToolSurface(context.Background(), applicationConfig, ConfiguredToolSurfaceOptions{
			Manager: manager,
			Profile: ToolProfileDefault,
			List:    true,
		})
		ginkgo.DeferCleanup(configuredSurface.Close)
		gomega.Expect(configuredSurface.ExternalToolsError).NotTo(gomega.HaveOccurred())

		partial := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameContextIndex, Limit: 2})
		gomega.Expect(partial.Error).To(gomega.BeEmpty(), partial.Output)

		var partialReport struct {
			FilesIndexed int `json:"files_indexed"`
		}
		gomega.Expect(json.Unmarshal([]byte(partial.Output), &partialReport)).To(gomega.Succeed())
		gomega.Expect(partialReport.FilesIndexed).To(gomega.Equal(2))

		partialStatus := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameContextStatus})
		gomega.Expect(partialStatus.Error).To(gomega.BeEmpty(), partialStatus.Output)

		var partialStatusReport struct {
			Fresh      bool     `json:"fresh"`
			StaleFiles []string `json:"stale_files"`
		}
		gomega.Expect(json.Unmarshal([]byte(partialStatus.Output), &partialStatusReport)).To(gomega.Succeed())
		gomega.Expect(partialStatusReport.Fresh).To(gomega.BeFalse())
		gomega.Expect(partialStatusReport.StaleFiles).NotTo(gomega.BeEmpty())

		indexed := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameContextIndex})
		gomega.Expect(indexed.Error).To(gomega.BeEmpty(), indexed.Output)

		var buildReport struct {
			FilesIndexed int      `json:"files_indexed"`
			Languages    []string `json:"languages"`
		}
		gomega.Expect(json.Unmarshal([]byte(indexed.Output), &buildReport)).To(gomega.Succeed())
		gomega.Expect(buildReport.FilesIndexed).To(gomega.BeNumerically(">=", 10))
		gomega.Expect(buildReport.Languages).To(gomega.ContainElements(
			"cpp",
			"gdscript",
			"go",
			"java",
			"javascript",
			"python",
			"rust",
			"typescript",
		))
		// The non-Go fixtures prove polyglot indexing coverage. The behavioral
		// mutation and compiler-verification portion below is intentionally Go-
		// specific and does not claim execution coverage for those toolchains.

		orientation := manager.Execute(context.Background(), ToolRequest{
			Tool:       ToolNameCodeNavigate,
			Capability: "read_symbol",
			Path:       "internal/pricing/price.go",
			Pattern:    "CalculateTotal",
		})
		gomega.Expect(orientation.Error).To(gomega.BeEmpty(), orientation.Output)
		gomega.Expect(orientation.Output).To(gomega.ContainSubstring("func CalculateTotal"))

		readTarget := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "internal/pricing/price.go"})
		gomega.Expect(readTarget.Error).To(gomega.BeEmpty(), readTarget.Output)

		move := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameApplyPatch, Content: `*** Begin Patch
*** Update File: internal/pricing/price.go
*** Move to: internal/pricing/calculator.go
@@
-func CalculateTotal(cents int) int {
+func ComputeTotal(cents int) int {
 	return cents + 100
 }
*** End Patch`})
		gomega.Expect(move.Error).To(gomega.BeEmpty(), move.Output)
		gomega.Expect(move.ChangedPaths).To(gomega.ContainElements("internal/pricing/price.go", "internal/pricing/calculator.go"))

		movedOrientation := manager.Execute(context.Background(), ToolRequest{
			Tool:       ToolNameCodeNavigate,
			Capability: "read_symbol",
			Path:       "internal/pricing/calculator.go",
			Pattern:    "ComputeTotal",
		})
		gomega.Expect(movedOrientation.Error).To(gomega.BeEmpty(), movedOrientation.Output)
		gomega.Expect(movedOrientation.Output).To(gomega.ContainSubstring("func ComputeTotal"))
		gomega.Expect(movedOrientation.Output).NotTo(gomega.ContainSubstring("func CalculateTotal"))

		broken := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameSourceSanity, Path: "internal/checkout/service.go"})
		gomega.Expect(broken.Error).NotTo(gomega.BeEmpty(), broken.Output)
		gomega.Expect(broken.Error).To(gomega.ContainSubstring("CalculateTotal"))

		readCaller := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "internal/checkout/service.go"})
		gomega.Expect(readCaller.Error).To(gomega.BeEmpty(), readCaller.Output)

		repair := manager.Execute(context.Background(), ToolRequest{
			Tool:        ToolNameEditFile,
			Path:        "internal/checkout/service.go",
			OldText:     "pricing.CalculateTotal(cents)",
			NewText:     "pricing.ComputeTotal(cents)",
			Description: "update the checkout caller after the pricing symbol move",
		})
		gomega.Expect(repair.Error).To(gomega.BeEmpty(), repair.Output)

		sanity := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameSourceSanity, Path: "internal/checkout/service.go"})
		gomega.Expect(sanity.Error).To(gomega.BeEmpty(), sanity.Output)

		search := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameSearchContent, Path: "internal", Pattern: "ComputeTotal", FixedStrings: true})
		gomega.Expect(search.Error).To(gomega.BeEmpty(), search.Output)
		gomega.Expect(search.Output).To(gomega.ContainSubstring("internal/pricing/calculator.go"))
		gomega.Expect(search.Output).To(gomega.ContainSubstring("internal/checkout/service.go"))

		// The shell surface currently has a POSIX `-lc` contract. Exercise it
		// where that contract is available while keeping the rest of this
		// development journey portable to Windows hosts.
		if runtime.GOOS != "windows" {
			verification := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "go test ./..."})
			gomega.Expect(verification.Error).To(gomega.BeEmpty(), verification.Output)

			failedVerification := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "go test ./does-not-exist/..."})
			gomega.Expect(failedVerification.Error).NotTo(gomega.BeEmpty())
			gomega.Expect(failedVerification.Output).To(gomega.ContainSubstring("does-not-exist"))
		}

		externalCheck := manager.Execute(context.Background(), ToolRequest{Tool: "contract_check", Args: json.RawMessage(`{"subcommand":"version"}`)})
		gomega.Expect(externalCheck.Error).To(gomega.BeEmpty(), externalCheck.Output)
		gomega.Expect(externalCheck.Output).To(gomega.ContainSubstring("go version"))

		externalFailure := manager.Execute(context.Background(), ToolRequest{Tool: "contract_check", Args: json.RawMessage(`{"subcommand":"definitely-not-a-go-subcommand"}`)})
		gomega.Expect(externalFailure.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(externalFailure.Output).To(gomega.ContainSubstring("definitely-not-a-go-subcommand"))

		stale := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameContextStatus})
		gomega.Expect(stale.Error).To(gomega.BeEmpty(), stale.Output)

		var staleReport struct {
			Fresh        bool     `json:"fresh"`
			StaleFiles   []string `json:"stale_files"`
			MissingFiles []string `json:"missing_files"`
		}
		gomega.Expect(json.Unmarshal([]byte(stale.Output), &staleReport)).To(gomega.Succeed())
		gomega.Expect(staleReport.Fresh).To(gomega.BeFalse())
		gomega.Expect(append(staleReport.StaleFiles, staleReport.MissingFiles...)).To(gomega.ContainElement("internal/pricing/price.go"))

		pack := manager.Execute(context.Background(), ToolRequest{
			Tool:              ToolNameContextPack,
			Task:              "finish the CalculateTotal to ComputeTotal pricing refactor",
			Path:              "internal",
			Language:          "go",
			RequiredEditPaths: []string{"internal/pricing/calculator.go", "internal/checkout/service.go"},
			RequiredTests:     []string{"go test ./..."},
			FreshnessMaxFiles: 10,
		})
		gomega.Expect(pack.Error).To(gomega.BeEmpty(), pack.Output)
		gomega.Expect(pack.Output).NotTo(gomega.ContainSubstring("return cents + 100"))

		var packReport struct {
			Fresh       bool `json:"fresh"`
			AutoRefresh struct {
				Attempted  bool `json:"attempted"`
				FreshAfter bool `json:"fresh_after"`
			} `json:"auto_refresh"`
		}
		gomega.Expect(json.Unmarshal([]byte(pack.Output), &packReport)).To(gomega.Succeed())
		gomega.Expect(packReport.AutoRefresh.Attempted).To(gomega.BeTrue())
		gomega.Expect(packReport.AutoRefresh.FreshAfter).To(gomega.BeTrue())
		gomega.Expect(packReport.Fresh).To(gomega.BeTrue())

		newSymbol := manager.Execute(context.Background(), ToolRequest{
			Tool:            ToolNameContextQuery,
			Pattern:         "ComputeTotal",
			Language:        "go",
			Resolution:      "symbol",
			FreshnessPolicy: "require_fresh",
		})
		gomega.Expect(newSymbol.Error).To(gomega.BeEmpty(), newSymbol.Output)
		gomega.Expect(newSymbol.Output).To(gomega.ContainSubstring("symbol://internal/pricing/calculator.go#ComputeTotal"))
		gomega.Expect(newSymbol.Output).NotTo(gomega.ContainSubstring("symbol://internal/pricing/price.go#CalculateTotal"))
	})

	ginkgo.It("contains symlink escapes and canonical protected targets while preserving ordinary in-workspace aliases", func() {
		root := ginkgo.GinkgoT().TempDir()
		outside := ginkgo.GinkgoT().TempDir()
		outsideBody := "OUTSIDE_REPOSITORY_SECRET_MUST_NOT_LEAK"
		protectedBody := "PROTECTED_WORKSPACE_SECRET_MUST_NOT_LEAK"
		attemptedReplacement := "ATTEMPTED_REPLACEMENT_MUST_NOT_LEAK"
		publicBody := "PUBLIC_WORKSPACE_GUIDE"

		writeWorkspaceFile(filepath.Join(outside, "customer.env"), outsideBody+"\n")
		writeWorkspaceFile(filepath.Join(root, "secrets", "token.txt"), protectedBody+"\n")
		writeWorkspaceFile(filepath.Join(root, "docs", "guide.txt"), publicBody+"\n")
		if err := os.Symlink(outside, filepath.Join(root, "linked-dependency")); err != nil {
			ginkgo.Skip(fmt.Sprintf("symlinks unavailable: %v", err))
		}
		gomega.Expect(os.Symlink("secrets", filepath.Join(root, "dependency-cache"))).To(gomega.Succeed())
		gomega.Expect(os.Symlink(filepath.Join("secrets", "token.txt"), filepath.Join(root, "cached-token.txt"))).To(gomega.Succeed())
		gomega.Expect(os.Symlink(filepath.Join("docs", "guide.txt"), filepath.Join(root, "linked-guide.txt"))).To(gomega.Succeed())

		manager := NewManager(config.Workspace{
			Root:                root,
			CWD:                 root,
			MaxFileBytes:        1 << 20,
			MaxOutputBytes:      1 << 20,
			MaxDirectoryEntries: 100,
			EnableWrites:        true,
			ToolRules: map[string][]config.ToolRule{
				ToolNameReadFile:           {{Pattern: "secrets/**", Decision: "deny"}},
				ToolNameListFiles:          {{Pattern: "secrets/**", Decision: "deny"}},
				ToolNameSearchContent:      {{Pattern: "secrets/**", Decision: "deny"}},
				ToolNameEditFile:           {{Pattern: "secrets/**", Decision: "deny"}},
				ToolNameApplyPatch:         {{Pattern: "secrets/**", Decision: "deny"}},
				ToolNameGoMoveDeclarations: {{Pattern: "secrets/**", Decision: "deny"}},
			},
		})
		manager.SetPermissionRules(map[string][]toolpolicy.Rule{
			ToolNameFileStats: {{Pattern: "secrets/**", Decision: toolpolicy.Ask}},
		})
		gomega.Expect(manager.EvaluateToolRule(ToolRequest{Tool: ToolNameFileStats, Path: "dependency-cache/token.txt"})).To(gomega.Equal(toolpolicy.Ask))

		escapeResults := []ToolResult{
			manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "linked-dependency/customer.env"}),
			manager.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "linked-dependency", Recursive: true}),
			manager.Execute(context.Background(), ToolRequest{Tool: ToolNameSearchContent, Path: "linked-dependency", Pattern: "SECRET"}),
		}
		discoveryEscape := manager.Execute(context.Background(), ToolRequest{
			Tool:     ToolNameDependencyGraph,
			Path:     "linked-dependency",
			Language: "go",
		})
		gomega.Expect(discoveryEscape.Error).NotTo(gomega.BeEmpty(), "dependency discovery followed an escaping symlink: %#v", discoveryEscape)
		gomega.Expect(discoveryEscape.Error).To(gomega.ContainSubstring("escapes workspace root"))
		gomega.Expect(strings.Join([]string{discoveryEscape.Summary, discoveryEscape.Output, discoveryEscape.Error}, "\n")).NotTo(gomega.ContainSubstring(outsideBody))
		directProtected := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "secrets/token.txt"})
		gomega.Expect(directProtected.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(directProtected.Summary).To(gomega.ContainSubstring("workspace tool_rules"))
		gomega.Expect(directProtected.Error).To(gomega.ContainSubstring("secrets/token.txt"))
		gomega.Expect(strings.Join([]string{directProtected.Summary, directProtected.Output, directProtected.Error}, "\n")).NotTo(gomega.ContainSubstring(protectedBody))
		directProtectedPatch := manager.Execute(context.Background(), ToolRequest{
			Tool:        ToolNameApplyPatch,
			Description: "attempt a direct protected patch",
			Content:     "*** Begin Patch\n*** Update File: secrets/token.txt\n@@\n-" + protectedBody + "\n+" + attemptedReplacement + "\n*** End Patch",
		})
		gomega.Expect(directProtectedPatch.Error).To(gomega.ContainSubstring("secrets/token.txt"))
		directProtectedMove := manager.Execute(context.Background(), ToolRequest{
			Tool:        ToolNameGoMoveDeclarations,
			Path:        "docs/source.go",
			Destination: "secrets/token.txt",
		})
		gomega.Expect(directProtectedMove.Error).To(gomega.ContainSubstring("secrets/token.txt"))

		protectedAliasResults := []ToolResult{
			manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "dependency-cache/token.txt"}),
			manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "cached-token.txt"}),
			manager.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "cached-token.txt"}),
			manager.Execute(context.Background(), ToolRequest{Tool: ToolNameSearchContent, Path: "cached-token.txt", Pattern: "SECRET"}),
			manager.Execute(context.Background(), ToolRequest{
				Tool:        ToolNameEditFile,
				Path:        "cached-token.txt",
				OldText:     protectedBody,
				NewText:     attemptedReplacement,
				Description: "attempt a denied edit through a workspace alias",
			}),
			manager.Execute(context.Background(), ToolRequest{
				Tool:        ToolNameApplyPatch,
				Description: "attempt a protected patch through an alias",
				Content:     "*** Begin Patch\n*** Update File: cached-token.txt\n@@\n-" + protectedBody + "\n+" + attemptedReplacement + "\n*** End Patch",
			}),
			manager.Execute(context.Background(), ToolRequest{
				Tool:        ToolNameApplyPatch,
				Description: "attempt a mixed public and protected patch",
				Content:     "*** Begin Patch\n*** Update File: docs/guide.txt\n@@\n-" + publicBody + "\n+changed public guide\n*** Update File: cached-token.txt\n@@\n-" + protectedBody + "\n+" + attemptedReplacement + "\n*** End Patch",
			}),
			manager.Execute(context.Background(), ToolRequest{
				Tool:        ToolNameGoMoveDeclarations,
				Path:        "docs/source.go",
				Destination: "docs/first.go",
				Moves: []GoDeclarationMove{
					{Destination: "docs/second.go"},
					{Destination: "cached-token.txt"},
				},
			}),
		}

		for _, result := range escapeResults {
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), "unexpected allowed result: %#v", result)
			publicResult := strings.Join([]string{result.Summary, result.Output, result.Error}, "\n")
			for _, forbidden := range []string{outsideBody, protectedBody, attemptedReplacement, outside} {
				gomega.Expect(publicResult).NotTo(gomega.ContainSubstring(forbidden))
			}
		}
		gomega.Expect(slices.ContainsFunc(escapeResults, func(result ToolResult) bool {
			return strings.Contains(result.Error, "symlink") || strings.Contains(result.Error, "has not been approved")
		})).To(gomega.BeTrue())

		for _, result := range protectedAliasResults {
			gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), "canonical protected target was allowed: %#v", result)
			gomega.Expect(result.Summary).To(gomega.ContainSubstring("workspace tool_rules"))
			publicResult := strings.Join([]string{result.Summary, result.Output, result.Error}, "\n")
			for _, forbidden := range []string{protectedBody, attemptedReplacement, root, "secrets/token.txt"} {
				gomega.Expect(publicResult).NotTo(gomega.ContainSubstring(forbidden))
			}
		}

		ordinaryRead := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameReadFile, Path: "linked-guide.txt"})
		ordinaryList := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameListFiles, Path: "linked-guide.txt"})
		ordinarySearch := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameSearchContent, Path: "linked-guide.txt", Pattern: publicBody, FixedStrings: true})
		for _, result := range []ToolResult{ordinaryRead, ordinaryList, ordinarySearch} {
			gomega.Expect(result.Error).To(gomega.BeEmpty(), "ordinary workspace alias was rejected: %#v", result)
		}
		gomega.Expect(ordinaryRead.Output).To(gomega.ContainSubstring(publicBody))
		gomega.Expect(ordinaryList.Output).To(gomega.ContainSubstring("linked-guide.txt"))
		gomega.Expect(ordinarySearch.Output).To(gomega.ContainSubstring(publicBody))

		unchanged, err := os.ReadFile(filepath.Join(root, "secrets", "token.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(unchanged)).To(gomega.Equal(protectedBody + "\n"))
		publicUnchanged, err := os.ReadFile(filepath.Join(root, "docs", "guide.txt"))
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(string(publicUnchanged)).To(gomega.Equal(publicBody + "\n"))

		unavailableRoot := filepath.Join(root, "missing-workspace-root")
		unavailableAccess := NewPathAccess(config.Workspace{Root: unavailableRoot, CWD: unavailableRoot})
		gomega.Expect(unavailableAccess.PathWithinWorkspace(filepath.Join(unavailableRoot, "lexically-inside.txt"))).To(gomega.BeFalse())
	})
})
