package sidecarvalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"strings"
)

type sidecarSmokeTranscript struct {
	SchemaVersion string             `json:"schema_version"`
	Steps         []sidecarSmokeStep `json:"steps"`
}

type sidecarSmokeStep struct {
	ID                 string            `json:"id"`
	Command            string            `json:"command"`
	DocumentedRecipe   bool              `json:"documented_recipe"`
	Argv               []string          `json:"argv"`
	ExitCode           int               `json:"exit_code"`
	JSON               bool              `json:"json"`
	ExpectJSONString   map[string]string `json:"expect_json_string"`
	ExpectJSONPresent  []string          `json:"expect_json_present"`
	ExpectJSONNull     []string          `json:"expect_json_null"`
	ExpectJSONNonEmpty []string          `json:"expect_json_non_empty"`
	StdoutIncludes     []string          `json:"stdout_includes"`
	StdoutExcludes     []string          `json:"stdout_excludes"`
}

type p78IntegrationProofSet struct {
	SchemaVersion  string                `json:"schema_version"`
	SharedContract p78SharedContract     `json:"shared_contract"`
	Proofs         []p78IntegrationProof `json:"proofs"`
}

type p78SharedContract struct {
	SidecarSchemaVersion    string   `json:"sidecar_schema_version"`
	Profile                 string   `json:"profile"`
	RequiredEnvelopeFields  []string `json:"required_envelope_fields"`
	RequiredReadinessFields []string `json:"required_readiness_fields"`
	PrivacyForbiddenClasses []string `json:"privacy_forbidden_classes"`
}

type p78IntegrationProof struct {
	ID                          string          `json:"id"`
	Path                        string          `json:"path"`
	Status                      string          `json:"status"`
	RecipeDoc                   string          `json:"recipe_doc"`
	ScriptPath                  string          `json:"script_path"`
	SetupStepIDs                []string        `json:"setup_step_ids"`
	TranscriptStepIDs           []string        `json:"transcript_step_ids"`
	Phase8WorkPackage           string          `json:"phase8_work_package"`
	ReplayFixture               string          `json:"replay_fixture"`
	ReplayTest                  string          `json:"replay_test"`
	ProductEvidenceExportParity bool            `json:"product_evidence_export_parity"`
	ClientClassifications       []string        `json:"client_classifications"`
	Consumes                    p78Consumes     `json:"consumes"`
	MCPContract                 map[string]any  `json:"mcp_contract"`
	Readiness                   map[string]bool `json:"readiness"`
	Limitations                 []string        `json:"limitations"`
	Privacy                     map[string]bool `json:"privacy"`
}

type p78Consumes struct {
	Profile   bool `json:"profile"`
	Schema    bool `json:"schema"`
	Freshness bool `json:"freshness"`
	Audit     bool `json:"audit"`
	Contract  bool `json:"contract"`
}

type p86MCPIntegrationProof struct {
	LinkedP78ProofID string `json:"linked_p7_8_proof_id"`
}

