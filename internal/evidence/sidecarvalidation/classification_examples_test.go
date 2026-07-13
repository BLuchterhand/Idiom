package sidecarvalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/runtime/localops"
)

type p76ClassificationExample struct {
	classification   autonomy.ClientClassification
	commandID        string
	overlayID        string
	limitation       string
	expectedExitCode int
	args             func(root string, configPath string) []string
	assert           func(stdout []byte, root string, configPath string)
}

var _ = ginkgo.Describe("P7.6 CLI classification examples", func() {
	examples := p76ClassificationExamples()
	ginkgo.It("covers every default client classification", func() {
		assertP76CoversDefaultClientClassifications(examples)
	})

	for _, example := range examples {
		example := example
		ginkgo.It(string(example.classification)+" uses generic primitives", func() {
			t := ginkgo.GinkgoT()
			t.Setenv("IDIOM_TELEMETRY", "0")
			gomega.Expect(strings.TrimSpace(example.overlayID)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("example must carry explicit overlay metadata and limitation: %#v", example))
			gomega.Expect(strings.TrimSpace(example.limitation)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("example must carry explicit overlay metadata and limitation: %#v", example))

			root := t.TempDir()
			writeP76ClassificationWorkspace(root)

			configPath := writeMainConfigRoot(root)
			if example.classification == autonomy.ClientLocalModel {
				stageDoctorConfigWithBudget(root, 4096, 512)

				configPath = filepath.Join(root, ".idiom", "config.json")
			}

			var stdout, stderr bytes.Buffer

			code := runMain(example.args(root, configPath), &stdout, &stderr)

			gomega.Expect(code).To(gomega.BeEquivalentTo(example.expectedExitCode), fmt.Sprintf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, example.expectedExitCode, stdout.String(), stderr.String()))

			gomega.Expect(stderr.Len()).To(gomega.BeEquivalentTo(0), fmt.Sprintf("P7.6 example should keep diagnostics in structured stdout, stderr=%s", stderr.String()))

			assertP76NoForbiddenExampleLeakage(stdout.String())
			example.assert(stdout.Bytes(), root, configPath)
		})
	}
})

