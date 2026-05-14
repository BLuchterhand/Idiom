package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestSidecarSmokeTranscript(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "../.."))
	transcriptPath := filepath.Join(repoRoot, "testdata", "sidecar", "discovery-smoke.transcript.json")
	data, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("read smoke transcript: %v", err)
	}
	var transcript sidecarSmokeTranscript
	if err := json.Unmarshal(data, &transcript); err != nil {
		t.Fatalf("decode smoke transcript: %v", err)
	}
	if transcript.SchemaVersion != "idiom.sidecar_smoke_transcript.v1" || len(transcript.Steps) == 0 {
		t.Fatalf("unexpected smoke transcript header: %#v", transcript)
	}

	root := t.TempDir()
	writeSidecarRecipeWorkspace(t, root)
	configPath := writeMainConfigRoot(t, root)
	for _, step := range transcript.Steps {
		t.Run(step.ID, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runMain(sidecarSmokeArgsWithConfig(step.Argv, configPath), &stdout, &stderr)
			if code != step.ExitCode {
				t.Fatalf("exit code = %d, want %d\nstdout=%s\nstderr=%s", code, step.ExitCode, stdout.String(), stderr.String())
			}
			for _, want := range step.StdoutIncludes {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
				}
			}
			for _, forbidden := range step.StdoutExcludes {
				if strings.Contains(stdout.String(), forbidden) {
					t.Fatalf("stdout unexpectedly contains %q:\n%s", forbidden, stdout.String())
				}
			}
			if !step.JSON {
				return
			}
			var payload any
			if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
				t.Fatalf("decode JSON stdout: %v\n%s", err, stdout.String())
			}
			for path, want := range step.ExpectJSONString {
				got, ok := jsonValueAtDottedPath(payload, path)
				if !ok {
					t.Fatalf("missing JSON path %s in %s", path, stdout.String())
				}
				if fmt.Sprint(got) != want {
					t.Fatalf("JSON path %s = %q, want %q", path, fmt.Sprint(got), want)
				}
			}
			for _, path := range step.ExpectJSONNull {
				got, ok := jsonValueAtDottedPath(payload, path)
				if !ok {
					t.Fatalf("missing JSON path %s in %s", path, stdout.String())
				}
				if got != nil {
					t.Fatalf("JSON path %s = %#v, want null", path, got)
				}
			}
			for _, path := range step.ExpectJSONPresent {
				if _, ok := jsonValueAtDottedPath(payload, path); !ok {
					t.Fatalf("missing JSON path %s in %s", path, stdout.String())
				}
			}
			for _, path := range step.ExpectJSONNonEmpty {
				got, ok := jsonValueAtDottedPath(payload, path)
				if !ok {
					t.Fatalf("missing JSON path %s in %s", path, stdout.String())
				}
				if jsonValueEmpty(got) {
					t.Fatalf("JSON path %s is empty: %#v", path, got)
				}
			}
		})
	}
}