var _ = ginkgo.It("sidecar smoke transcript", func() {
	t := ginkgo.GinkgoT()
	repoRoot := sidecarValidationRepoRoot()
	transcriptPath := filepath.Join(repoRoot, "testdata", "sidecar", "discovery-smoke.transcript.json")

	data, err := os.ReadFile(transcriptPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read smoke transcript: %v", err))

	var transcript sidecarSmokeTranscript

	gomega.Expect(json.Unmarshal(data, &transcript)).To(gomega.Succeed(), "decode smoke transcript")

	gomega.Expect(transcript.SchemaVersion).To(gomega.BeEquivalentTo("idiom.sidecar_smoke_transcript.v1"), fmt.Sprintf("unexpected smoke transcript header: %#v", transcript))
	gomega.Expect(transcript.Steps).NotTo(gomega.HaveLen(0), fmt.Sprintf("unexpected smoke transcript header: %#v", transcript))

	root := t.TempDir()
	writeSidecarRecipeWorkspace(root)

	configPath := writeMainConfigRoot(root)
	for _, step := range transcript.Steps {
		ginkgo.By(step.ID)
		func() {
			var stdout, stderr bytes.Buffer

			code := runMain(sidecarSmokeArgsWithConfig(step.Argv, configPath), &stdout, &stderr)

			gomega.Expect(code).To(gomega.BeEquivalentTo(step.ExitCode), fmt.Sprintf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, step.ExitCode, stdout.String(), stderr.String()))

			for _, want := range step.StdoutIncludes {
				gomega.Expect(stdout.String()).To(gomega.ContainSubstring(want), fmt.Sprintf("stdout missing %q:\n%s", want, stdout.String()))
			}

			for _, forbidden := range step.StdoutExcludes {
				gomega.Expect(stdout.String()).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("stdout unexpectedly contains %q:\n%s", forbidden, stdout.String()))
			}

			if !step.JSON {
				return
			}

			var payload any

			gomega.Expect(json.Unmarshal(stdout.Bytes(), &payload)).To(gomega.Succeed(), "decode JSON stdout:\n%s", stdout.String())

			for path, want := range step.ExpectJSONString {
				got, ok := jsonValueAtDottedPath(payload, path)
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing JSON path %s in %s", path, stdout.String()))

				gomega.Expect(fmt.Sprint(got)).To(gomega.BeEquivalentTo(want), fmt.Sprintf("JSON path %s = %q, want %q", path, fmt.Sprint(got), want))
			}

			for _, path := range step.ExpectJSONNull {
				got, ok := jsonValueAtDottedPath(payload, path)
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing JSON path %s in %s", path, stdout.String()))

				gomega.Expect(got).To(gomega.BeNil(), fmt.Sprintf("JSON path %s = %#v, want null", path, got))
			}

			for _, path := range step.ExpectJSONPresent {
				{
					_, ok := jsonValueAtDottedPath(payload, path)
					gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing JSON path %s in %s", path, stdout.String()))
				}
			}

			for _, path := range step.ExpectJSONNonEmpty {
				got, ok := jsonValueAtDottedPath(payload, path)
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("missing JSON path %s in %s", path, stdout.String()))

				gomega.Expect(jsonValueEmpty(got)).To(gomega.BeFalse(), fmt.Sprintf("JSON path %s is empty: %#v", path, got))
			}
		}()
	}
})
var _ = ginkgo.It("p78 integration proof records", func() {
	repoRoot := sidecarValidationRepoRoot()
	proofsPath := filepath.Join(repoRoot, "testdata", "sidecar", "integration-proofs.json")

	data, err := os.ReadFile(proofsPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read integration proofs: %v", err))

	var proofs p78IntegrationProofSet

	gomega.Expect(json.Unmarshal(data, &proofs)).To(gomega.Succeed(), "decode integration proofs")

	gomega.Expect(proofs.SchemaVersion).To(gomega.BeEquivalentTo("idiom.sidecar_integration_proofs.v1"), fmt.Sprintf("unexpected proof schema version %q", proofs.SchemaVersion))

	gomega.Expect(proofs.SharedContract.SidecarSchemaVersion).To(gomega.BeEquivalentTo("idiom.tool.v1"), fmt.Sprintf("proofs are not tied to the shared discovery sidecar contract: %#v", proofs.SharedContract))
	gomega.Expect(proofs.SharedContract.Profile).To(gomega.BeEquivalentTo("discovery"), fmt.Sprintf("proofs are not tied to the shared discovery sidecar contract: %#v", proofs.SharedContract))

	for _, required := range []string{
		"sidecar.schema_version",
		"sidecar.profile",
		"sidecar.contract",
		"sidecar.freshness",
		"sidecar.audit_event_ids",
		"sidecar.structured_output",
		"sidecar.error",
	} {
		gomega.Expect(stringSliceContains(proofs.SharedContract.RequiredEnvelopeFields, required)).To(gomega.BeTrue(), fmt.Sprintf("shared contract missing required envelope field %q", required))
	}

	for _, required := range proofs.SharedContract.RequiredReadinessFields {
		gomega.Expect(strings.TrimSpace(required)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("blank readiness field in shared contract: %#v", proofs.SharedContract.RequiredReadinessFields))
	}

	transcript, err := readSidecarSmokeTranscript(repoRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read smoke transcript: %v", err))

	transcriptSteps := map[string]sidecarSmokeStep{}
	for _, step := range transcript.Steps {
		transcriptSteps[step.ID] = step
	}

	docData, err := os.ReadFile(filepath.Join(repoRoot, "docs", "sidecar-integration-proofs.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read integration proof docs: %v", err))

	doc := string(docData)

	seenPaths := map[string]bool{}

	for _, proof := range proofs.Proofs {
		gomega.Expect(proof.ID).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("proof missing id/path: %#v", proof))
		gomega.Expect(proof.Path).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("proof missing id/path: %#v", proof))

		seenPaths[proof.Path] = true
		gomega.Expect(doc).To(gomega.ContainSubstring(proof.Path), fmt.Sprintf("integration proof docs do not mention path %q", proof.Path))

		gomega.Expect(proof.Consumes.Profile).To(gomega.BeTrue(), fmt.Sprintf("proof %s does not consume the complete shared sidecar contract: %#v", proof.ID, proof.Consumes))
		gomega.Expect(proof.Consumes.Schema).To(gomega.BeTrue(), fmt.Sprintf("proof %s does not consume the complete shared sidecar contract: %#v", proof.ID, proof.Consumes))
		gomega.Expect(proof.Consumes.Freshness).To(gomega.BeTrue(), fmt.Sprintf("proof %s does not consume the complete shared sidecar contract: %#v", proof.ID, proof.Consumes))
		gomega.Expect(proof.Consumes.Audit).To(gomega.BeTrue(), fmt.Sprintf("proof %s does not consume the complete shared sidecar contract: %#v", proof.ID, proof.Consumes))
		gomega.Expect(proof.Consumes.Contract).To(gomega.BeTrue(), fmt.Sprintf("proof %s does not consume the complete shared sidecar contract: %#v", proof.ID, proof.Consumes))

		for _, required := range proofs.SharedContract.RequiredReadinessFields {
			gomega.Expect(proof.Readiness[required]).To(gomega.BeTrue(), fmt.Sprintf("proof %s readiness %q is not true: %#v", proof.ID, required, proof.Readiness))
		}

		for field, leaked := range proof.Privacy {
			gomega.Expect(leaked).To(gomega.BeFalse(), fmt.Sprintf("proof %s privacy field %s reports leaked content", proof.ID, field))
		}

		proofPayload, err := json.Marshal(proof)
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal proof %s for privacy scan: %v", proof.ID, err))

		for _, pattern := range []string{
			"/Users/",
			"CustomerX",
			"private-repo",
			"sidecar-private-body-sentinel",
			"sidecar-auth-retry-sentinel",
			"sk-",
			"ghp_",
			"github_pat_",
			"xoxb-",
		} {
			gomega.Expect(string(proofPayload)).NotTo(gomega.ContainSubstring(pattern), fmt.Sprintf("integration proof fixture contains forbidden privacy pattern %q", pattern))
		}

		switch proof.Path {
		case "shell_ci_script":
			assertP78ExecutableProof(proof, transcriptSteps)

			gomega.Expect(proof.ScriptPath).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("shell/CI proof must reference a script path"))

			scriptData, err := os.ReadFile(filepath.Join(repoRoot, proof.ScriptPath))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read shell/CI proof script: %v", err))

			script := string(scriptData)
			for _, required := range []string{"idiom.tool.v1", "--profile discovery", "sidecar.audit_event_ids", "sidecar.freshness.policy"} {
				gomega.Expect(script).To(gomega.ContainSubstring(required), fmt.Sprintf("shell/CI proof script missing %q", required))
			}
		case "external_coding_agent_workflow":
			assertP78ExecutableProof(proof, transcriptSteps)

			gomega.Expect(proof.ScriptPath).To(gomega.BeEquivalentTo(""), fmt.Sprintf("external coding-agent proof should be a generic workflow recipe, not a script-specific path"))
		case "mcp_stdio":
			gomega.Expect(proof.Status).To(gomega.BeEquivalentTo("test_validated"), fmt.Sprintf("MCP proof must be test validated, got %q", proof.Status))

			gomega.Expect(proof.Readiness["protocol_implemented"]).To(gomega.BeTrue(), fmt.Sprintf("MCP proof readiness is not executable: %#v", proof.Readiness))
			gomega.Expect(proof.Readiness["phase8_deferred"]).To(gomega.BeFalse(), fmt.Sprintf("MCP proof readiness is not executable: %#v", proof.Readiness))
			gomega.Expect(proof.Readiness["reviewed_placeholder"]).To(gomega.BeFalse(), fmt.Sprintf("MCP proof readiness is not executable: %#v", proof.Readiness))
			gomega.Expect(proof.Readiness["executable_evidence"]).To(gomega.BeTrue(), fmt.Sprintf("MCP proof readiness is not executable: %#v", proof.Readiness))

			gomega.Expect(proof.Phase8WorkPackage).To(gomega.BeEquivalentTo("P8.6"), fmt.Sprintf("MCP proof must tie back to P8.6, got %q", proof.Phase8WorkPackage))

			gomega.Expect(proof.ReplayFixture).To(gomega.BeEquivalentTo("testdata/sidecar/mcp-stdio-integration-proof.json"), fmt.Sprintf("MCP proof missing replay/product-evidence linkage: %#v", proof))
			gomega.Expect(proof.ReplayTest).To(gomega.Equal("go test ./internal/surfaces/cli/mcp -ginkgo.focus='P8\\.6 MCP stdio proof replays the release proof without leaking protected evidence' -ginkgo.fail-on-empty -count=1"), fmt.Sprintf("MCP proof missing executable Ginkgo replay linkage: %#v", proof))
			gomega.Expect(proof.ProductEvidenceExportParity).To(gomega.BeTrue(), fmt.Sprintf("MCP proof missing replay/product-evidence linkage: %#v", proof))

			readinessFields, ok := proof.MCPContract["readiness_fields"].([]any)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("MCP contract missing readiness_fields: %#v", proof.MCPContract))
			gomega.Expect(readinessFields).NotTo(gomega.HaveLen(0), fmt.Sprintf("MCP contract missing readiness_fields: %#v", proof.MCPContract))

			for _, value := range readinessFields {
				field, ok := value.(string)
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("MCP contract has malformed readiness field %#v: %#v", value, proof.MCPContract))
				gomega.Expect(strings.TrimSpace(field)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("MCP contract has malformed readiness field %#v: %#v", value, proof.MCPContract))

				gomega.Expect(proof.Readiness[field]).To(gomega.BeTrue(), fmt.Sprintf("MCP proof missing declared readiness field %q: %#v", field, proof.Readiness))
			}

			gomega.Expect(proof.Readiness["roots_resources_present"]).To(gomega.BeTrue(), fmt.Sprintf("MCP proof missing required P8.6 readiness fields: %#v", proof.Readiness))

			gomega.Expect(proof.MCPContract["transport"]).To(gomega.BeEquivalentTo("mcp_stdio"), fmt.Sprintf("MCP contract does not preserve sidecar shape: %#v", proof.MCPContract))
			gomega.Expect(proof.MCPContract["sidecar_schema_version"]).To(gomega.BeEquivalentTo("idiom.tool.v1"), fmt.Sprintf("MCP contract does not preserve sidecar shape: %#v", proof.MCPContract))
			gomega.Expect(proof.MCPContract["profile"]).To(gomega.BeEquivalentTo("discovery"), fmt.Sprintf("MCP contract does not preserve sidecar shape: %#v", proof.MCPContract))

			for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
				gomega.Expect(stringSliceContains(proof.ClientClassifications, class)).To(gomega.BeTrue(), fmt.Sprintf("MCP proof missing classification %q: %#v", class, proof.ClientClassifications))
			}

			replayData, err := os.ReadFile(filepath.Join(repoRoot, proof.ReplayFixture))
			gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read MCP replay fixture: %v", err))

			var replay p86MCPIntegrationProof

			gomega.Expect(json.Unmarshal(replayData, &replay)).To(gomega.Succeed(), "decode MCP replay fixture")

			gomega.Expect(replay.LinkedP78ProofID).To(gomega.BeEquivalentTo(proof.ID), fmt.Sprintf("MCP replay fixture links to %q, want %q", replay.LinkedP78ProofID, proof.ID))

			gomega.Expect(strings.Join(proof.Limitations, " ")).To(gomega.ContainSubstring("metadata-only"), fmt.Sprintf("MCP proof must preserve resource-body limitation: %#v", proof.Limitations))
		default:
			ginkgo.Fail(fmt.Sprintf("unexpected proof path %q", proof.Path), 1)
		}
	}

	for _, requiredPath := range []string{"shell_ci_script", "external_coding_agent_workflow", "mcp_stdio"} {
		gomega.Expect(seenPaths[requiredPath]).To(gomega.BeTrue(), fmt.Sprintf("missing P7.8 proof path %q", requiredPath))
	}
})
var _ = ginkgo.It("sidecar recipes cover caller classes and compatibility", func() {
	repoRoot := sidecarValidationRepoRoot()

	data, err := os.ReadFile(filepath.Join(repoRoot, "docs", "sidecar-recipes.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read sidecar recipes: %v", err))

	doc := string(data)

	transcript, err := readSidecarSmokeTranscript(repoRoot)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read smoke transcript: %v", err))

	for _, required := range []string{
		"Shell Or CI Script",
		"External Coding-Agent Harness",
		"MCP Stdio",
		"Local Or Private-Code User",
		"sidecar.schema_version",
		"sidecar.profile",
		"sidecar.error",
		"sidecar.freshness",
		"sidecar.contract",
		"`Tool`, `Summary`, `Output`, and `Error`",
		"testdata/sidecar/discovery-smoke.transcript.json",
		"testdata/sidecar/integration-proofs.json",
		"The caller owns strategy",
		"Idiom owns the contract",
	} {
		gomega.Expect(doc).To(gomega.ContainSubstring(required), fmt.Sprintf("sidecar recipes missing %q", required))
	}

	documentedSteps := map[string]bool{}
	normalizedDoc := normalizedShellSnippet(doc)

	for _, step := range transcript.Steps {
		if !step.DocumentedRecipe {
			continue
		}

		documentedSteps[step.ID] = true
		gomega.Expect(normalizedDoc).To(gomega.ContainSubstring(normalizedShellSnippet(step.Command)), fmt.Sprintf("sidecar recipes do not document executable step %q command %q", step.ID, step.Command))
	}

	for _, requiredStep := range []string{
		"external-harness-context-query",
		"local-private-context-pack",
	} {
		gomega.Expect(documentedSteps[requiredStep]).To(gomega.BeTrue(), fmt.Sprintf("smoke transcript missing documented executable recipe step %q", requiredStep))
	}
})