func p76ClassificationExamples() []p76ClassificationExample {
	return []p76ClassificationExample{
		{
			classification:   autonomy.ClientFinance,
			commandID:        "finance-discovery-policy",
			overlayID:        "finance-proof-overlay",
			limitation:       "proof-only: compliance posture and regulated write automation remain deferred to P7.9/P9.9",
			expectedExitCode: 1,
			args: func(root string, configPath string) []string {
				return []string{
					"tool", "write_file",
					"-config", configPath,
					"--profile", "discovery",
					"--caller-id", "fin-audit",
					"--request-id", "p76-fin",
					"--input", `{"path":"regulated/ledger.txt","content":"P76_FINANCE_SENTINEL_DO_NOT_LEAK"}`,
				}
			},
			assert: func(stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(stdout)
				assertP76SidecarCommon(payload, "write_file", "discovery", autonomy.ClientFinance)
				assertP76JSONPath(payload, "sidecar.caller.caller_id", "fin-audit")
				assertP76JSONPath(payload, "sidecar.error.class", "profile_denied")
				assertP76JSONPath(payload, "sidecar.error.code", "profile_denied")

				{
					_, err := os.Stat(filepath.Join(root, "regulated", "ledger.txt"))
					gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("discovery-profile finance proof should not write regulated fixture; stat err=%v", err))
				}
			},
		},
		{
			classification:   autonomy.ClientHealthcare,
			commandID:        "healthcare-redaction-denial",
			overlayID:        "healthcare-proof-overlay",
			limitation:       "proof-only: no HIPAA compliance claim; PHI fixtures and posture checks remain deferred to P7.9/P9.9",
			expectedExitCode: 1,
			args: func(root string, configPath string) []string {
				return []string{
					"tool", "context_pack",
					"-config", configPath,
					"--profile", "discovery",
					"--caller-id", "hc-review",
					"--request-id", "p76-hc",
					"--input", `{"task":"P76_HEALTHCARE_SENTINEL_DO_NOT_LEAK","mode":"plan","limit":1}`,
				}
			},
			assert: func(stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(stdout)
				assertP76SidecarCommon(payload, "context_pack", "discovery", autonomy.ClientHealthcare)
				assertP76JSONPath(payload, "sidecar.error.class", "profile_denied")

				{
					_, err := os.Stat(filepath.Join(root, ".idiom", "comprehension"))
					gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("discovery-profile healthcare proof should not create context artifacts; stat err=%v", err))
				}
			},
		},
		{
			classification:   autonomy.ClientPublicSector,
			commandID:        "public-sector-offline-side-effect-denial",
			overlayID:        "public-sector-proof-overlay",
			limitation:       "proof-only: certification, offline procurement readiness, and reviewed posture remain deferred to P7.9/P9.9",
			expectedExitCode: 1,
			args: func(root string, configPath string) []string {
				markerPath := filepath.Join(root, "network-side-effect.txt")

				return []string{
					"tool", "shell",
					"-config", configPath,
					"--profile", "discovery",
					"--caller-id", "pubsec-ci",
					"--request-id", "p76-pub",
					"--input", fmt.Sprintf(`{"command":%q}`, "printf touched > "+markerPath),
				}
			},
			assert: func(stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(stdout)
				assertP76SidecarCommon(payload, "shell", "discovery", autonomy.ClientPublicSector)
				assertP76JSONPath(payload, "sidecar.error.class", "profile_denied")

				{
					_, err := os.Stat(filepath.Join(root, "network-side-effect.txt"))
					gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("public-sector discovery proof should not perform shell side effect; stat err=%v", err))
				}
			},
		},
		{
			classification:   autonomy.ClientEnterpriseSaaS,
			commandID:        "enterprise-source-sanity-contract",
			overlayID:        "enterprise-saas-ci-overlay",
			limitation:       "MVP proof: merge-facing compliance and retained buyer value remain P7.10/P7.11/P9.7/P9.9 scope",
			expectedExitCode: 0,
			args: func(root string, configPath string) []string {
				return []string{
					"tool", "source_sanity",
					"-config", configPath,
					"--caller-id", "ci-agent",
					"--request-id", "p76-saas",
					"--trace-id", "trace:p76-saas",
					"--input", `{"path":"pkg/payments/retry.go"}`,
				}
			},
			assert: func(stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(stdout)
				assertP76SidecarCommon(payload, "source_sanity", "default", autonomy.ClientEnterpriseSaaS)
				assertP76JSONPath(payload, "sidecar.caller.trace_id", "trace:p76-saas")
				assertP76JSONPath(payload, "sidecar.structured_output.format", "text")

				{
					got, _ := jsonValueAtDottedPath(payload, "sidecar.error")
					gomega.Expect(got).To(gomega.BeNil(), fmt.Sprintf("enterprise source-sanity proof should succeed, got error %#v", got))
				}
			},
		},
		{
			classification:   autonomy.ClientToolingVendor,
			commandID:        "tooling-vendor-schema-metadata",
			overlayID:        "tooling-vendor-schema-overlay",
			limitation:       "MVP proof: MCP resource parity and partner conformance remain P8.5/P9.9 scope",
			expectedExitCode: 0,
			args: func(root string, configPath string) []string {
				return []string{"tool", "schemas"}
			},
			assert: func(stdout []byte, root string, configPath string) {
				var export struct {
					SchemaVersion string `json:"schema_version"`
					Metadata      struct {
						ClientClassifications []autonomy.ClientClassification `json:"client_classifications"`
						ProductSignals        []autonomy.ProductSignal        `json:"product_signals"`
					} `json:"metadata"`
					Tools []struct {
						Name     string   `json:"name"`
						Profiles []string `json:"profiles"`
						Policy   struct {
							ErrorClasses []string `json:"error_classes"`
						} `json:"policy"`
					} `json:"tools"`
				}

				gomega.Expect(json.Unmarshal(stdout, &export)).To(gomega.Succeed(), "decode schema export output:\n%s", string(stdout))

				gomega.Expect(export.SchemaVersion).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("tooling-vendor schema proof should expose schema and tools: %#v", export))
				gomega.Expect(export.Tools).NotTo(gomega.HaveLen(0), fmt.Sprintf("tooling-vendor schema proof should expose schema and tools: %#v", export))

				assertP76ClassificationInMetadata(export.Metadata.ClientClassifications, autonomy.ClientToolingVendor)
				assertP76ProductSignalInMetadata(export.Metadata.ProductSignals, autonomy.ProductIntegrationProof)
			},
		},
		{
			classification:   autonomy.ClientLocalModel,
			commandID:        "local-model-readiness-evidence",
			overlayID:        "local-model-readiness-overlay",
			limitation:       "MVP proof: model quality, live capacity, and operational reliability remain P6/P7.9/P9.9 scope",
			expectedExitCode: 0,
			args: func(root string, configPath string) []string {
				return []string{
					"local-ops", "benchmark",
					"--json",
					"--config", configPath,
					"--profile", "implement",
				}
			},
			assert: func(stdout []byte, root string, configPath string) {
				var report localops.BenchmarkReport

				gomega.Expect(json.Unmarshal(stdout, &report)).To(gomega.Succeed(), "decode local-ops report output:\n%s", string(stdout))

				gomega.Expect(report.SchemaVersion).To(gomega.BeEquivalentTo(localops.BenchmarkSchemaVersion), fmt.Sprintf("local-model proof should emit one schema-versioned profile report: %#v", report))
				gomega.Expect(report.Profiles).To(gomega.HaveLen(1), fmt.Sprintf("local-model proof should emit one schema-versioned profile report: %#v", report))

				profile := report.Profiles[0]
				gomega.Expect(profile.Name).To(gomega.BeEquivalentTo("implement"), fmt.Sprintf("local-model proof missing machine-readable readiness evidence: %#v", profile))
				gomega.Expect(profile.Readiness.Contract).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("local-model proof missing machine-readable readiness evidence: %#v", profile))
				gomega.Expect(profile.Readiness.QueueState).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("local-model proof missing machine-readable readiness evidence: %#v", profile))
				gomega.Expect(profile.Readiness.ContextBudgetState).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("local-model proof missing machine-readable readiness evidence: %#v", profile))

				gomega.Expect(report.Privacy.MetadataOnly).To(gomega.BeTrue(), fmt.Sprintf("local-model proof should be metadata-only privacy evidence: %#v", report.Privacy))
				gomega.Expect(report.Privacy.RawPromptCaptured).To(gomega.BeFalse(), fmt.Sprintf("local-model proof should be metadata-only privacy evidence: %#v", report.Privacy))
				gomega.Expect(report.Privacy.RawFileBodyCaptured).To(gomega.BeFalse(), fmt.Sprintf("local-model proof should be metadata-only privacy evidence: %#v", report.Privacy))
				gomega.Expect(report.Privacy.SecretCaptureAllowed).To(gomega.BeFalse(), fmt.Sprintf("local-model proof should be metadata-only privacy evidence: %#v", report.Privacy))
			},
		},
		{
			classification:   autonomy.ClientOpenSource,
			commandID:        "open-source-context-pack",
			overlayID:        "open-source-quickstart-overlay",
			limitation:       "MVP proof: full demo transcript, cleanup timing, and retained-use evidence remain P7.10/P7.11/P9.7 scope",
			expectedExitCode: 0,
			args: func(root string, configPath string) []string {
				return []string{
					"tool", "context_pack",
					"-config", configPath,
					"--caller-id", "oss-user",
					"--request-id", "p76-oss",
					"--input", `{"task":"localize contributor docs","mode":"plan","limit":4}`,
				}
			},
			assert: func(stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(stdout)
				assertP76SidecarCommon(payload, "context_pack", "default", autonomy.ClientOpenSource)
				assertP76JSONPath(payload, "sidecar.freshness.policy", "read_through")
				assertP76JSONPath(payload, "sidecar.structured_output.task", "localize contributor docs")

				for _, path := range []string{
					"sidecar.structured_output.pack_id",
					"sidecar.structured_output.index_id",
					"sidecar.structured_output.artifact_path",
				} {
					{
						got, ok := jsonValueAtDottedPath(payload, path)
						gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("open-source context-pack proof missing %s: %#v", path, payload))
						gomega.Expect(jsonValueEmpty(got)).To(gomega.BeFalse(), fmt.Sprintf("open-source context-pack proof missing %s: %#v", path, payload))
					}
				}
			},
		},
	}
}

