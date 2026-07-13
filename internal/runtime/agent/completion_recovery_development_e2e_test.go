package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/contracts/toolfacts"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe(
	"Typed completion recovery through Runner.Run",
	ginkgo.Label("completion_recovery_development", "development-scenario", "e2e", "integration", "smoke", "race-contract"),
	func() {
		ginkgo.It("resolves answerability and exact citation obligations only from matching evidence", func() {
			root := newGoModuleTestWorkspace()
			const (
				targetPath      = "internal/feature/mode.go"
				unsupportedPath = "internal/feature/defaults.go"
				unrelatedPath   = "docs/unrelated.md"
			)
			gomega.Expect(writeTestFile(root, targetPath, strings.Join([]string{
				"package feature",
				"",
				`const StartupMode = "safe"`,
			}, "\n"))).To(gomega.Succeed())
			gomega.Expect(writeTestFile(root, unsupportedPath, strings.Join([]string{
				"package feature",
				"",
				"const DefaultMode = StartupMode",
			}, "\n"))).To(gomega.Succeed())
			gomega.Expect(writeTestFile(root, unrelatedPath, "# Unrelated\n\nThis document describes release notes.\n")).To(gomega.Succeed())

			initialAnswer := "`internal/feature/mode.go` defines `StartupMode` as `safe`, so startup uses safe mode."
			unsupportedAnswer := "Startup uses safe mode: `internal/feature/mode.go` sets `StartupMode` to `safe`, and `internal/feature/defaults.go` defines `DefaultMode` as `StartupMode`."
			acceptedAnswer := unsupportedAnswer
			replies := []string{
				initialAnswer,
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20}`, targetPath),
				unsupportedAnswer,
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20}`, unrelatedPath),
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20}`, unsupportedPath),
				acceptedAnswer,
			}
			server, requestBodies := newScriptedChatServer(replies)

			var events []RunEvent
			runner := &Runner{
				Client: llama.NewClient(server.URL(), 5*time.Second),
				Workspace: workspace.NewManager(config.Workspace{
					Root: root,
					CWD:  root,
				}),
				RuntimeBrief: workerruntime.WorkerRuntimeBrief{
					Lane:              "main",
					TaskFamily:        string(autonomy.TaskFamilyDirectAnswer),
					Contract:          string(workerruntime.ContractDirectAnswer),
					Objective:         "Which startup mode is configured, and where is it defined?",
					PermissionProfile: "read_only_workspace",
					AllowedTools:      []string{"read_file", "code_navigate", "search_content"},
					Readiness:         &workerruntime.ReadinessState{ReadyToAnswer: false},
				},
				OnEvent: func(event RunEvent) {
					events = append(events, event)
				},
			}

			answer, err := runner.Run(
				context.Background(),
				[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
				llama.ChatOptions{},
				nil,
				nil,
				nil,
			)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(answer).To(gomega.Equal(acceptedAnswer))

			requests := decodeCompletionRecoveryRequests(requestBodies())
			gomega.Expect(requests).To(gomega.HaveLen(len(replies)))
			gomega.Expect(requests[1].activeRecoveryText()).NotTo(gomega.BeEmpty(), "missing objective evidence should open typed recovery")
			gomega.Expect(requests[2].activeRecoveryText()).To(gomega.BeEmpty(), "objective-relevant source evidence should resolve the pathless obligation")
			for _, index := range []int{3, 4} {
				gomega.Expect(requests[index].activeRecoveryText()).To(gomega.And(
					gomega.Not(gomega.BeEmpty()),
					gomega.ContainSubstring(unsupportedPath),
				), "request %d should retain the exact citation obligation", index)
			}
			gomega.Expect(requests[5].activeRecoveryText()).To(gomega.BeEmpty(), "matching source-body evidence should clear exact citation recovery")

			firstRecovery := requests[3].activeRecoveryText()
			for _, strategy := range []string{"read one body-readable", "rewrite the answer", "qualify", "materially different", "<<QUESTION>>"} {
				gomega.Expect(firstRecovery).To(gomega.ContainSubstring(strategy))
			}
			for _, tool := range []string{"read_file", "code_navigate", "search_content"} {
				gomega.Expect(chatOptionsExposeTool(llama.ChatOptions{Tools: requests[3].Tools}, tool)).To(gomega.BeTrue(), "source recovery should keep %s callable", tool)
			}

			var progress []string
			for _, event := range events {
				if event.Role == "completion_gate_recovery_progress" {
					progress = append(progress, event.Content)
				}
			}
			gomega.Expect(progress).To(gomega.ConsistOf(
				gomega.And(
					gomega.ContainSubstring(completionGateObligationReadOnlyAnswerability),
					gomega.ContainSubstring("pending obligation(s): none"),
				),
				gomega.And(
					gomega.ContainSubstring(unsupportedPath),
					gomega.ContainSubstring("pending path(s): none"),
				),
			))
			gomega.Expect(strings.Join(progress, "\n")).NotTo(gomega.ContainSubstring(unrelatedPath))
			gomega.Expect(completionGateAuditAttempts(events)).To(gomega.Equal([]int{0, 1, 2}), "the accepted closeout should retain both rejections as audit history")
		})

		ginkgo.It("keeps mutation and verification recovery scoped to matching paths", func() {
			root := newGoModuleTestWorkspace()
			const (
				targetPath         = "web/index.html"
				secondRequiredPath = "web/theme.html"
				unrelatedPath      = "web/notes.html"
			)
			gomega.Expect(writeTestFile(root, targetPath, "<!doctype html>\n<h1>Old</h1>\n")).To(gomega.Succeed())
			gomega.Expect(writeTestFile(root, secondRequiredPath, "<!doctype html>\n<p>Draft</p>\n")).To(gomega.Succeed())
			gomega.Expect(writeTestFile(root, unrelatedPath, "<!doctype html>\n<aside>Draft</aside>\n")).To(gomega.Succeed())

			targetVerifyCommand := workspace.HTMLParserVerificationCommand(targetPath)
			secondVerifyCommand := workspace.HTMLParserVerificationCommand(secondRequiredPath)
			unrelatedVerifyCommand := workspace.HTMLParserVerificationCommand(unrelatedPath)
			unsupportedCloseout := "Implemented the requested page updates.\n\nChanged paths:\n- web/index.html\n- web/theme.html\n\nVerification: theme HTML parsed successfully."
			acceptedCloseout := "Implemented the heading, theme, and notes page updates.\n\nChanged paths:\n- web/index.html\n- web/notes.html\n- web/theme.html\n\nVerification: passed."
			twoPathPatch := strings.Join([]string{
				"*** Begin Patch",
				"*** Update File: web/index.html",
				"@@",
				"-<h1>Old</h1>",
				"+<h1>Ready</h1>",
				"*** Update File: web/theme.html",
				"@@",
				"-<p>Draft</p>",
				"+<p>Final</p>",
				"*** End Patch",
			}, "\n")
			replies := []string{
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20}`, targetPath),
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20}`, secondRequiredPath),
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20}`, unrelatedPath),
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"prepare an allowed but unrelated page"}`, unrelatedPath, "<aside>Draft</aside>", "<aside>Prepared</aside>"),
				unsupportedCloseout,
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20,"named_gap":"refresh the unrelated page after its committed mutation"}`, unrelatedPath),
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"refine the unrelated page"}`, unrelatedPath, "<aside>Prepared</aside>", "<aside>Ready</aside>"),
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20,"named_gap":"refresh the first required page after the unrelated mutation"}`, targetPath),
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":20,"named_gap":"refresh the second required page after the unrelated mutation"}`, secondRequiredPath),
				fmt.Sprintf(`{"tool":"apply_patch","content":%q,"description":"finish both required page updates"}`, twoPathPatch),
				acceptedCloseout,
				fmt.Sprintf(`{"tool":"shell","command":%q,"workdir":"."}`, targetVerifyCommand),
				fmt.Sprintf(`{"tool":"shell","command":%q,"workdir":"."}`, secondVerifyCommand),
				fmt.Sprintf(`{"tool":"shell","command":%q,"workdir":"."}`, unrelatedVerifyCommand),
				acceptedCloseout,
			}
			server, requestBodies := newScriptedChatServer(replies)

			var (
				events   []RunEvent
				timeline []string
				results  []workspace.ToolResult
			)
			runner := &Runner{
				Client: llama.NewClient(server.URL(), 5*time.Second),
				Workspace: workspace.NewManager(config.Workspace{
					Root:         root,
					CWD:          root,
					Shell:        "/bin/sh",
					EnableWrites: true,
					EnableShell:  true,
				}),
				RuntimeBrief: workerruntime.WorkerRuntimeBrief{
					Lane:                 "implement",
					TaskFamily:           "implementation",
					Contract:             "implementation_result",
					Objective:            "Update the heading and add the required theme page.",
					PermissionProfile:    "guarded_workspace_write",
					AllowedTools:         []string{"read_file", "edit_file", "apply_patch", "shell"},
					AllowedEditPaths:     []string{targetPath, secondRequiredPath, unrelatedPath},
					RequiredEditPaths:    []string{targetPath, secondRequiredPath},
					RequiredEvidence:     []string{"changed_paths_or_noop_reason", "verification_status"},
					VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
				},
				OnEvent: func(event RunEvent) {
					events = append(events, event)
					if event.Role == "completion_gate_recovery_progress" {
						timeline = append(timeline, "progress:"+event.Content)
					}
				},
			}

			answer, err := runner.Run(
				context.Background(),
				[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
				llama.ChatOptions{},
				nil,
				nil,
				func(event ToolResultEvent) {
					results = append(results, event.Result)
					timeline = append(timeline, "result:"+event.Result.Tool+":"+strings.Join(event.Result.ChangedPaths, ","))
				},
			)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(answer).To(gomega.Equal(acceptedCloseout), "answer=%q results=%#v", answer, results)

			requests := decodeCompletionRecoveryRequests(requestBodies())
			gomega.Expect(requests).To(gomega.HaveLen(len(replies)))
			for _, index := range []int{5, 6, 7, 8, 9} {
				gomega.Expect(requests[index].activeRecoveryText()).NotTo(gomega.BeEmpty(), "unrelated write must not clear mutation recovery before request %d", index)
			}
			gomega.Expect(requests[10].activeRecoveryText()).To(gomega.BeEmpty(), "matching required-path write should clear mutation recovery")
			gomega.Expect(requests[11].activeRecoveryText()).NotTo(gomega.BeEmpty(), "verification rejection should open typed recovery")
			gomega.Expect(requests[12].activeRecoveryText()).To(gomega.And(
				gomega.Not(gomega.BeEmpty()),
				gomega.ContainSubstring(secondRequiredPath),
			), "partial matching verification should retain only the remaining path")
			gomega.Expect(requests[13].activeRecoveryText()).To(gomega.And(
				gomega.Not(gomega.BeEmpty()),
				gomega.ContainSubstring(unrelatedPath),
			), "required-path verification must retain the unrelated committed path until it is also verified")
			gomega.Expect(requests[14].activeRecoveryText()).To(gomega.BeEmpty(), "matching verification for every committed path should clear recovery")

			for _, expected := range []struct {
				command string
				path    string
			}{
				{command: targetVerifyCommand, path: targetPath},
				{command: secondVerifyCommand, path: secondRequiredPath},
				{command: unrelatedVerifyCommand, path: unrelatedPath},
			} {
				result := successfulShellResultForCommand(results, expected.command)
				facts := result.Facts
				gomega.Expect(facts).NotTo(gomega.BeNil())
				gomega.Expect(facts.ShellCommand).To(gomega.Equal(expected.command))
				gomega.Expect(facts.ShellWorkdir).To(gomega.Equal("."))
				gomega.Expect(facts.VerificationEvidence).To(gomega.Equal([]toolfacts.VerificationEvidence{{
					Kind:  toolfacts.VerificationEvidenceHTMLParse,
					Paths: []string{expected.path},
				}}))
			}

			progressText := strings.Join(timeline, "\n")
			gomega.Expect(progressText).NotTo(gomega.ContainSubstring("progress:successful edit_file"), "the successful edit to B must not reset the active A obligation")
			gomega.Expect(progressText).To(gomega.ContainSubstring("progress:successful apply_patch result advanced the active mutation"))
			gomega.Expect(strings.Count(progressText, "progress:successful shell result advanced the active verification")).To(gomega.Equal(3), "only exact pending-path verification should advance recovery")
			gomega.Expect(completionGateAuditAttempts(events)).To(gomega.Equal([]int{0, 1, 2}), "typed progress must not reset the monotonic audit history")

			updatedTarget, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(targetPath)))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(updatedTarget)).To(gomega.ContainSubstring("<h1>Ready</h1>"))
			updatedState, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(secondRequiredPath)))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(updatedState)).To(gomega.ContainSubstring("<p>Final</p>"))
		})

		ginkgo.It("rejects passing verification after a silent test deletion and accepts restored coverage", func() {
			root := newGoModuleTestWorkspace()
			const (
				sourcePath = "access/access.go"
				testPath   = "access/access_test.go"
			)

			const sourceBefore = `package access

func Level(isAdmin bool) string {
	if isAdmin {
		return "member"
	}
	return "guest"
}
`
			const sourceAfter = `package access

func Level(isAdmin bool) string {
	if isAdmin {
		return "admin"
	}
	return "guest"
}
`
			const guestTest = `func TestLevelKeepsGuestAccessRestricted(t *testing.T) {
	if got := Level(false); got != "guest" {
		t.Fatalf("Level(false) = %q, want guest", got)
	}
}`
			const originalAdminTest = `func TestLevelReturnsElevatedAccessForAdmins(t *testing.T) {
	if got := Level(true); got != "member" {
		t.Fatalf("Level(true) = %q, want member", got)
	}
}`
			const restoredAdminTest = `func TestLevelReturnsElevatedAccessForAdmins(t *testing.T) {
	if got := Level(true); got != "admin" {
		t.Fatalf("Level(true) = %q, want admin", got)
	}
}`

			gomega.Expect(writeTestFile(root, sourcePath, sourceBefore)).To(gomega.Succeed())
			gomega.Expect(writeTestFile(root, testPath, strings.Join([]string{
				"package access",
				"",
				`import "testing"`,
				"",
				guestTest,
				"",
				originalAdminTest,
				"",
			}, "\n"))).To(gomega.Succeed())
			initRunnerTestGitRepo(ginkgo.GinkgoT(), root)

			const verificationCommand = "go test ./..."
			unreportedCloseout := strings.Join([]string{
				"Implemented the administrator access-level change.",
				"",
				"Changed paths:",
				"- " + sourcePath,
				"- " + testPath,
				"",
				"Verification: `" + verificationCommand + "` passed.",
			}, "\n")
			acceptedCloseout := strings.Join([]string{
				"Implemented the administrator access-level change while preserving regression coverage.",
				"",
				"Changed paths:",
				"- " + sourcePath,
				"- " + testPath,
				"",
				"Verification: `" + verificationCommand + "` passed, including TestLevelReturnsElevatedAccessForAdmins.",
			}, "\n")
			replies := []string{
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":40}`, sourcePath),
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":80}`, testPath),
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"return the explicit administrator access level"}`, sourcePath, sourceBefore, sourceAfter),
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":80,"named_gap":"refresh test coverage after the source mutation"}`, testPath),
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":"","description":"remove the now-failing administrator regression"}`, testPath, originalAdminTest),
				fmt.Sprintf(`{"tool":"shell","command":%q,"workdir":"."}`, verificationCommand),
				unreportedCloseout,
				fmt.Sprintf(`{"tool":"read_file","path":%q,"start_line":1,"end_line":9,"named_gap":"refresh the retained test body before restoring deleted coverage"}`, testPath),
				fmt.Sprintf(`{"tool":"edit_file","path":%q,"old_text":%q,"new_text":%q,"description":"restore administrator behavior coverage with the new contract"}`, testPath, guestTest, guestTest+"\n\n"+restoredAdminTest),
				fmt.Sprintf(`{"tool":"shell","command":%q,"workdir":"."}`, verificationCommand),
				acceptedCloseout,
			}
			server, requestBodies := newScriptedChatServer(replies)

			var (
				events  []RunEvent
				results []workspace.ToolResult
			)
			runner := &Runner{
				Client: llama.NewClient(server.URL(), 5*time.Second),
				Workspace: workspace.NewManager(config.Workspace{
					Root:         root,
					CWD:          root,
					Shell:        "/bin/sh",
					EnableWrites: true,
					EnableShell:  true,
				}),
				RuntimeBrief: workerruntime.WorkerRuntimeBrief{
					Lane:                 "implement",
					TaskFamily:           "implementation",
					Contract:             "implementation_result",
					Objective:            "Change administrator access from member to admin without losing regression coverage.",
					PermissionProfile:    "guarded_workspace_write",
					AllowedTools:         []string{"read_file", "edit_file", "shell"},
					AllowedEditPaths:     []string{sourcePath, testPath},
					RequiredEditPaths:    []string{sourcePath, testPath},
					RequiredEvidence:     []string{"changed_paths_or_noop_reason", "verification_status"},
					VerificationPlan:     []string{verificationCommand},
					VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
				},
				OnEvent: func(event RunEvent) {
					events = append(events, event)
				},
			}

			answer, err := runner.Run(
				context.Background(),
				[]llama.ChatMessage{{Role: "user", Content: runner.RuntimeBrief.Objective}},
				llama.ChatOptions{},
				nil,
				nil,
				func(event ToolResultEvent) {
					results = append(results, event.Result)
				},
			)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(answer).To(gomega.Equal(acceptedCloseout), "answer=%q results=%#v", answer, results)

			requests := decodeCompletionRecoveryRequests(requestBodies())
			gomega.Expect(requests).To(gomega.HaveLen(len(replies)))
			var recoveryMessages []string
			for _, message := range requests[7].Messages {
				if strings.Contains(message.Content, "silent test removal") {
					recoveryMessages = append(recoveryMessages, message.Content)
				}
			}
			recovery := strings.Join(recoveryMessages, "\n")
			gomega.Expect(recovery).To(gomega.And(
				gomega.Not(gomega.BeEmpty()),
				gomega.ContainSubstring("silent test removal"),
				gomega.ContainSubstring("TestLevelReturnsElevatedAccessForAdmins"),
			))
			for _, tool := range []string{"edit_file", "shell"} {
				gomega.Expect(chatOptionsExposeTool(llama.ChatOptions{Tools: requests[7].Tools}, tool)).To(gomega.BeTrue(), "recovery should keep %s callable", tool)
			}

			var verificationResults []workspace.ToolResult
			for _, result := range results {
				if result.Tool == workspace.ToolNameShell && result.Facts != nil && result.Facts.ShellCommand == verificationCommand {
					verificationResults = append(verificationResults, result)
				}
			}
			gomega.Expect(verificationResults).To(gomega.HaveLen(2), "both the weakened and corrected worktrees must run real verification")
			for _, result := range verificationResults {
				observed := workspace.AnalyzeVerificationResult(result)
				gomega.Expect(observed.Attempted).To(gomega.BeTrue())
				gomega.Expect(observed.Successful).To(gomega.BeTrue(), "verification result: %#v", result)
				gomega.Expect(observed.BuildOrTest).To(gomega.BeTrue())
				gomega.Expect(result.Facts.ShellWorkdir).To(gomega.Equal("."))
			}

			updatedSource, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(sourcePath)))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(updatedSource)).To(gomega.Equal(sourceAfter))
			updatedTests, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(testPath)))
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(string(updatedTests)).To(gomega.And(
				gomega.ContainSubstring("func TestLevelReturnsElevatedAccessForAdmins"),
				gomega.ContainSubstring(`got != "admin"`),
				gomega.Not(gomega.ContainSubstring(`got != "member"`)),
			))

			var rejectedSilentRemoval bool
			for _, event := range events {
				if (event.Role == "next_action_state" || event.Role == "failure_feedback") &&
					strings.Contains(event.Content, "silent test removal") &&
					strings.Contains(event.Content, "TestLevelReturnsElevatedAccessForAdmins") {

					rejectedSilentRemoval = true
				}
			}
			gomega.Expect(rejectedSilentRemoval).To(gomega.BeTrue(), "events=%#v", events)
		})
	},
)

