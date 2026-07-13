package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
)

var _ = ginkgo.Describe("verification observation", func() {
	ginkgo.It("shell tool attaches command facts", func() {
		root := ginkgo.GinkgoT().TempDir()
		mgr := NewManager(config.Workspace{
			Root:        root,
			CWD:         root,
			Shell:       "/bin/sh",
			EnableShell: true,
		})

		result := mgr.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: "printf ok"})
		gomega.Expect(result.Error).To(gomega.Equal(""), fmt.Sprintf("shell command failed: %#v", result))
		gomega.Expect(result.Facts).NotTo(gomega.BeNil(), fmt.Sprintf("shell result did not attach facts: %#v", result))
		gomega.Expect(result.Facts.ShellCommand).To(gomega.Equal("printf ok"), fmt.Sprintf("ShellCommand = %q, want command from request", result.Facts.ShellCommand))
		gomega.Expect(result.Facts.ShellWorkdir).To(gomega.Equal("."), fmt.Sprintf("ShellWorkdir = %q, want workspace root", result.Facts.ShellWorkdir))

	})
})
var _ = ginkgo.Describe("analyze verification request classifies verification tools", func() {

	tests := []struct {
		name      string
		req       ToolRequest
		attempted bool
	}{
		{
			name:      "shell check command",
			req:       ToolRequest{Tool: ToolNameShell, Command: "git diff --check"},
			attempted: true,
		},
		{
			name:      "full suite command",
			req:       ToolRequest{Tool: ToolNameShell, Command: "go test ./..."},
			attempted: true,
		},
		{
			name:      "flagged Python unittest command",
			req:       ToolRequest{Tool: ToolNameShell, Command: "python3 -B -m unittest"},
			attempted: true,
		},
		{
			name:      "read only shell is not verification",
			req:       ToolRequest{Tool: ToolNameShell, Command: "git status --short"},
			attempted: false,
		},
		{
			name:      "source sanity",
			req:       ToolRequest{Tool: ToolNameSourceSanity, Path: "internal/app.go"},
			attempted: true,
		},
		{
			name:      "diagnostic tool",
			req:       ToolRequest{Tool: "go_diagnostics", Path: "internal/app.go"},
			attempted: true,
		},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			got := AnalyzeVerificationRequest(tt.req)
			gomega.Expect(got.Attempted).To(gomega.Equal(tt.attempted), fmt.Sprintf("Attempted = %v, want %v", got.Attempted, tt.attempted))

		})
	}

	ginkgo.It("uses the executed Python target rather than verification-looking argv", func() {
		observed := AnalyzeVerificationRequest(ToolRequest{Tool: ToolNameShell, Command: "python3 -B -m unittest"})
		gomega.Expect(observed.Attempted).To(gomega.BeTrue())
		gomega.Expect(observed.BuildOrTest).To(gomega.BeTrue())
		gomega.Expect(observed.Verification).To(gomega.Equal(ShellVerificationFullSuite))
		gomega.Expect(observed.FullSuite).To(gomega.BeTrue())
		gomega.Expect(observed.Ecosystem).To(gomega.Equal(VerificationEcosystemPython))

		postWrite := AnalyzePostWriteVerificationResult(ToolResult{
			Tool: ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{
				ShellCommand: "python3 -B -m unittest",
				ShellWorkdir: ".",
			},
		}, []string{"labels/labels.py", "internal/labels/labels.go"})
		gomega.Expect(postWrite.Coverage).To(gomega.Equal(VerificationCoverageFullSuite))
		gomega.Expect(postWrite.ScopePaths).To(gomega.Equal([]string{"."}))
		gomega.Expect(postWrite.CoveredPendingPaths).To(gomega.Equal([]string{"labels/labels.py"}))
		gomega.Expect(postWrite.Successful).To(gomega.BeTrue())

		for _, spoof := range []string{
			`python3 -c "print('ready')" -m unittest`,
			"python3 scripts/format.py -m unittest",
			"python3 -V -m unittest",
		} {
			got := AnalyzeVerificationRequest(ToolRequest{Tool: ToolNameShell, Command: spoof})
			gomega.Expect(got.Attempted).To(gomega.BeFalse(), fmt.Sprintf("unused argv must not become Python verification: %q -> %#v", spoof, got))
		}
	})

	ginkgo.It("recognizes only the exact canonical HTML parser request", func() {
		command := HTMLParserVerificationCommand("web/index.html")
		got := AnalyzeVerificationRequest(ToolRequest{
			Tool:    ToolNameShell,
			Command: command,
			Workdir: "site",
		})

		gomega.Expect(got.Attempted).To(gomega.BeTrue(), fmt.Sprintf("canonical HTML parser request was not recognized: %#v", got))
		gomega.Expect(got.Verification).To(gomega.Equal(ShellVerificationCheck))
		gomega.Expect(got.Coverage).To(gomega.Equal(VerificationCoverageExactHTMLPaths))
		gomega.Expect(got.Workdir).To(gomega.Equal("site"))
		gomega.Expect(got.ScopePaths).To(gomega.Equal([]string{"site/web/index.html"}))

		for _, spoof := range []string{
			`python3 -c "print('HTML parsed OK: web/index.html')"`,
			`printf 'from html.parser import HTMLParser; HTML parsed OK: web/index.html'`,
			command + "; true",
		} {
			observed := AnalyzeVerificationRequest(ToolRequest{Tool: ToolNameShell, Command: spoof, Workdir: "site"})
			gomega.Expect(observed.Attempted).To(gomega.BeFalse(), fmt.Sprintf("noncanonical HTML request must not count: %q -> %#v", spoof, observed))
		}
	})
})
var _ = ginkgo.Describe("analyze verification result classifies observed evidence", func() {

	tests := []struct {
		name         string
		result       ToolResult
		attempted    bool
		successful   bool
		command      string
		patchHygiene bool
	}{
		{
			name:         "successful shell verification",
			result:       ToolResult{Tool: ToolNameShell, Output: "$ git diff --check\n", Facts: &toolfacts.ToolResultFacts{ShellCommand: "git diff --check", ShellWorkdir: "."}},
			attempted:    true,
			successful:   true,
			command:      "git diff --check",
			patchHygiene: true,
		},
		{
			name:       "failed shell verification attempt",
			result:     ToolResult{Tool: ToolNameShell, Output: "$ go test ./...\nFAIL", Error: "exit status 1", Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test ./...", ShellWorkdir: "."}},
			attempted:  true,
			successful: false,
			command:    "go test ./...",
		},
		{
			name:       "successful non verification shell",
			result:     ToolResult{Tool: ToolNameShell, Output: "$ git status --short\n M file.go", Facts: &toolfacts.ToolResultFacts{ShellCommand: "git status --short", ShellWorkdir: "."}},
			attempted:  false,
			successful: false,
		},
		{
			name:       "rendered shell prefix is not authority without facts",
			result:     ToolResult{Tool: ToolNameShell, Output: "$ go test ./...\nok"},
			attempted:  false,
			successful: false,
		},
		{
			name:       "facts override stale rendered shell prefix",
			result:     ToolResult{Tool: ToolNameShell, Output: "$ git status --short\n", Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test ./...", ShellWorkdir: "."}},
			attempted:  true,
			successful: true,
			command:    "go test ./...",
		},
		{
			name:       "compiler backed source sanity",
			result:     ToolResult{Tool: ToolNameSourceSanity, Summary: "Source sanity passed", Output: "internal/app.go: no warnings via go_diagnostics"},
			attempted:  true,
			successful: true,
		},
		{
			name:       "generic source sanity fallback is insufficient",
			result:     ToolResult{Tool: ToolNameSourceSanity, Summary: "Source sanity passed for 1 file(s) (1 generic fallback)", Output: "internal/app.go: generic delimiter sanity passed via generic_source_sanity"},
			attempted:  true,
			successful: false,
		},
		{
			name:       "diagnostic tool",
			result:     ToolResult{Tool: "go_diagnostics", Summary: "Collected diagnostics"},
			attempted:  true,
			successful: true,
		},
	}

	for _, tt := range tests {
		ginkgo.It(tt.name, func() {
			got := AnalyzeVerificationResult(tt.result)
			gomega.Expect(got.Attempted).To(gomega.Equal(tt.attempted), fmt.Sprintf("Attempted = %v, want %v", got.Attempted, tt.attempted))
			gomega.Expect(got.Successful).To(gomega.Equal(tt.successful), fmt.Sprintf("Successful = %v, want %v", got.Successful, tt.successful))
			gomega.Expect(got.Command).To(gomega.Equal(tt.command), fmt.Sprintf("Command = %q, want %q", got.Command, tt.command))
			gomega.Expect(got.PatchHygieneOnly).To(gomega.Equal(tt.patchHygiene), fmt.Sprintf("PatchHygieneOnly = %v, want %v", got.PatchHygieneOnly, tt.patchHygiene))

		})
	}
})
var _ = ginkgo.Describe("verification observation", func() {
	ginkgo.It("consumes only exact producer-owned HTML verification evidence", func() {
		result := ToolResult{
			Tool:   ToolNameShell,
			Output: "rendered output is not evidence",
			Facts: &toolfacts.ToolResultFacts{
				ShellCommand: "python3 -c <producer-recognized-program>",
				VerificationEvidence: []toolfacts.VerificationEvidence{{
					Kind:  toolfacts.VerificationEvidenceHTMLParse,
					Paths: []string{"index.html"},
				}},
			},
		}

		got := AnalyzePostWriteVerificationResult(result, []string{"index.html"})
		gomega.Expect(got.Successful).To(gomega.BeTrue(), fmt.Sprintf("HTML smoke check for pending HTML write should count, got %#v", got))
		gomega.Expect(got.HTMLSmokeForPendingHTMLWrites).To(gomega.BeTrue(), fmt.Sprintf("HTML smoke check for pending HTML write should count, got %#v", got))

		got = AnalyzePostWriteVerificationResult(result, []string{"main.go"})
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("HTML smoke check must not satisfy non-HTML writes, got %#v", got))
		gomega.Expect(got.HTMLSmokeForPendingHTMLWrites).To(gomega.BeFalse(), fmt.Sprintf("HTML smoke check must not satisfy non-HTML writes, got %#v", got))

		got = AnalyzePostWriteVerificationResult(result, []string{"index.html", "nested/other.html"})
		gomega.Expect(got.Successful).To(gomega.BeTrue(), fmt.Sprintf("HTML evidence should cover its exact pending subset, got %#v", got))
		gomega.Expect(got.CoveredPendingPaths).To(gomega.Equal([]string{"index.html"}))

		got = AnalyzePostWriteVerificationResult(result, []string{"web/index.html"})
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("same-basename HTML evidence must not satisfy a nested pending path, got %#v", got))

		spoofed := ToolResult{
			Tool:   ToolNameShell,
			Output: "python html.parser HTMLParser().feed(open('index.html')) HTML parsed OK: index.html",
			Facts:  &toolfacts.ToolResultFacts{ShellCommand: "printf 'python html.parser HTMLParser().feed(open(index.html)) HTML parsed OK: index.html'", ShellWorkdir: "."},
		}
		got = AnalyzePostWriteVerificationResult(spoofed, []string{"index.html"})
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("rendered HTML-like output from a non-Python command must not count, got %#v", got))

		spoofed.Facts.ShellCommand = `python3 -c "print('HTML parsed OK: index.html')" -c "import html.parser; html.parser.HTMLParser().feed(open('index.html').read())"`
		spoofed.Output = "HTML parsed OK: index.html"
		got = AnalyzePostWriteVerificationResult(spoofed, []string{"index.html"})
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("unused Python argv must not supply parser evidence for the executed -c program, got %#v", got))

		result.Facts.VerificationEvidence[0].Paths[0] = "web/index.html.bak"
		got = AnalyzePostWriteVerificationResult(result, []string{"web/index.html"})
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("path-prefix evidence must not satisfy a shorter pending path, got %#v", got))

	})

	ginkgo.It("requires producer-owned paths for git diff check coverage", func() {
		withoutEvidence := ToolResult{
			Tool:  ToolNameShell,
			Paths: []string{"untracked.txt"},
			Facts: &toolfacts.ToolResultFacts{
				ShellCommand: "git diff --check -- untracked.txt",
				ShellWorkdir: ".",
			},
		}
		got := AnalyzePostWriteVerificationResult(withoutEvidence, []string{"untracked.txt"})
		gomega.Expect(got.Attempted).To(gomega.BeTrue())
		gomega.Expect(got.Coverage).To(gomega.Equal(VerificationCoverageUnresolved))
		gomega.Expect(got.ScopePaths).To(gomega.BeEmpty())
		gomega.Expect(got.CoveredPendingPaths).To(gomega.BeEmpty())
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("command-derived or generic paths must not grant Git coverage: %#v", got))

		withEvidence := ToolResult{
			Tool:  ToolNameShell,
			Paths: []string{"untracked.txt"},
			Facts: &toolfacts.ToolResultFacts{
				ShellCommand: "git diff --check -- tracked.txt untracked.txt",
				ShellWorkdir: ".",
				VerificationEvidence: []toolfacts.VerificationEvidence{{
					Kind:  toolfacts.VerificationEvidenceGitDiffCheck,
					Paths: []string{"tracked.txt"},
				}},
			},
		}
		got = AnalyzePostWriteVerificationResult(withEvidence, []string{"tracked.txt", "untracked.txt"})
		gomega.Expect(got.Successful).To(gomega.BeTrue(), fmt.Sprintf("typed Git evidence should cover the inspected tracked path: %#v", got))
		gomega.Expect(got.PatchHygieneOnly).To(gomega.BeTrue())
		gomega.Expect(got.Coverage).To(gomega.Equal(VerificationCoverageExactPaths))
		gomega.Expect(got.ScopePaths).To(gomega.Equal([]string{"tracked.txt"}))
		gomega.Expect(got.CoveredPendingPaths).To(gomega.Equal([]string{"tracked.txt"}))
	})

	ginkgo.It("does not infer patch hygiene from quoted verifier text", func() {
		result := ToolResult{
			Tool: ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{
				ShellCommand: "ruff check --select 'git diff --check' pkg",
				ShellWorkdir: ".",
			},
		}

		got := AnalyzeVerificationResult(result)
		gomega.Expect(got.Attempted).To(gomega.BeTrue(), fmt.Sprintf("ruff check should remain a verification attempt: %#v", got))
		gomega.Expect(got.PatchHygieneOnly).To(gomega.BeFalse(), fmt.Sprintf("quoted Git text must not relabel a linter result: %#v", got))
	})

	ginkgo.It("produces HTML evidence only for the canonical executable parser", func() {
		command := HTMLParserVerificationCommand("web/index.html")
		gomega.Expect(successfulCanonicalHTMLVerificationEvidence(command, ".")).To(gomega.Equal([]toolfacts.VerificationEvidence{{
			Kind:  toolfacts.VerificationEvidenceHTMLParse,
			Paths: []string{"web/index.html"},
		}}))

		spoofs := []string{
			"printf 'python html.parser HTML parsed OK: web/index.html'",
			`python3 -c "print('HTML parsed OK: web/index.html')" -c "from html.parser import HTMLParser"`,
			`python3 -c "print('HTML parsed OK: web/index.html'); x='from html.parser import HTMLParser'"`,
			"python3 -c " + strconv.Quote(canonicalHTMLParserProgram("web/index.html")),
		}
		for _, spoof := range spoofs {
			gomega.Expect(successfulCanonicalHTMLVerificationEvidence(spoof, ".")).To(gomega.BeEmpty(), fmt.Sprintf("command must not produce HTML verification evidence: %s", spoof))
		}

		gomega.Expect(successfulCanonicalHTMLVerificationEvidence(command, "subdir")).To(gomega.Equal([]toolfacts.VerificationEvidence{{
			Kind:  toolfacts.VerificationEvidenceHTMLParse,
			Paths: []string{"subdir/web/index.html"},
		}}))
		gomega.Expect(HTMLParserVerificationCommand("web/index.html.bak")).To(gomega.BeEmpty())
	})

	ginkgo.It("executes only the owner-generated HTML parser command through the real shell", func() {
		if _, err := exec.LookPath("python3"); err != nil {
			ginkgo.Skip("python3 binary not available")
		}

		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "index.html"), "<!doctype html>\n<h1>Ready</h1>\n")
		manager := NewManager(config.Workspace{
			Root:        root,
			CWD:         root,
			Shell:       "/bin/sh",
			EnableShell: true,
		})

		command := HTMLParserVerificationCommand("index.html")
		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: command})
		gomega.Expect(result.Error).To(gomega.BeEmpty(), fmt.Sprintf("owner-generated HTML verifier failed: %#v", result))
		gomega.Expect(result.Facts.VerificationEvidence).To(gomega.Equal([]toolfacts.VerificationEvidence{{
			Kind:  toolfacts.VerificationEvidenceHTMLParse,
			Paths: []string{"index.html"},
		}}))

		literalEscapes := "python3 -c " + strconv.Quote(canonicalHTMLParserProgram("index.html"))
		gomega.Expect(literalEscapes).NotTo(gomega.Equal(command))
		gomega.Expect(AnalyzeVerificationRequest(ToolRequest{Tool: ToolNameShell, Command: literalEscapes}).Attempted).To(gomega.BeFalse())
		result = manager.Execute(context.Background(), ToolRequest{Tool: ToolNameShell, Command: literalEscapes})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty(), "literal \\n escapes are not Python source when passed through /bin/sh")
		gomega.Expect(result.Facts.VerificationEvidence).To(gomega.BeEmpty())
	})

	ginkgo.It("accepts a targeted test only for pending paths under its scope", func() {
		result := ToolResult{
			Tool:  ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test ./internal/runtime/agent", ShellWorkdir: "."},
		}

		related := AnalyzePostWriteVerificationResult(result, []string{"internal/runtime/agent/runner_core.go"})
		gomega.Expect(related.Successful).To(gomega.BeTrue(), fmt.Sprintf("related targeted test should count, got %#v", related))
		gomega.Expect(related.RelevantToPendingWrites).To(gomega.BeTrue())
		gomega.Expect(related.Coverage).To(gomega.Equal(VerificationCoveragePathScope))
		gomega.Expect(related.ScopePaths).To(gomega.Equal([]string{"internal/runtime/agent"}))
		gomega.Expect(related.CoveredPendingPaths).To(gomega.Equal([]string{"internal/runtime/agent/runner_core.go"}))

		unrelated := AnalyzePostWriteVerificationResult(result, []string{"internal/unrelated/service.go"})
		gomega.Expect(unrelated.Successful).To(gomega.BeFalse(), fmt.Sprintf("unrelated targeted test must not count, got %#v", unrelated))
		gomega.Expect(unrelated.RelevantToPendingWrites).To(gomega.BeFalse())
		gomega.Expect(unrelated.CoveredPendingPaths).To(gomega.BeEmpty())
	})

	ginkgo.It("reports only the pending subset covered by a targeted test", func() {
		result := ToolResult{
			Tool:  ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test ./internal/runtime/agent", ShellWorkdir: "."},
		}
		pending := []string{
			"internal/runtime/agent/runner_core.go",
			"internal/capabilities/workspace/verification_observation.go",
			"web/app.ts",
		}

		got := AnalyzePostWriteVerificationResult(result, pending)
		gomega.Expect(got.Successful).To(gomega.BeTrue(), fmt.Sprintf("targeted test should cover its pending subset, got %#v", got))
		gomega.Expect(got.CoveredPendingPaths).To(gomega.Equal([]string{"internal/runtime/agent/runner_core.go"}))
	})

	ginkgo.It("does not turn relevant failed verification into success", func() {
		result := ToolResult{
			Tool:  ToolNameShell,
			Error: "exit status 1",
			Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test ./internal/runtime/agent", ShellWorkdir: "."},
		}

		got := AnalyzePostWriteVerificationResult(result, []string{"internal/runtime/agent/runner_core.go"})
		gomega.Expect(got.RelevantToPendingWrites).To(gomega.BeTrue(), fmt.Sprintf("failed targeted test should retain relevance, got %#v", got))
		gomega.Expect(got.CoveredPendingPaths).To(gomega.Equal([]string{"internal/runtime/agent/runner_core.go"}))
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("failed targeted test must not count as successful, got %#v", got))
	})

	ginkgo.It("reports a successful full suite as covering every pending path", func() {
		result := ToolResult{
			Tool:  ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test ./...", ShellWorkdir: "."},
		}
		pending := []string{
			"internal/runtime/agent/runner_core.go",
			"internal/capabilities/workspace/verification_observation.go",
		}

		got := AnalyzePostWriteVerificationResult(result, pending)
		gomega.Expect(got.Successful).To(gomega.BeTrue(), fmt.Sprintf("full suite should count, got %#v", got))
		gomega.Expect(got.Coverage).To(gomega.Equal(VerificationCoverageFullSuite))
		gomega.Expect(got.ScopePaths).To(gomega.Equal([]string{"."}))
		gomega.Expect(got.Ecosystem).To(gomega.Equal(VerificationEcosystemGo))
		gomega.Expect(got.CoveredPendingPaths).To(gomega.Equal(pending[:2]))
	})

	ginkgo.It("does not manufacture verification from a producer whose status is masked by a pipe", func() {
		command := "go test ./internal/runtime/agent | head -n 20"
		analysis := AnalyzeShellCommand(command)
		gomega.Expect(analysis.HasControlSyntax).To(gomega.BeTrue())
		gomega.Expect(analysis.SideEffect).To(gomega.Equal(autonomy.SideEffectUnknown))

		got := AnalyzeVerificationRequest(ToolRequest{
			Tool:    ToolNameShell,
			Command: command,
			Workdir: ".",
		})
		gomega.Expect(got.Attempted).To(gomega.BeFalse())
		gomega.Expect(got.Verification).To(gomega.Equal(ShellVerificationNone))
		gomega.Expect(got.ScopePaths).To(gomega.BeEmpty())
		gomega.Expect(got.Coverage).To(gomega.Equal(VerificationCoverageNone))
	})

	ginkgo.It("resolves suite coverage against workdir and artifact ecosystem", func() {
		result := ToolResult{
			Tool: ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{
				ShellCommand: "go test ./...",
				ShellWorkdir: "internal/runtime/agent",
			},
		}
		pending := []string{
			"internal/runtime/agent/runner_core.go",
			"internal/capabilities/workspace/verification_observation.go",
			"internal/runtime/agent/panel.ts",
		}

		got := AnalyzePostWriteVerificationResult(result, pending)
		gomega.Expect(got.ScopePaths).To(gomega.Equal([]string{"internal/runtime/agent"}))
		gomega.Expect(got.CoveredPendingPaths).To(gomega.Equal([]string{"internal/runtime/agent/runner_core.go"}))
		gomega.Expect(got.Successful).To(gomega.BeTrue())
	})

	ginkgo.It("applies ecosystem filtering to targeted linters and tests", func() {
		pending := []string{"pkg/service.go", "pkg/service.ts", "pkg/service.py"}
		tests := []struct {
			command string
			covered []string
		}{
			{command: "go test ./pkg", covered: []string{"pkg/service.go"}},
			{command: "ruff check pkg", covered: []string{"pkg/service.py"}},
			{command: "eslint pkg", covered: []string{"pkg/service.ts"}},
		}

		for _, tt := range tests {
			result := ToolResult{Tool: ToolNameShell, Facts: &toolfacts.ToolResultFacts{ShellCommand: tt.command, ShellWorkdir: "."}}
			got := AnalyzePostWriteVerificationResult(result, pending)
			gomega.Expect(got.CoveredPendingPaths).To(gomega.Equal(tt.covered), fmt.Sprintf("command %q got %#v", tt.command, got))
		}
	})

	ginkgo.It("does not derive full-suite coverage from quoted or flag text", func() {
		pending := []string{"internal/runtime/agent/runner_core.go"}

		quoted := ToolResult{
			Tool:  ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{ShellCommand: "printf 'go test ./...'", ShellWorkdir: "."},
		}
		got := AnalyzePostWriteVerificationResult(quoted, pending)
		gomega.Expect(got.Attempted).To(gomega.BeFalse(), fmt.Sprintf("quoted command text must not become verification, got %#v", got))
		gomega.Expect(got.Successful).To(gomega.BeFalse())

		flagText := ToolResult{
			Tool:  ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test -run 'go test ./...' ./internal/unrelated", ShellWorkdir: "."},
		}
		got = AnalyzePostWriteVerificationResult(flagText, pending)
		gomega.Expect(got.FullSuite).To(gomega.BeFalse(), fmt.Sprintf("-run value must not become full-suite target, got %#v", got))
		gomega.Expect(got.ScopePaths).To(gomega.Equal([]string{"internal/unrelated"}))
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("unrelated actual target must not cover pending write, got %#v", got))

		narrowed := ToolResult{Tool: ToolNameShell, Facts: &toolfacts.ToolResultFacts{ShellCommand: "go test -run TestOnly ./...", ShellWorkdir: "."}}
		got = AnalyzePostWriteVerificationResult(narrowed, pending)
		gomega.Expect(got.FullSuite).To(gomega.BeFalse())
		gomega.Expect(got.ScopePaths).To(gomega.Equal([]string{"."}))
		gomega.Expect(got.Successful).To(gomega.BeTrue(), fmt.Sprintf("executed focused suite should retain Go path scope, got %#v", got))
	})

	ginkgo.It("retains target scope for successful focused Go tests", func() {
		result := ToolResult{
			Tool: ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{
				ShellCommand: "go test -run TestX ./pkg",
				ShellWorkdir: ".",
			},
		}

		related := AnalyzePostWriteVerificationResult(result, []string{"pkg/service.go"})
		gomega.Expect(related.FullSuite).To(gomega.BeFalse())
		gomega.Expect(related.CoveredPendingPaths).To(gomega.Equal([]string{"pkg/service.go"}))
		gomega.Expect(related.Successful).To(gomega.BeTrue())

		unrelated := AnalyzePostWriteVerificationResult(result, []string{"other/service.go"})
		gomega.Expect(unrelated.CoveredPendingPaths).To(gomega.BeEmpty())
		gomega.Expect(unrelated.Successful).To(gomega.BeFalse())
	})

	ginkgo.It("withholds path scope from non-executing Go test modes", func() {
		for _, command := range []string{"go test -c ./pkg", "go test -count=0 ./pkg", "go test -list TestX ./pkg", "go test -n ./pkg", "go test -exec=true ./pkg", "go test -exec /usr/bin/true ./pkg", "go build -n ./pkg", "go vet -n ./pkg"} {
			result := ToolResult{Tool: ToolNameShell, Facts: &toolfacts.ToolResultFacts{ShellCommand: command, ShellWorkdir: "."}}
			got := AnalyzePostWriteVerificationResult(result, []string{"pkg/service.go"})
			gomega.Expect(got.FullSuite).To(gomega.BeFalse(), fmt.Sprintf("non-executing command %q got %#v", command, got))
			gomega.Expect(got.ScopePaths).To(gomega.BeEmpty(), fmt.Sprintf("non-executing command %q got %#v", command, got))
			gomega.Expect(got.Successful).To(gomega.BeFalse())
		}
	})

	ginkgo.It("uses only structured diagnostic paths as post-write scope", func() {
		pending := []string{"internal/runtime/agent/runner_core.go"}
		tests := []struct {
			name       string
			result     ToolResult
			successful bool
		}{
			{
				name:       "result path covers pending file",
				result:     ToolResult{Tool: "go_diagnostics", Paths: []string{"internal/runtime/agent"}},
				successful: true,
			},
			{
				name: "diagnostic fact covers pending file",
				result: ToolResult{
					Tool:  "go_diagnostics",
					Facts: &toolfacts.ToolResultFacts{DiagnosticEvidencePaths: []string{"internal/runtime/agent/runner_core.go"}},
				},
				successful: true,
			},
			{
				name:       "unrelated diagnostic path does not cover pending file",
				result:     ToolResult{Tool: "go_diagnostics", Paths: []string{"internal/unrelated"}},
				successful: false,
			},
			{
				name:       "rendered output without structured paths is not scope authority",
				result:     ToolResult{Tool: "go_diagnostics", Output: "internal/runtime/agent/runner_core.go: no diagnostics"},
				successful: false,
			},
		}

		for _, tt := range tests {
			got := AnalyzePostWriteVerificationResult(tt.result, pending)
			gomega.Expect(got.Successful).To(gomega.Equal(tt.successful), fmt.Sprintf("%s: got %#v", tt.name, got))
		}
	})

	ginkgo.It("scopes source sanity through diagnostic facts", func() {
		result := ToolResult{
			Tool:    ToolNameSourceSanity,
			Summary: "Source sanity passed",
			Output:  "internal/runtime/agent/runner_core.go: no warnings via go_diagnostics",
			Facts: &toolfacts.ToolResultFacts{
				DiagnosticEvidencePaths: []string{"internal/runtime/agent/runner_core.go"},
			},
		}

		related := AnalyzePostWriteVerificationResult(result, []string{"internal/runtime/agent/runner_core.go"})
		gomega.Expect(related.Successful).To(gomega.BeTrue(), fmt.Sprintf("scoped source sanity should count, got %#v", related))

		unrelated := AnalyzePostWriteVerificationResult(result, []string{"internal/unrelated/service.go"})
		gomega.Expect(unrelated.Successful).To(gomega.BeFalse(), fmt.Sprintf("unrelated source sanity must not count, got %#v", unrelated))
	})

	ginkgo.It("preserves failed source sanity attempt scope without parsing errors", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "component.astro"), "<main>content</main>\n")
		manager := NewManager(config.Workspace{Root: root, CWD: root})

		result := manager.Execute(context.Background(), ToolRequest{Tool: ToolNameSourceSanity, Path: "component.astro"})
		gomega.Expect(result.Error).NotTo(gomega.BeEmpty())
		gomega.Expect(result.Paths).To(gomega.Equal([]string{"component.astro"}))

		observed := AnalyzePostWriteVerificationResult(result, []string{"component.astro"})
		gomega.Expect(observed.Attempted).To(gomega.BeTrue())
		gomega.Expect(observed.RelevantToPendingWrites).To(gomega.BeTrue())
		gomega.Expect(observed.Successful).To(gomega.BeFalse())
		gomega.Expect(observed.CoveredPendingPaths).To(gomega.Equal([]string{"component.astro"}))
	})

	ginkgo.It("keeps build dependency closure coverage unresolved", func() {
		result := ToolResult{
			Tool:  ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{ShellCommand: "go build ./cmd/idiom", ShellWorkdir: "."},
		}

		internal := AnalyzePostWriteVerificationResult(result, []string{"internal/runtime/agent/runner_core.go"})
		gomega.Expect(internal.Coverage).To(gomega.Equal(VerificationCoverageDependencyClosureUnresolved))
		gomega.Expect(internal.ScopePaths).To(gomega.Equal([]string{"cmd/idiom"}))
		gomega.Expect(internal.Successful).To(gomega.BeFalse(), fmt.Sprintf("unproven transitive coverage must not count, got %#v", internal))

		direct := AnalyzePostWriteVerificationResult(result, []string{"cmd/idiom/main.go"})
		gomega.Expect(direct.Successful).To(gomega.BeTrue(), fmt.Sprintf("direct build target scope should count, got %#v", direct))
		gomega.Expect(direct.CoveredPendingPaths).To(gomega.Equal([]string{"cmd/idiom/main.go"}))

		result.Paths = []string{"internal/runtime/agent"}
		producerScoped := AnalyzePostWriteVerificationResult(result, []string{"internal/runtime/agent/runner_core.go"})
		gomega.Expect(producerScoped.Successful).To(gomega.BeTrue(), fmt.Sprintf("producer-owned build scope should count, got %#v", producerScoped))
		gomega.Expect(producerScoped.Coverage).To(gomega.Equal(VerificationCoverageDependencyClosureUnresolved))
	})

	ginkgo.It("does not treat Go output flag values as verification scopes", func() {
		result := ToolResult{
			Tool:  ToolNameShell,
			Facts: &toolfacts.ToolResultFacts{ShellCommand: "go build -o cmd/idiom ./cmd/other", ShellWorkdir: "."},
		}

		got := AnalyzePostWriteVerificationResult(result, []string{"cmd/idiom/main.go"})
		gomega.Expect(got.ScopePaths).To(gomega.Equal([]string{"cmd/other"}), fmt.Sprintf("output path must not become build scope, got %#v", got))
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("output path must not cover pending writes, got %#v", got))

		result.Facts.ShellCommand = "go test -outputdir internal/runtime/agent ./internal/unrelated"
		got = AnalyzePostWriteVerificationResult(result, []string{"internal/runtime/agent/runner_core.go"})
		gomega.Expect(got.ScopePaths).To(gomega.Equal([]string{"internal/unrelated"}), fmt.Sprintf("test output directory must not become scope, got %#v", got))
		gomega.Expect(got.Successful).To(gomega.BeFalse(), fmt.Sprintf("test output directory must not cover pending writes, got %#v", got))
	})
})
