package sidecarproof

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"idiom/internal/autonomy"
	"idiom/internal/localops"
)

type p76ClassificationExample struct {
	classification   autonomy.ClientClassification
	commandID        string
	overlayID        string
	limitation       string
	expectedExitCode int
	args             func(t *testing.T, root string, configPath string) []string
	assert           func(t *testing.T, stdout []byte, root string, configPath string)
}

func TestP76CLIClassificationExamplesUseGenericPrimitives(t *testing.T) {
	t.Setenv("IDIOM_TELEMETRY", "0")

	examples := p76ClassificationExamples()
	assertP76CoversDefaultClientClassifications(t, examples)

	for _, example := range examples {
		t.Run(string(example.classification), func(t *testing.T) {
			if strings.TrimSpace(example.overlayID) == "" || strings.TrimSpace(example.limitation) == "" {
				t.Fatalf("example must carry explicit overlay metadata and limitation: %#v", example)
			}
			root := t.TempDir()
			writeP76ClassificationWorkspace(t, root)
			configPath := writeMainConfigRoot(t, root)
			if example.classification == autonomy.ClientLocalModel {
				stageDoctorConfigWithBudget(t, root, 4096, 512)
				configPath = filepath.Join(root, ".idiom", "config.json")
			}

			var stdout, stderr bytes.Buffer
			code := runMain(example.args(t, root, configPath), &stdout, &stderr)
			if code != example.expectedExitCode {
				t.Fatalf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, example.expectedExitCode, stdout.String(), stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("P7.6 example should keep diagnostics in structured stdout, stderr=%s", stderr.String())
			}
			assertP76NoForbiddenExampleLeakage(t, stdout.String())
			example.assert(t, stdout.Bytes(), root, configPath)
		})
	}
}