func writeP76ClassificationWorkspace(root string) {
	ginkgo.GinkgoHelper()

	writeSidecarRecipeFile(filepath.Join(root, "go.mod"), "module example.com/p76\n\ngo 1.22\n")
	writeSidecarRecipeFile(filepath.Join(root, "pkg", "payments", "retry.go"), `package payments

func RetryPayment(attempts int) bool {
	return attempts < 3
}
`)
	writeSidecarRecipeFile(filepath.Join(root, "docs", "CONTRIBUTING.md"), "# Contributing\n\nRun go test ./... before sending changes.\n")
}

func assertP76CoversDefaultClientClassifications(examples []p76ClassificationExample) {
	ginkgo.GinkgoHelper()

	seen := map[autonomy.ClientClassification]p76ClassificationExample{}
	for _, example := range examples {
		{
			_, exists := seen[example.classification]
			gomega.Expect(exists).To(gomega.BeFalse(), fmt.Sprintf("duplicate P7.6 classification example for %q", example.classification))
		}

		seen[example.classification] = example
	}

	for _, classification := range autonomy.DefaultClientClassifications() {
		example, ok := seen[classification]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing P7.6 classification example for %q", classification))

		gomega.Expect(example.commandID).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("classification %q missing executable command id", classification))
	}

	gomega.Expect(seen).To(gomega.HaveLen(len(autonomy.DefaultClientClassifications())), fmt.Sprintf("P7.6 examples include non-default classifications: %#v", seen))
}

