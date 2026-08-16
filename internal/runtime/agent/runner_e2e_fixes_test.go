package agent

import (
	"context"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"idiom/internal/capabilities/workspace"
	compactionevidence "idiom/internal/evidence/compaction"
	"idiom/internal/evidence/trace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/llama"
	"idiom/internal/runtime/workerruntime"
)

var _ = ginkgo.Describe("Runner end-to-end recovery fixes", ginkgo.Label("e2e"), func() {
	ginkgo.It("fails closed when resumed compaction provenance is rejected", func() {
		root := ginkgo.GinkgoT().TempDir()
		server, requestBodies := newScriptedChatServer([]string{"must not be called"})
		defer server.Close()
		provenance := compactionevidence.New("source", "retained", "summary", 4, 3, []string{"kept.go"}, []string{"missing.go"}, "evidence_weighted", "resume")
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			SeedContextCompactionProvenance: &provenance,
		}

		_, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "resume"}}, llama.ChatOptions{}, nil, nil, nil)
		gomega.Expect(err).To(gomega.MatchError("context compaction provenance is not accepted for resume"))
		gomega.Expect(requestBodies()).To(gomega.BeEmpty())
	})

	ginkgo.It("emits and replays a redacted model-to-tool trace", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRunnerE2EFile(root, "README.md", "trace evidence\n")
		server, _ := newScriptedChatServer([]string{
			`{"tool":"read_file","path":"README.md"}`,
			"The README contains trace evidence.",
		})
		recorder := trace.NewRecorder("runner-trace-e2e")
		var modelEvents []string
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 3, MaxNoProgressTurns: 3},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         "Read README.md and report its evidence.",
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{workspace.ToolNameReadFile},
			},
			Trace: recorder,
			OnEvent: func(event RunEvent) {
				if event.Role == "model_call" {
					modelEvents = append(modelEvents, event.Content)
				}
			},
		}

		answer, err := runner.Run(context.Background(), []llama.ChatMessage{{Role: "user", Content: "Read README.md and report its evidence."}}, llama.ChatOptions{}, nil, nil, nil)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal("The README contains trace evidence."))
		gomega.Expect(modelEvents).NotTo(gomega.BeEmpty())
		gomega.Expect(modelEvents[0]).To(gomega.ContainSubstring(`"model_capability_profile_schema":"idiom.model_capability_profile.v1"`))

		document := recorder.Snapshot()
		gomega.Expect(trace.Replay(document).Valid).To(gomega.BeTrue())
		kinds := make(map[trace.SpanKind]bool)
		for _, span := range document.Spans {
			kinds[span.Kind] = true
		}
		gomega.Expect(kinds[trace.SpanModelCall]).To(gomega.BeTrue())
		gomega.Expect(kinds[trace.SpanToolRequest]).To(gomega.BeTrue())
		gomega.Expect(kinds[trace.SpanPolicy]).To(gomega.BeTrue())
		gomega.Expect(kinds[trace.SpanToolResult]).To(gomega.BeTrue())
		gomega.Expect(kinds[trace.SpanEvidence]).To(gomega.BeTrue())
	})

	ginkgo.It("allows a documentation read after readiness while under the cap", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRunnerE2EFile(root, "internal/runtime/agent/foo.go", "package agent\n\nfunc Foo() {}\n")
		writeRunnerE2EFile(root, "docs/design-context.md", "# Design\n\nWidget details live here.\n")
		writeRunnerE2EFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n")

		finalAnswer := "Submitted the Foo implementation plan after reviewing both the source and its design context."
		replies := []string{
			`{"tool":"read_file","path":"internal/runtime/agent/foo.go","start_line":1,"end_line":40}`,
			`{"tool":"read_file","path":"docs/design-context.md"}`,
			`{"tool":"history_range","path":"internal/runtime/agent/foo.go","start_line":1,"end_line":20}`,
			`{"tool":"submit_plan","rationale":"Update Foo after reviewing source, design context, and history.","edits":[{"path":"internal/runtime/agent/foo.go","old_text":"func Foo() {}","new_text":"func Foo() { /* planned behavior */ }","purpose":"implement the planned Foo behavior"}]}`,
			finalAnswer,
		}
		server, requestBodies := newScriptedChatServer(replies)
		var requests []workspace.ToolRequest

		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root:         root,
				CWD:          root,
				EnableWrites: true,
			}),
			Policy: RunPolicy{MaxToolSteps: 6, MaxNoProgressTurns: 4},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        PlanThenExecutePlannerTaskFamily,
				Contract:          "implementation_result",
				Objective:         "Plan a change to Foo().",
				PermissionProfile: "guarded_workspace_write",
				AllowedEditPaths:  []string{"internal/runtime/agent/foo.go"},
				AllowedTools:      []string{"read_file", "history_range", "submit_plan"},
			},
		}

		answer, err := runner.Run(
			workspace.WithExecutionSessionID(context.Background(), "documentation-read-plan"),
			[]llama.ChatMessage{{Role: "user", Content: "Plan a change to Foo()."}},
			llama.ChatOptions{},
			nil,
			func(event ToolRequestEvent) {
				requests = append(requests, event.Request)
			},
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))
		gomega.Expect(requests).To(gomega.HaveLen(4))
		gomega.Expect([]string{requests[0].Tool, requests[1].Tool, requests[2].Tool, requests[3].Tool}).To(gomega.Equal([]string{
			workspace.ToolNameReadFile,
			workspace.ToolNameReadFile,
			workspace.ToolNameHistoryRange,
			workspace.ToolNameSubmitPlan,
		}))
		gomega.Expect([]string{requests[0].Path, requests[1].Path}).To(gomega.Equal([]string{"internal/runtime/agent/foo.go", "docs/design-context.md"}))
		gomega.Expect(requestBodies()).To(gomega.HaveLen(len(replies)))

		bodies := strings.Join(requestBodies(), "\n---\n")
		gomega.Expect(bodies).NotTo(gomega.And(
			gomega.ContainSubstring("Plan-execute read outside allowed candidate scope"),
			gomega.ContainSubstring("docs/design-context.md"),
		))
		gomega.Expect(bodies).To(gomega.ContainSubstring("Widget details live here"))
	})

	ginkgo.It("supports a TDD coverage follow-up from a feature-planner plan-tab read", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRunnerE2EFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n")
		writeRunnerE2EFile(root, ".planning/20260621-161128.627.md", featurePlannerPlanMarkdownForTest())

		finalAnswer := featurePlannerPlanTableAnswerForTest()
		replies := []string{
			`{"tool":"read_file","path":".planning/20260621-161128.627.md","start_line":1,"end_line":120}`,
			finalAnswer,
		}
		server, requestBodies := newScriptedChatServer(replies)

		objective := "what tests do w have planned? should we expand the intended test suite?"
		var events []RunEvent
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         objective,
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"read_file"},
			},
			OnEvent: func(event RunEvent) {
				events = append(events, event)
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: objective}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))

		bodies := requestBodies()
		gomega.Expect(bodies).To(gomega.HaveLen(2))
		gomega.Expect(strings.Join(bodies, "\n---\n")).To(gomega.ContainSubstring("Feature Planner Candidate"))

		for _, event := range events {
			sourceGroundingRepair := strings.Contains(event.Content, "source_grounding_unmet") ||
				strings.Contains(event.Content, "does not exist in this workspace") ||
				strings.Contains(event.Content, "tests/test_llm_client.py") && strings.Contains(event.Content, "source-body evidence")
			gomega.Expect(sourceGroundingRepair).To(gomega.BeFalse(), "event=%s content=%s", event.Role, event.Content)
		}
	})

	ginkgo.It("does not leak a feature-planner TDD-gate meta final", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRunnerE2EFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n")
		writeRunnerE2EFile(root, ".planning/20260621-161128.627.md", featurePlannerPlanMarkdownForTest())

		leaked := "The answer above is grounded in the inspected evidence (`.planning/20260621-161128.627.md` lines 25-39 for the planned tests). The proposed additions are explicitly marked as `unverified - proposed` since they don't exist on disk. This satisfies the gate requirements."
		finalAnswer := "The planned tests are `tests/test_models.py`, `tests/test_config.py`, and `tests/test_orchestrator.py`. I would expand the intended suite with persistence variants, config default/error cases, and orchestrator failure-propagation invariants."
		replies := []string{
			`{"tool":"read_file","path":".planning/20260621-161128.627.md","start_line":1,"end_line":120}`,
			leaked,
			finalAnswer,
		}
		server, _ := newScriptedChatServer(replies)

		objective := "what tests do w have planned? should we expand the intended test suite?"
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         objective,
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"read_file"},
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: objective}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))
		for _, forbidden := range []string{"The answer above", "gate requirements", "<<QUESTION>>", "Recovery card"} {
			gomega.Expect(answer).NotTo(gomega.ContainSubstring(forbidden))
		}
		gomega.Expect(answer).NotTo(gomega.BeEmpty())
	})

	ginkgo.It("does not leak an embedded question protocol final", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRunnerE2EFile(root, "docs/harness.md", strings.Join([]string{
			"# Harness",
			"The harness owns final-answer acceptance and user-facing presentation.",
			"Protocol markers are internal transport details, not user-facing answers.",
		}, "\n"))
		writeRunnerE2EFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n")

		leaked := "I identified the newest conversation log at `/home/dev/.idiom/logs/conversations/20260622-064537.192-20260622-104535.851.jsonl`.\n### <<QUESTION>> — Exact Blocker\nI need permission to read the conversation log from the user-level path."
		finalAnswer := "Final-answer acceptance and presentation are the shared boundary: `docs/harness.md` says the harness owns final-answer acceptance and that protocol markers are internal transport details, not user-facing answers."
		replies := []string{
			`{"tool":"read_file","path":"docs/harness.md","start_line":1,"end_line":20}`,
			leaked,
			finalAnswer,
		}
		server, _ := newScriptedChatServer(replies)

		objective := "What boundary owns leaked protocol text in user-facing final answers?"
		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root: root,
				CWD:  root,
			}),
			Policy: RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:        "conversation",
				Contract:          "direct_answer",
				Objective:         objective,
				PermissionProfile: "read_only_workspace",
				AllowedTools:      []string{"read_file"},
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: objective}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(answer).To(gomega.Equal(finalAnswer))
		for _, forbidden := range []string{"<<QUESTION>>", "user-level path", "newest conversation log"} {
			gomega.Expect(answer).NotTo(gomega.ContainSubstring(forbidden))
		}
	})

	ginkgo.It("returns deterministic incomplete status instead of leaked implementation prose", func() {
		root := ginkgo.GinkgoT().TempDir()
		writeRunnerE2EFile(root, "go.mod", "module idiomtest\n\ngo 1.22\n")

		leaked := "Changed paths: none\n\nVerification: not run or not detected.\n\nBlocker:\n- File search rejected: pattern is required"
		rewritten := "The implementation lane ended before a successful workspace edit was recorded; no files were changed."
		// The leaked-template violation and the unchanged required-evidence
		// violation are distinct recovery states. Script every observed reply so
		// an unaccounted-for model request still fails at the protocol boundary.
		server, requestBodies := newScriptedChatServer([]string{leaked, rewritten, rewritten, rewritten})

		runner := &Runner{
			Client: llama.NewClient(server.URL(), 5*time.Second),
			Workspace: workspace.NewManager(config.Workspace{
				Root:         root,
				CWD:          root,
				EnableWrites: true,
			}),
			Policy:    RunPolicy{MaxToolSteps: 4, MaxNoProgressTurns: 4},
			AgentName: "main",
			RuntimeBrief: workerruntime.WorkerRuntimeBrief{
				TaskFamily:           "implementation",
				Contract:             "implementation_result",
				PermissionProfile:    "guarded_workspace_write",
				VerificationContract: "tests_passed_or_failed_or_skipped_with_blocker",
			},
		}

		answer, err := runner.Run(
			context.Background(),
			[]llama.ChatMessage{{Role: "user", Content: "Implement the change."}},
			llama.ChatOptions{},
			nil,
			nil,
			nil,
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())

		bodies := requestBodies()
		gomega.Expect(bodies).To(gomega.HaveLen(3))
		gomega.Expect(answer).To(gomega.And(
			gomega.ContainSubstring("Changed paths: none"),
			gomega.ContainSubstring("no successful write was recorded"),
		))
		gomega.Expect(answer).NotTo(gomega.ContainSubstring("File search rejected"))
		gomega.Expect(bodies[1]).To(gomega.Or(
			gomega.ContainSubstring("File search rejected"),
			gomega.ContainSubstring("Changed paths: none"),
		))
	})

})

func writeRunnerE2EFile(root string, path string, content string) {
	ginkgo.GinkgoHelper()
	gomega.Expect(writeTestFile(root, path, content)).To(gomega.Succeed())
}