func p76ClassificationExamples() []p76ClassificationExample {
	return []p76ClassificationExample{
		{
			classification:   autonomy.ClientFinance,
			commandID:        "finance-discovery-policy",
			overlayID:        "finance-proof-overlay",
			limitation:       "proof-only: compliance posture and regulated write automation remain deferred to P7.9/P9.9",
			expectedExitCode: 1,
			args: func(t *testing.T, root string, configPath string) []string {
				return []string{
					"tool", "write_file",
					"-config", configPath,
					"--profile", "discovery",
					"--caller-id", "fin-audit",
					"--request-id", "p76-fin",
					"--input", `{"path":"regulated/ledger.txt","content":"P76_FINANCE_SENTINEL_DO_NOT_LEAK"}`,
				}
			},
			assert: func(t *testing.T, stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(t, stdout)
				assertP76SidecarCommon(t, payload, "write_file", "discovery", autonomy.ClientFinance)
				assertP76JSONPath(t, payload, "sidecar.caller.caller_id", "fin-audit")
				assertP76JSONPath(t, payload, "sidecar.error.class", "profile_denied")
				assertP76JSONPath(t, payload, "sidecar.error.code", "profile_denied")
				if _, err := os.Stat(filepath.Join(root, "regulated", "ledger.txt")); !os.IsNotExist(err) {
					t.Fatalf("discovery-profile finance proof should not write regulated fixture; stat err=%v", err)
				}
			},
		},
		{
			classification:   autonomy.ClientHealthcare,
			commandID:        "healthcare-redaction-denial",
			overlayID:        "healthcare-proof-overlay",
			limitation:       "proof-only: no HIPAA compliance claim; PHI fixtures and posture checks remain deferred to P7.9/P9.9",
			expectedExitCode: 1,
			args: func(t *testing.T, root string, configPath string) []string {
				return []string{
					"tool", "context_pack",
					"-config", configPath,
					"--profile", "discovery",
					"--caller-id", "hc-review",
					"--request-id", "p76-hc",
					"--input", `{"task":"P76_HEALTHCARE_SENTINEL_DO_NOT_LEAK","mode":"plan","limit":1}`,
				}
			},
			assert: func(t *testing.T, stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(t, stdout)
				assertP76SidecarCommon(t, payload, "context_pack", "discovery", autonomy.ClientHealthcare)
				assertP76JSONPath(t, payload, "sidecar.error.class", "profile_denied")
				if _, err := os.Stat(filepath.Join(root, ".idiom", "comprehension")); !os.IsNotExist(err) {
					t.Fatalf("discovery-profile healthcare proof should not create context artifacts; stat err=%v", err)
				}
			},
		},
		{
			classification:   autonomy.ClientPublicSector,
			commandID:        "public-sector-offline-side-effect-denial",
			overlayID:        "public-sector-proof-overlay",
			limitation:       "proof-only: certification, offline procurement readiness, and reviewed posture remain deferred to P7.9/P9.9",
			expectedExitCode: 1,
			args: func(t *testing.T, root string, configPath string) []string {
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
			assert: func(t *testing.T, stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(t, stdout)
				assertP76SidecarCommon(t, payload, "shell", "discovery", autonomy.ClientPublicSector)
				assertP76JSONPath(t, payload, "sidecar.error.class", "profile_denied")
				if _, err := os.Stat(filepath.Join(root, "network-side-effect.txt")); !os.IsNotExist(err) {
					t.Fatalf("public-sector discovery proof should not perform shell side effect; stat err=%v", err)
				}
			},
		},
		{
			classification:   autonomy.ClientEnterpriseSaaS,
			commandID:        "enterprise-source-sanity-contract",
			overlayID:        "enterprise-saas-ci-overlay",
			limitation:       "MVP proof: merge-facing compliance and retained buyer value remain P7.10/P7.11/P9.7/P9.9 scope",
			expectedExitCode: 0,
			args: func(t *testing.T, root string, configPath string) []string {
				return []string{
					"tool", "source_sanity",
					"-config", configPath,
					"--caller-id", "ci-agent",
					"--request-id", "p76-saas",
					"--trace-id", "trace:p76-saas",
					"--input", `{"path":"pkg/payments/retry.go"}`,
				}
			},
			assert: func(t *testing.T, stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(t, stdout)
				assertP76SidecarCommon(t, payload, "source_sanity", "default", autonomy.ClientEnterpriseSaaS)
				assertP76JSONPath(t, payload, "sidecar.caller.trace_id", "trace:p76-saas")
				assertP76JSONPath(t, payload, "sidecar.structured_output.format", "text")
				if got, _ := jsonValueAtDottedPath(payload, "sidecar.error"); got != nil {
					t.Fatalf("enterprise source-sanity proof should succeed, got error %#v", got)
				}
			},
		},
		{
			classification:   autonomy.ClientToolingVendor,
			commandID:        "tooling-vendor-schema-metadata",
			overlayID:        "tooling-vendor-schema-overlay",
			limitation:       "MVP proof: MCP resource parity and partner conformance remain P8.5/P9.9 scope",
			expectedExitCode: 0,
			args: func(t *testing.T, root string, configPath string) []string {
				return []string{"tool", "schemas"}
			},
			assert: func(t *testing.T, stdout []byte, root string, configPath string) {
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
				if err := json.Unmarshal(stdout, &export); err != nil {
					t.Fatalf("decode schema export: %v\n%s", err, string(stdout))
				}
				if export.SchemaVersion == "" || len(export.Tools) == 0 {
					t.Fatalf("tooling-vendor schema proof should expose schema and tools: %#v", export)
				}
				assertP76ClassificationInMetadata(t, export.Metadata.ClientClassifications, autonomy.ClientToolingVendor)
				assertP76ProductSignalInMetadata(t, export.Metadata.ProductSignals, autonomy.ProductIntegrationProof)
			},
		},
		{
			classification:   autonomy.ClientLocalModel,
			commandID:        "local-model-readiness-evidence",
			overlayID:        "local-model-readiness-overlay",
			limitation:       "MVP proof: model quality, live capacity, and operational reliability remain P6/P7.9/P9.9 scope",
			expectedExitCode: 0,
			args: func(t *testing.T, root string, configPath string) []string {
				return []string{
					"local-ops", "benchmark",
					"--json",
					"--config", configPath,
					"--profile", "implement",
				}
			},
			assert: func(t *testing.T, stdout []byte, root string, configPath string) {
				var report localops.BenchmarkReport
				if err := json.Unmarshal(stdout, &report); err != nil {
					t.Fatalf("decode local-ops report: %v\n%s", err, string(stdout))
				}
				if report.SchemaVersion != localops.BenchmarkSchemaVersion || len(report.Profiles) != 1 {
					t.Fatalf("local-model proof should emit one schema-versioned profile report: %#v", report)
				}
				profile := report.Profiles[0]
				if profile.Name != "implement" || profile.Readiness.Contract == "" ||
					profile.Readiness.QueueState == "" || profile.Readiness.ContextBudgetState == "" {
					t.Fatalf("local-model proof missing machine-readable readiness evidence: %#v", profile)
				}
				if !report.Privacy.MetadataOnly || report.Privacy.RawPromptCaptured ||
					report.Privacy.RawFileBodyCaptured || report.Privacy.SecretCaptureAllowed {
					t.Fatalf("local-model proof should be metadata-only privacy evidence: %#v", report.Privacy)
				}
			},
		},
		{
			classification:   autonomy.ClientOpenSource,
			commandID:        "open-source-context-pack",
			overlayID:        "open-source-quickstart-overlay",
			limitation:       "MVP proof: full demo transcript, cleanup timing, and retained-use evidence remain P7.10/P7.11/P9.7 scope",
			expectedExitCode: 0,
			args: func(t *testing.T, root string, configPath string) []string {
				return []string{
					"tool", "context_pack",
					"-config", configPath,
					"--caller-id", "oss-user",
					"--request-id", "p76-oss",
					"--input", `{"task":"localize contributor docs","mode":"plan","limit":4}`,
				}
			},
			assert: func(t *testing.T, stdout []byte, root string, configPath string) {
				payload := decodeSidecarCompatJSON(t, stdout)
				assertP76SidecarCommon(t, payload, "context_pack", "default", autonomy.ClientOpenSource)
				assertP76JSONPath(t, payload, "sidecar.freshness.policy", "read_through")
				assertP76JSONPath(t, payload, "sidecar.structured_output.task", "localize contributor docs")
				for _, path := range []string{
					"sidecar.structured_output.pack_id",
					"sidecar.structured_output.index_id",
					"sidecar.structured_output.artifact_path",
				} {
					if got, ok := jsonValueAtDottedPath(payload, path); !ok || jsonValueEmpty(got) {
						t.Fatalf("open-source context-pack proof missing %s: %#v", path, payload)
					}
				}
			},
		},
	}
}

func writeP76ClassificationWorkspace(t *testing.T, root string) {
	t.Helper()
	writeSidecarRecipeFile(t, filepath.Join(root, "go.mod"), "module example.com/p76\n\ngo 1.22\n")
	writeSidecarRecipeFile(t, filepath.Join(root, "pkg", "payments", "retry.go"), `package payments

func RetryPayment(attempts int) bool {
	return attempts < 3
}
`)
	writeSidecarRecipeFile(t, filepath.Join(root, "docs", "CONTRIBUTING.md"), "# Contributing\n\nRun go test ./... before sending changes.\n")
}

func assertP76CoversDefaultClientClassifications(t *testing.T, examples []p76ClassificationExample) {
	t.Helper()
	seen := map[autonomy.ClientClassification]p76ClassificationExample{}
	for _, example := range examples {
		if _, exists := seen[example.classification]; exists {
			t.Fatalf("duplicate P7.6 classification example for %q", example.classification)
		}
		seen[example.classification] = example
	}
	for _, classification := range autonomy.DefaultClientClassifications() {
		example, ok := seen[classification]
		if !ok {
			t.Fatalf("missing P7.6 classification example for %q", classification)
		}
		if example.commandID == "" {
			t.Fatalf("classification %q missing executable command id", classification)
		}
	}
	if len(seen) != len(autonomy.DefaultClientClassifications()) {
		t.Fatalf("P7.6 examples include non-default classifications: %#v", seen)
	}
}

func assertP76SidecarCommon(t *testing.T, payload map[string]any, tool string, profile string, classification autonomy.ClientClassification) {
	t.Helper()
	assertSidecarCompatLegacyFields(t, payload)
	assertP76JSONPath(t, payload, "sidecar.schema_version", "idiom.tool.v1")
	assertP76JSONPath(t, payload, "sidecar.tool", tool)
	assertP76JSONPath(t, payload, "sidecar.profile", profile)
	assertP76JSONPath(t, payload, "sidecar.contract.id", "idiom.tool.v1:"+tool)
	assertP76JSONPath(t, payload, "sidecar.contract.transport", "cli")
	assertP76SidecarClassificationMetadata(t, payload, classification)
	assertP76SidecarProductSignals(t, payload)
	assertNoPublicToolPayloadStrategyLeakage(t, payload)
}

func assertP76SidecarClassificationMetadata(t *testing.T, payload map[string]any, classification autonomy.ClientClassification) {
	t.Helper()
	values, ok := p76StringSliceAtPath(payload, "sidecar.contract.metadata.client_classifications")
	if !ok {
		t.Fatalf("missing sidecar classification metadata in %#v", payload)
	}
	if len(values) != len(autonomy.DefaultClientClassifications()) {
		t.Fatalf("sidecar should carry all generic classifications, got %#v", values)
	}
	for _, want := range autonomy.DefaultClientClassifications() {
		if !p76ContainsString(values, string(want)) {
			t.Fatalf("sidecar metadata missing classification %q: %#v", want, values)
		}
	}
	if !p76ContainsString(values, string(classification)) {
		t.Fatalf("sidecar metadata missing current classification %q: %#v", classification, values)
	}
}

func assertP76SidecarProductSignals(t *testing.T, payload map[string]any) {
	t.Helper()
	values, ok := p76StringSliceAtPath(payload, "sidecar.contract.metadata.product_signals")
	if !ok {
		t.Fatalf("missing sidecar product signal metadata in %#v", payload)
	}
	for _, want := range autonomy.DefaultProductSignals() {
		if !p76ContainsString(values, string(want)) {
			t.Fatalf("sidecar product metadata missing signal %q: %#v", want, values)
		}
	}
}

func assertP76ClassificationInMetadata(t *testing.T, values []autonomy.ClientClassification, want autonomy.ClientClassification) {
	t.Helper()
	if len(values) != len(autonomy.DefaultClientClassifications()) {
		t.Fatalf("schema metadata should carry all generic classifications, got %#v", values)
	}
	for _, classification := range autonomy.DefaultClientClassifications() {
		found := false
		for _, value := range values {
			if value == classification {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("schema metadata missing classification %q: %#v", classification, values)
		}
	}
	for _, value := range values {
		if value == want {
			return
		}
	}
	t.Fatalf("schema metadata missing current classification %q: %#v", want, values)
}

func assertP76ProductSignalInMetadata(t *testing.T, values []autonomy.ProductSignal, want autonomy.ProductSignal) {
	t.Helper()
	for _, signal := range values {
		if signal == want {
			return
		}
	}
	t.Fatalf("schema metadata missing product signal %q: %#v", want, values)
}

func assertP76JSONPath(t *testing.T, payload map[string]any, path string, want string) {
	t.Helper()
	got, ok := jsonValueAtDottedPath(payload, path)
	if !ok {
		t.Fatalf("missing JSON path %s in %#v", path, payload)
	}
	if fmt.Sprint(got) != want {
		t.Fatalf("JSON path %s = %q, want %q", path, fmt.Sprint(got), want)
	}
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

func assertP76NoForbiddenExampleLeakage(t *testing.T, output string) {
	t.Helper()
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
		if strings.Contains(output, forbidden) {
			t.Fatalf("P7.6 example leaked forbidden marker %q:\n%s", forbidden, output)
		}
	}
}