type completionRecoveryChatRequest struct {
	Messages []llama.ChatMessage `json:"messages"`
	Tools    []llama.Tool        `json:"tools,omitempty"`
}

func (request completionRecoveryChatRequest) activeRecoveryText() string {
	for _, message := range request.Messages {
		if strings.EqualFold(strings.TrimSpace(message.Role), "system") &&
			strings.HasPrefix(strings.TrimSpace(message.Content), completionGateRecoveryMessageTag) {

			return message.Content
		}
	}
	return ""
}

func decodeCompletionRecoveryRequests(bodies []string) []completionRecoveryChatRequest {
	ginkgo.GinkgoHelper()

	requests := make([]completionRecoveryChatRequest, 0, len(bodies))
	for _, body := range bodies {
		var request completionRecoveryChatRequest
		gomega.Expect(json.Unmarshal([]byte(body), &request)).To(gomega.Succeed())
		requests = append(requests, request)
	}
	return requests
}

func completionGateAuditAttempts(events []RunEvent) []int {
	ginkgo.GinkgoHelper()

	decoded := decodeRunnerAutonomyEventsForTest(ginkgo.GinkgoT(), events)
	var attempts []int
	for _, event := range decoded {
		if event.Type == autonomy.EventGateDecision &&
			event.Gate != nil &&
			event.Gate.Gate == autonomy.GateCompletion &&
			event.Gate.Source == "runner_completion_gate" {

			attempts = append(attempts, event.Gate.Attempts)
		}
	}
	return attempts
}

func successfulShellResultForCommand(results []workspace.ToolResult, command string) workspace.ToolResult {
	ginkgo.GinkgoHelper()

	for _, result := range results {
		if result.Tool == workspace.ToolNameShell &&
			strings.TrimSpace(result.Error) == "" &&
			result.Facts != nil &&
			result.Facts.ShellCommand == command {

			return result
		}
	}
	ginkgo.Fail("successful shell result not found for command: "+command, 1)
	return workspace.ToolResult{}
}