func TestP78IntegrationProofRecords(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "../.."))
	proofsPath := filepath.Join(repoRoot, "testdata", "sidecar", "integration-proofs.json")
	data, err := os.ReadFile(proofsPath)
	if err != nil {
		t.Fatalf("read integration proofs: %v", err)
	}
	var proofs p78IntegrationProofSet
	if err := json.Unmarshal(data, &proofs); err != nil {
		t.Fatalf("decode integration proofs: %v", err)
	}
	if proofs.SchemaVersion != "idiom.sidecar_integration_proofs.v1" {
		t.Fatalf("unexpected proof schema version %q", proofs.SchemaVersion)
	}
	if proofs.SharedContract.SidecarSchemaVersion != "idiom.tool.v1" || proofs.SharedContract.Profile != "discovery" {
		t.Fatalf("proofs are not tied to the shared discovery sidecar contract: %#v", proofs.SharedContract)
	}
	for _, required := range []string{
		"sidecar.schema_version",
		"sidecar.profile",
		"sidecar.contract",
		"sidecar.freshness",
		"sidecar.audit_event_ids",
		"sidecar.structured_output",
		"sidecar.error",
	} {
		if !stringSliceContains(proofs.SharedContract.RequiredEnvelopeFields, required) {
			t.Fatalf("shared contract missing required envelope field %q", required)
		}
	}
	for _, required := range proofs.SharedContract.RequiredReadinessFields {
		if strings.TrimSpace(required) == "" {
			t.Fatalf("blank readiness field in shared contract: %#v", proofs.SharedContract.RequiredReadinessFields)
		}
	}

	transcript, err := readSidecarSmokeTranscript(repoRoot)
	if err != nil {
		t.Fatalf("read smoke transcript: %v", err)
	}
	transcriptSteps := map[string]sidecarSmokeStep{}
	for _, step := range transcript.Steps {
		transcriptSteps[step.ID] = step
	}

	docData, err := os.ReadFile(filepath.Join(repoRoot, "docs", "sidecar-integration-proofs.md"))
	if err != nil {
		t.Fatalf("read integration proof docs: %v", err)
	}
	doc := string(docData)

	seenPaths := map[string]bool{}
	for _, proof := range proofs.Proofs {
		if proof.ID == "" || proof.Path == "" {
			t.Fatalf("proof missing id/path: %#v", proof)
		}
		seenPaths[proof.Path] = true
		if !strings.Contains(doc, proof.Path) {
			t.Fatalf("integration proof docs do not mention path %q", proof.Path)
		}
		if !proof.Consumes.Profile || !proof.Consumes.Schema || !proof.Consumes.Freshness || !proof.Consumes.Audit || !proof.Consumes.Contract {
			t.Fatalf("proof %s does not consume the complete shared sidecar contract: %#v", proof.ID, proof.Consumes)
		}
		for _, required := range proofs.SharedContract.RequiredReadinessFields {
			if !proof.Readiness[required] {
				t.Fatalf("proof %s readiness %q is not true: %#v", proof.ID, required, proof.Readiness)
			}
		}
		for field, leaked := range proof.Privacy {
			if leaked {
				t.Fatalf("proof %s privacy field %s reports leaked content", proof.ID, field)
			}
		}
		proofPayload, err := json.Marshal(proof)
		if err != nil {
			t.Fatalf("marshal proof %s for privacy scan: %v", proof.ID, err)
		}
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
			if strings.Contains(string(proofPayload), pattern) {
				t.Fatalf("integration proof fixture contains forbidden privacy pattern %q", pattern)
			}
		}
		switch proof.Path {
		case "shell_ci_script":
			assertP78ExecutableProof(t, proof, transcriptSteps)
			if proof.ScriptPath == "" {
				t.Fatalf("shell/CI proof must reference a script path")
			}
			scriptData, err := os.ReadFile(filepath.Join(repoRoot, proof.ScriptPath))
			if err != nil {
				t.Fatalf("read shell/CI proof script: %v", err)
			}
			script := string(scriptData)
			for _, required := range []string{"idiom.tool.v1", "--profile discovery", "sidecar.audit_event_ids", "sidecar.freshness.policy"} {
				if !strings.Contains(script, required) {
					t.Fatalf("shell/CI proof script missing %q", required)
				}
			}
		case "external_coding_agent_workflow":
			assertP78ExecutableProof(t, proof, transcriptSteps)
			if proof.ScriptPath != "" {
				t.Fatalf("external coding-agent proof should be a generic workflow recipe, not a script-specific path")
			}
		case "mcp_stdio":
			if proof.Status != "test_validated" {
				t.Fatalf("MCP proof must be test validated, got %q", proof.Status)
			}
			if !proof.Readiness["protocol_implemented"] || proof.Readiness["phase8_deferred"] || proof.Readiness["reviewed_placeholder"] || !proof.Readiness["executable_evidence"] {
				t.Fatalf("MCP proof readiness is not executable: %#v", proof.Readiness)
			}
			if proof.Phase8WorkPackage != "P8.6" {
				t.Fatalf("MCP proof must tie back to P8.6, got %q", proof.Phase8WorkPackage)
			}
			if proof.ReplayFixture != "testdata/sidecar/mcp-stdio-integration-proof.json" ||
				!strings.Contains(proof.ReplayTest, "TestP86MCPStdioIntegrationProofReplay") ||
				!proof.ProductEvidenceExportParity {
				t.Fatalf("MCP proof missing replay/product-evidence linkage: %#v", proof)
			}
			for _, field := range p98RequiredMCPParityReadinessFields {
				if !proof.Readiness[field] {
					t.Fatalf("MCP proof missing required P9.8 parity field %q: %#v", field, proof.Readiness)
				}
			}
			if !proof.Readiness["roots_resources_present"] {
				t.Fatalf("MCP proof missing required P8.6 readiness fields: %#v", proof.Readiness)
			}
			if proof.MCPContract["transport"] != "mcp_stdio" || proof.MCPContract["sidecar_schema_version"] != "idiom.tool.v1" || proof.MCPContract["profile"] != "discovery" {
				t.Fatalf("MCP contract does not preserve sidecar shape: %#v", proof.MCPContract)
			}
			for _, class := range []string{"finance", "healthcare", "public-sector", "enterprise-saas", "tooling-vendor", "local-model", "open-source"} {
				if !stringSliceContains(proof.ClientClassifications, class) {
					t.Fatalf("MCP proof missing classification %q: %#v", class, proof.ClientClassifications)
				}
			}
			replayData, err := os.ReadFile(filepath.Join(repoRoot, proof.ReplayFixture))
			if err != nil {
				t.Fatalf("read MCP replay fixture: %v", err)
			}
			var replay p86MCPIntegrationProof
			if err := json.Unmarshal(replayData, &replay); err != nil {
				t.Fatalf("decode MCP replay fixture: %v", err)
			}
			if replay.LinkedP78ProofID != proof.ID {
				t.Fatalf("MCP replay fixture links to %q, want %q", replay.LinkedP78ProofID, proof.ID)
			}
			if !strings.Contains(strings.Join(proof.Limitations, " "), "metadata-only") {
				t.Fatalf("MCP proof must preserve resource-body limitation: %#v", proof.Limitations)
			}
		default:
			t.Fatalf("unexpected proof path %q", proof.Path)
		}
	}
	for _, requiredPath := range []string{"shell_ci_script", "external_coding_agent_workflow", "mcp_stdio"} {
		if !seenPaths[requiredPath] {
			t.Fatalf("missing P7.8 proof path %q", requiredPath)
		}
	}
}