func assertP78ExecutableProof(proof p78IntegrationProof, transcriptSteps map[string]sidecarSmokeStep) {
	ginkgo.GinkgoHelper()

	gomega.Expect(proof.Status).To(gomega.BeEquivalentTo("test_validated"), fmt.Sprintf("proof %s should be test-validated, got %q", proof.ID, proof.Status))

	gomega.Expect(proof.Readiness["executable_evidence"]).To(gomega.BeTrue(), fmt.Sprintf("proof %s readiness is not executable: %#v", proof.ID, proof.Readiness))
	gomega.Expect(proof.Readiness["reviewed_placeholder"]).To(gomega.BeFalse(), fmt.Sprintf("proof %s readiness is not executable: %#v", proof.ID, proof.Readiness))
	gomega.Expect(proof.Readiness["protocol_implemented"]).To(gomega.BeTrue(), fmt.Sprintf("proof %s readiness is not executable: %#v", proof.ID, proof.Readiness))
	gomega.Expect(proof.Readiness["phase8_deferred"]).To(gomega.BeFalse(), fmt.Sprintf("proof %s readiness is not executable: %#v", proof.ID, proof.Readiness))

	gomega.Expect(proof.TranscriptStepIDs).NotTo(gomega.BeEmpty(), fmt.Sprintf("proof %s must reference at least one transcript step", proof.ID))

	for _, stepID := range proof.SetupStepIDs {
		{
			_, ok := transcriptSteps[stepID]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("proof %s references missing setup transcript step %q", proof.ID, stepID))
		}
	}

	for _, stepID := range proof.TranscriptStepIDs {
		step, ok := transcriptSteps[stepID]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("proof %s references missing proof transcript step %q", proof.ID, stepID))

		if !step.JSON {
			continue
		}

		for _, required := range []string{"sidecar.schema_version", "sidecar.profile", "sidecar.contract.id"} {
			{
				_, ok := step.ExpectJSONString[required]
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("transcript step %s does not assert %s", stepID, required))
			}
		}

		{
			_, ok := step.ExpectJSONString["sidecar.freshness.policy"]
			if !ok {
				gomega.Expect(stringSliceContains(step.ExpectJSONNull, "sidecar.freshness")).To(gomega.BeTrue(), fmt.Sprintf("transcript step %s does not assert freshness presence or explicit null", stepID))
			}
		}

		gomega.Expect(stringSliceContains(step.ExpectJSONPresent, "sidecar.audit_event_ids")).To(gomega.BeTrue(), fmt.Sprintf("transcript step %s does not assert sidecar audit field presence", stepID))
	}
}