func assertP76SidecarCommon(payload map[string]any, tool string, profile string, classification autonomy.ClientClassification) {
	ginkgo.GinkgoHelper()

	assertSidecarCompatLegacyFields(payload)
	assertP76JSONPath(payload, "sidecar.schema_version", "idiom.tool.v1")
	assertP76JSONPath(payload, "sidecar.tool", tool)
	assertP76JSONPath(payload, "sidecar.profile", profile)
	assertP76JSONPath(payload, "sidecar.contract.id", "idiom.tool.v1:"+tool)
	assertP76JSONPath(payload, "sidecar.contract.transport", "cli")
	assertP76SidecarClassificationMetadata(payload, classification)
	assertP76SidecarProductSignals(payload)
	assertNoPublicToolPayloadStrategyLeakage(payload)
}

func assertP76SidecarClassificationMetadata(payload map[string]any, classification autonomy.ClientClassification) {
	ginkgo.GinkgoHelper()

	values, ok := p76StringSliceAtPath(payload, "sidecar.contract.metadata.client_classifications")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing sidecar classification metadata in %#v", payload))

	gomega.Expect(values).To(gomega.HaveLen(len(autonomy.DefaultClientClassifications())), fmt.Sprintf("sidecar should carry all generic classifications, got %#v", values))

	for _, want := range autonomy.DefaultClientClassifications() {
		gomega.Expect(p76ContainsString(values, string(want))).To(gomega.BeTrue(), fmt.Sprintf("sidecar metadata missing classification %q: %#v", want, values))
	}

	gomega.Expect(p76ContainsString(values, string(classification))).To(gomega.BeTrue(), fmt.Sprintf("sidecar metadata missing current classification %q: %#v", classification, values))
}

func assertP76SidecarProductSignals(payload map[string]any) {
	ginkgo.GinkgoHelper()

	values, ok := p76StringSliceAtPath(payload, "sidecar.contract.metadata.product_signals")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing sidecar product signal metadata in %#v", payload))

	for _, want := range autonomy.DefaultProductSignals() {
		gomega.Expect(p76ContainsString(values, string(want))).To(gomega.BeTrue(), fmt.Sprintf("sidecar product metadata missing signal %q: %#v", want, values))
	}
}

func assertP76ClassificationInMetadata(values []autonomy.ClientClassification, want autonomy.ClientClassification) {
	ginkgo.GinkgoHelper()

	gomega.Expect(values).To(gomega.HaveLen(len(autonomy.DefaultClientClassifications())), fmt.Sprintf("schema metadata should carry all generic classifications, got %#v", values))

	for _, classification := range autonomy.DefaultClientClassifications() {
		found := false

		for _, value := range values {
			if value == classification {
				found = true
				break
			}
		}

		gomega.Expect(found).To(gomega.BeTrue(), fmt.Sprintf("schema metadata missing classification %q: %#v", classification, values))
	}

	gomega.Expect(values).To(gomega.ContainElement(want), fmt.Sprintf("schema metadata missing current classification %q: %#v", want, values))
}

func assertP76ProductSignalInMetadata(values []autonomy.ProductSignal, want autonomy.ProductSignal) {
	ginkgo.GinkgoHelper()

	gomega.Expect(values).To(gomega.ContainElement(want), fmt.Sprintf("schema metadata missing product signal %q: %#v", want, values))
}

func assertP76JSONPath(payload map[string]any, path string, want string) {
	ginkgo.GinkgoHelper()

	got, ok := jsonValueAtDottedPath(payload, path)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing JSON path %s in %#v", path, payload))

	gomega.Expect(fmt.Sprint(got)).To(gomega.BeEquivalentTo(want), fmt.Sprintf("JSON path %s = %q, want %q", path, fmt.Sprint(got), want))
}

func p76StringSliceAtPath(payload map[string]any, path string) ([]string, bool) {
	value, ok := jsonValueAtDottedPath(payload, path)
	if !ok {
		return nil, false
	}

	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}

	values := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}

		values = append(values, text)
	}

	return values, true
}

func p76ContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}

func assertP76NoForbiddenExampleLeakage(output string) {
	ginkgo.GinkgoHelper()

	for _, forbidden := range []string{
		"P76_FINANCE_SENTINEL_DO_NOT_LEAK",
		"P76_HEALTHCARE_SENTINEL_DO_NOT_LEAK",
		"RAW_PROMPT_BODY_SENTINEL",
		"RAW_FILE_BODY_SENTINEL",
		"PRIVATE_PATH_SENTINEL",
		"account_number",
		"patient_name",
		"customer_identifier",
	} {
		gomega.Expect(output).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("P7.6 example leaked forbidden marker %q:\n%s", forbidden, output))
	}
}