func TestSidecarRecipesCoverCallerClassesAndCompatibility(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "../.."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "docs", "sidecar-recipes.md"))
	if err != nil {
		t.Fatalf("read sidecar recipes: %v", err)
	}
	doc := string(data)
	transcript, err := readSidecarSmokeTranscript(repoRoot)
	if err != nil {
		t.Fatalf("read smoke transcript: %v", err)
	}
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
		if !strings.Contains(doc, required) {
			t.Fatalf("sidecar recipes missing %q", required)
		}
	}
	documentedSteps := map[string]bool{}
	normalizedDoc := normalizedShellSnippet(doc)
	for _, step := range transcript.Steps {
		if !step.DocumentedRecipe {
			continue
		}
		documentedSteps[step.ID] = true
		if !strings.Contains(normalizedDoc, normalizedShellSnippet(step.Command)) {
			t.Fatalf("sidecar recipes do not document executable step %q command %q", step.ID, step.Command)
		}
	}
	for _, requiredStep := range []string{
		"external-harness-context-query",
		"local-private-context-pack",
	} {
		if !documentedSteps[requiredStep] {
			t.Fatalf("smoke transcript missing documented executable recipe step %q", requiredStep)
		}
	}
}

func assertP78ExecutableProof(t *testing.T, proof p78IntegrationProof, transcriptSteps map[string]sidecarSmokeStep) {
	t.Helper()
	if proof.Status != "test_validated" {
		t.Fatalf("proof %s should be test-validated, got %q", proof.ID, proof.Status)
	}
	if !proof.Readiness["executable_evidence"] || proof.Readiness["reviewed_placeholder"] || !proof.Readiness["protocol_implemented"] || proof.Readiness["phase8_deferred"] {
		t.Fatalf("proof %s readiness is not executable: %#v", proof.ID, proof.Readiness)
	}
	if len(proof.TranscriptStepIDs) == 0 {
		t.Fatalf("proof %s must reference at least one transcript step", proof.ID)
	}
	for _, stepID := range proof.SetupStepIDs {
		if _, ok := transcriptSteps[stepID]; !ok {
			t.Fatalf("proof %s references missing setup transcript step %q", proof.ID, stepID)
		}
	}
	for _, stepID := range proof.TranscriptStepIDs {
		step, ok := transcriptSteps[stepID]
		if !ok {
			t.Fatalf("proof %s references missing proof transcript step %q", proof.ID, stepID)
		}
		if !step.JSON {
			continue
		}
		for _, required := range []string{"sidecar.schema_version", "sidecar.profile", "sidecar.contract.id"} {
			if _, ok := step.ExpectJSONString[required]; !ok {
				t.Fatalf("transcript step %s does not assert %s", stepID, required)
			}
		}
		if _, ok := step.ExpectJSONString["sidecar.freshness.policy"]; !ok && !stringSliceContains(step.ExpectJSONNull, "sidecar.freshness") {
			t.Fatalf("transcript step %s does not assert freshness presence or explicit null", stepID)
		}
		if !stringSliceContains(step.ExpectJSONPresent, "sidecar.audit_event_ids") {
			t.Fatalf("transcript step %s does not assert sidecar audit field presence", stepID)
		}
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

func writeSidecarRecipeWorkspace(t *testing.T, root string) {
	t.Helper()
	writeSidecarRecipeFile(t, filepath.Join(root, "go.mod"), "module example.com/sidecar\n\ngo 1.22\n")
	writeSidecarRecipeFile(t, filepath.Join(root, "cmd", "tool", "main.go"), `package main

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
	writeSidecarRecipeFile(t, filepath.Join(root, "auth", "retry.go"), `package auth

func Authenticate() string {
	return retryRequest()
}

func retryRequest() string {
	return "sidecar-auth-retry-sentinel"
}
`)
}

func writeSidecarRecipeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
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