func readSidecarSmokeTranscript(repoRoot string) (sidecarSmokeTranscript, error) {
	transcriptPath := filepath.Join(repoRoot, "testdata", "sidecar", "discovery-smoke.transcript.json")

	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		return sidecarSmokeTranscript{}, err
	}

	var transcript sidecarSmokeTranscript

	if err := json.Unmarshal(data, &transcript); err != nil {
		return sidecarSmokeTranscript{}, err
	}

	return transcript, nil
}

func writeSidecarRecipeWorkspace(root string) {
	ginkgo.GinkgoHelper()

	writeSidecarRecipeFile(filepath.Join(root, "go.mod"), "module example.com/sidecar\n\ngo 1.22\n")
	writeSidecarRecipeFile(filepath.Join(root, "cmd", "tool", "main.go"), `package main

func main() {
	_ = callToolMain()
}

func callToolMain() string {
	return runToolMainWithTelemetry()
}

func runToolMainWithTelemetry() string {
	return "sidecar-private-body-sentinel"
}
`)
	writeSidecarRecipeFile(filepath.Join(root, "auth", "retry.go"), `package auth

func Authenticate() string {
	return retryRequest()
}

func retryRequest() string {
	return "sidecar-auth-retry-sentinel"
}
`)
}

func writeSidecarRecipeFile(path string, content string) {
	ginkgo.GinkgoHelper()

	gomega.Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(gomega.Succeed(), "create directory %s", filepath.Dir(path))

	gomega.Expect(os.WriteFile(path, []byte(content), 0o644)).To(gomega.Succeed(), "write %s", path)
}

func normalizedShellSnippet(value string) string {
	value = strings.ReplaceAll(value, "\\\r\n", " ")
	value = strings.ReplaceAll(value, "\\\n", " ")

	return strings.Join(strings.Fields(value), " ")
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

func sidecarSmokeArgsWithConfig(args []string, configPath string) []string {
	copied := append([]string(nil), args...)
	if len(copied) < 2 || copied[0] != "tool" {
		return copied
	}

	if copied[1] == "list" {
		return append(copied, "--config", configPath)
	}

	if copied[1] == "schemas" {
		return copied
	}

	withConfig := make([]string, 0, len(copied)+2)

	withConfig = append(withConfig, copied[:2]...)
	withConfig = append(withConfig, "-config", configPath)
	withConfig = append(withConfig, copied[2:]...)

	return withConfig
}

func jsonValueAtDottedPath(root any, path string) (any, bool) {
	current := root
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}

		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}

	return current, true
}

func jsonValueEmpty(value any) bool {
	if value == nil {
		return true
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return strings.TrimSpace(fmt.Sprint(value)) == ""
	}
}
