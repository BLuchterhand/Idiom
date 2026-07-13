package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"idiom/internal/contracts/taskledger"
	"idiom/internal/platform/config"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

var _ = ginkgo.Describe("task ledger blocker contract", func() {
	ginkgo.It("normalizes workspace-backed subjects without deriving them from blocker kind", func() {
		manager := newTaskLedgerBlockerManager()
		blocker, err := manager.NormalizeTaskLedgerBlocker(taskledger.StatusBlocked, &TaskLedgerBlocker{
			Kind: "missing-input",
			Subject: TaskLedgerBlockerSubjectRef{
				Namespace: "  ARTIFACT  ",
				ID:        "  workspace:specs/api.yaml  ",
			},
			Reason: "  the fixture is not available in the workspace  ",
		})
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		gomega.Expect(blocker).To(gomega.Equal(&TaskLedgerBlocker{
			Kind: TaskLedgerBlockerMissingInput,
			Subject: TaskLedgerBlockerSubjectRef{
				Namespace: TaskLedgerBlockerSubjectArtifact,
				ID:        "workspace:specs/api.yaml",
			},
			Reason: "the fixture is not available in the workspace",
		}))
	})

	ginkgo.DescribeTable("accepts authority-backed namespace-specific subjects",
		func(kind TaskLedgerBlockerKind, subject TaskLedgerBlockerSubjectRef, expectedID string) {
			manager := newTaskLedgerBlockerManager()
			blocker, err := manager.NormalizeTaskLedgerBlocker(taskledger.StatusBlocked, &TaskLedgerBlocker{
				Kind:    kind,
				Subject: subject,
				Reason:  "the identified subject is currently unavailable",
			})
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(blocker.Subject.Namespace).To(gomega.Equal(subject.Namespace))
			gomega.Expect(blocker.Subject.ID).To(gomega.Equal(expectedID))
		},
		ginkgo.Entry("configured permission", TaskLedgerBlockerApprovalRequired, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectPermission, ID: "protected_path:prompts/base.md"}, "protected_path:prompts/base.md"),
		ginkgo.Entry("workspace artifact", TaskLedgerBlockerMissingInput, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectArtifact, ID: "workspace:specs/api.yaml"}, "workspace:specs/api.yaml"),
		ginkgo.Entry("absolute service URI", TaskLedgerBlockerExternalDependency, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectService, ID: "uri:https://postgres.example.test/primary"}, "uri:https://postgres.example.test/primary"),
		ginkgo.Entry("GOPROXY environment variable", TaskLedgerBlockerEnvironment, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectEnvironmentVariable, ID: "GOPROXY"}, "GOPROXY"),
		ginkgo.Entry("CI environment variable", TaskLedgerBlockerEnvironment, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectEnvironmentVariable, ID: "CI"}, "CI"),
		ginkgo.Entry("PATH environment variable", TaskLedgerBlockerEnvironment, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectEnvironmentVariable, ID: "PATH"}, "PATH"),
		ginkgo.Entry("known tool", TaskLedgerBlockerCapabilityUnavailable, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectTool, ID: ToolNameGoMoveDeclarations}, ToolNameGoMoveDeclarations),
		ginkgo.Entry("configured policy", TaskLedgerBlockerPolicyConstraint, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectPolicy, ID: "tool_rule:write_file"}, "tool_rule:write_file"),
		ginkgo.Entry("exact executable", TaskLedgerBlockerEnvironment, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectCommand, ID: "executable:go"}, "executable:go"),
		ginkgo.Entry("workspace configuration", TaskLedgerBlockerMissingInput, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectConfiguration, ID: "workspace:config/idiom.json"}, "workspace:config/idiom.json"),
		ginkgo.Entry("canonical workspace path", TaskLedgerBlockerMissingInput, TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectWorkspacePath, ID: "pkg/feature.go"}, "pkg/feature.go"),
	)

	ginkgo.DescribeTable("rejects unrecognized or ambiguous blocker subjects",
		func(blocker *TaskLedgerBlocker, errorText string) {
			_, err := newTaskLedgerBlockerManager().NormalizeTaskLedgerBlocker(taskledger.StatusBlocked, blocker)
			gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring(errorText)))
		},
		ginkgo.Entry("blocked without blocker", nil, "requires blocker.kind"),
		ginkgo.Entry("unsupported kind", &TaskLedgerBlocker{Kind: "other", Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectArtifact, ID: "workspace:specs/api.yaml"}, Reason: "unavailable"}, "unsupported"),
		ginkgo.Entry("generic service slug", &TaskLedgerBlocker{Kind: TaskLedgerBlockerExternalDependency, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectService, ID: "unknown_service"}, Reason: "unavailable"}, "uri:<absolute-service-uri>"),
		ginkgo.Entry("generic artifact slug", &TaskLedgerBlocker{Kind: TaskLedgerBlockerMissingInput, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectArtifact, ID: "missing_input"}, Reason: "missing"}, "workspace:<path>"),
		ginkgo.Entry("unknown tool-shaped slug", &TaskLedgerBlocker{Kind: TaskLedgerBlockerCapabilityUnavailable, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectTool, ID: "compiler_binary"}, Reason: "unavailable"}, "not in the workspace tool catalog"),
		ginkgo.Entry("generic policy slug", &TaskLedgerBlocker{Kind: TaskLedgerBlockerPolicyConstraint, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectPolicy, ID: "some_policy"}, Reason: "blocked"}, "configured tool_rule:<tool>"),
		ginkgo.Entry("generic configuration slug", &TaskLedgerBlocker{Kind: TaskLedgerBlockerMissingInput, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectConfiguration, ID: "required_data"}, Reason: "missing"}, "workspace:<path>"),
		ginkgo.Entry("non-POSIX environment variable", &TaskLedgerBlocker{Kind: TaskLedgerBlockerEnvironment, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectEnvironmentVariable, ID: "GO-PROXY"}, Reason: "missing"}, "POSIX variable name"),
		ginkgo.Entry("raw command slug", &TaskLedgerBlocker{Kind: TaskLedgerBlockerEnvironment, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectCommand, ID: "compiler_binary"}, Reason: "missing"}, "executable:<exact-name>"),
		ginkgo.Entry("unconfigured protected path", &TaskLedgerBlocker{Kind: TaskLedgerBlockerApprovalRequired, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectPermission, ID: "protected_path:other.txt"}, Reason: "approval required"}, "configured protected_path:<path>"),
		ginkgo.Entry("unconfigured policy rule", &TaskLedgerBlocker{Kind: TaskLedgerBlockerPolicyConstraint, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectPolicy, ID: "tool_rule:edit_file"}, Reason: "blocked"}, "configured tool_rule:<tool>"),
		ginkgo.Entry("missing reason", &TaskLedgerBlocker{Kind: TaskLedgerBlockerEnvironment, Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectEnvironmentVariable, ID: "CI"}}, "reason is required"),
	)

	ginkgo.It("rejects a blocker on a non-blocked item", func() {
		_, err := newTaskLedgerBlockerManager().NormalizeTaskLedgerBlocker(taskledger.StatusCompleted, &TaskLedgerBlocker{
			Kind:    TaskLedgerBlockerEnvironment,
			Subject: TaskLedgerBlockerSubjectRef{Namespace: TaskLedgerBlockerSubjectEnvironmentVariable, ID: "CI"},
			Reason:  "not configured",
		})
		gomega.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("only valid when status is blocked")))
	})

	ginkgo.It("publishes namespace-specific structured subject schemas", func() {
		var definition ToolDefinition
		for _, candidate := range baseToolDefinitions() {
			if candidate.Name == ToolNameTaskLedger {
				definition = candidate
				break
			}
		}
		gomega.Expect(definition.Name).To(gomega.Equal(ToolNameTaskLedger))

		var schema struct {
			Properties struct {
				Todos struct {
					Items struct {
						Properties struct {
							Blocker struct {
								Required   []string `json:"required"`
								Properties struct {
									Kind struct {
										Enum []string `json:"enum"`
									} `json:"kind"`
									Subject struct {
										OneOf []struct {
											Properties struct {
												Namespace struct {
													Enum []string `json:"enum"`
												} `json:"namespace"`
												ID struct {
													Pattern string `json:"pattern"`
												} `json:"id"`
											} `json:"properties"`
										} `json:"oneOf"`
									} `json:"subject"`
								} `json:"properties"`
							} `json:"blocker"`
						} `json:"properties"`
					} `json:"items"`
				} `json:"todos"`
			} `json:"properties"`
		}
		gomega.Expect(json.Unmarshal([]byte(definition.ParametersJSON), &schema)).To(gomega.Succeed())
		blocker := schema.Properties.Todos.Items.Properties.Blocker
		gomega.Expect(blocker.Required).To(gomega.ConsistOf("kind", "subject", "reason"))
		gomega.Expect(slices.Equal(blocker.Properties.Kind.Enum, TaskLedgerBlockerKindValues())).To(gomega.BeTrue(), fmt.Sprintf("blocker kind schema = %#v", blocker.Properties.Kind.Enum))
		gomega.Expect(blocker.Properties.Subject.OneOf).To(gomega.HaveLen(len(TaskLedgerBlockerSubjectNamespaceValues())))

		var namespaces []string
		patterns := make(map[string]string)
		for _, variant := range blocker.Properties.Subject.OneOf {
			gomega.Expect(variant.Properties.Namespace.Enum).To(gomega.HaveLen(1))
			namespace := variant.Properties.Namespace.Enum[0]
			namespaces = append(namespaces, namespace)
			patterns[namespace] = variant.Properties.ID.Pattern
		}
		gomega.Expect(slices.Equal(namespaces, TaskLedgerBlockerSubjectNamespaceValues())).To(gomega.BeTrue(), fmt.Sprintf("subject namespace schema = %#v", namespaces))
		gomega.Expect(patterns[string(TaskLedgerBlockerSubjectEnvironmentVariable)]).To(gomega.Equal("^[A-Za-z_][A-Za-z0-9_]*$"))
		gomega.Expect(patterns[string(TaskLedgerBlockerSubjectService)]).To(gomega.HavePrefix("^uri:"))
	})
})

func newTaskLedgerBlockerManager() *Manager {
	root := ginkgo.GinkgoT().TempDir()
	for path, body := range map[string]string{
		"config/idiom.json": "{}\n",
		"pkg/feature.go":    "package pkg\n",
		"prompts/base.md":   "base prompt\n",
		"specs/api.yaml":    "openapi: 3.1.0\n",
	} {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		gomega.Expect(os.MkdirAll(filepath.Dir(absolute), 0o755)).To(gomega.Succeed())
		gomega.Expect(os.WriteFile(absolute, []byte(body), 0o644)).To(gomega.Succeed())
	}

	manager := NewManager(config.Workspace{
		Root:         root,
		CWD:          root,
		EnableWrites: true,
		ToolRules: map[string][]config.ToolRule{
			ToolNameWriteFile: {{Pattern: "*", Decision: "ask"}},
		},
	})
	manager.SetProtectedWritePaths([]string{"prompts/base.md"})
	return manager
}
