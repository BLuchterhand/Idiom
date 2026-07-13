package sidecarvalidation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/runtime/localops"
)

type p710ClientDemoTranscriptSet struct {
	SchemaVersion string                     `json:"schema_version"`
	Name          string                     `json:"name"`
	Description   string                     `json:"description"`
	Shared        p710SharedDemoContract     `json:"shared_contract"`
	Transcripts   []p710ClientDemoTranscript `json:"transcripts"`
}

type p710SharedDemoContract struct {
	SidecarSchemaVersion    string   `json:"sidecar_schema_version"`
	DecisionBoundary        string   `json:"decision_boundary"`
	GenericPrimitives       []string `json:"generic_primitives"`
	SharedSecurityContracts []string `json:"shared_security_contracts"`
	ForbiddenEvidenceFields []string `json:"forbidden_evidence_fields"`
}

type p710ClientDemoTranscript struct {
	ID                      string                        `json:"id"`
	Classification          autonomy.ClientClassification `json:"classification"`
	ClassificationLabel     string                        `json:"classification_label"`
	Stance                  string                        `json:"stance"`
	P76CommandID            string                        `json:"p7_6_command_id"`
	ValueClaim              string                        `json:"value_claim"`
	SellingPoint            string                        `json:"selling_point"`
	GenericPrimitives       []string                      `json:"generic_primitives"`
	SharedSecurityContracts []string                      `json:"shared_security_contracts"`
	EvidenceFields          []string                      `json:"evidence_fields"`
	MVPLimitation           string                        `json:"mvp_limitation"`
	FinalStateExpansion     string                        `json:"final_state_expansion"`
	PrivacyPosture          string                        `json:"privacy_posture"`
	PassNoGoCriteria        p710PassNoGoCriteria          `json:"pass_no_go_criteria"`
	ReleaseClaimImpact      string                        `json:"release_claim_impact"`
	Commands                []p710ClientDemoCommand       `json:"commands"`
}

type p710PassNoGoCriteria struct {
	Pass []string `json:"pass"`
	NoGo []string `json:"no_go"`
}

type p710ClientDemoCommand struct {
	ID                  string                  `json:"id"`
	Command             string                  `json:"command"`
	Argv                []string                `json:"argv"`
	ExitCode            int                     `json:"exit_code"`
	JSON                bool                    `json:"json"`
	ExpectedOutputShape p710ExpectedOutputShape `json:"expected_output_shape"`
}

type p710ExpectedOutputShape struct {
	StableFields   []string          `json:"stable_fields"`
	JSONString     map[string]string `json:"json_string"`
	JSONPresent    []string          `json:"json_present"`
	JSONNonEmpty   []string          `json:"json_non_empty"`
	JSONNull       []string          `json:"json_null"`
	StdoutIncludes []string          `json:"stdout_includes"`
	StdoutExcludes []string          `json:"stdout_excludes"`
}

var _ = ginkgo.It("p710 client demo transcripts", func() {
	t := ginkgo.GinkgoT()
	t.Setenv("IDIOM_TELEMETRY", "0")

	repoRoot := sidecarValidationRepoRoot()
	transcriptPath := filepath.Join(repoRoot, "testdata", "sidecar", "client-demo-transcripts.json")

	data, err := os.ReadFile(transcriptPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read P7.10 transcripts: %v", err))

	assertP710NoForbiddenFixtureEvidence(string(data))

	transcriptSet, err := decodeP710ClientDemoTranscriptSet(data)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode P7.10 transcripts: %v", err))

	gomega.Expect(validateP710TranscriptSetIdentityAndClassifications(transcriptSet)).To(gomega.Succeed(), "validate P7.10 transcript set")

	assertP710SharedContract(transcriptSet.Shared)

	examples := p76ClassificationExamples()
	assertP76CoversDefaultClientClassifications(examples)

	p76ByClassification := map[autonomy.ClientClassification]p76ClassificationExample{}
	for _, example := range examples {
		p76ByClassification[example.classification] = example
	}

	docData, err := os.ReadFile(filepath.Join(repoRoot, "docs", "client-demo-transcripts.md"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read P7.10 docs: %v", err))

	doc := string(docData)
	gomega.Expect(doc).To(gomega.ContainSubstring("testdata/sidecar/client-demo-transcripts.json"), fmt.Sprintf("P7.10 docs do not point to executable transcript validation"))
	gomega.Expect(doc).To(gomega.ContainSubstring("-ginkgo.focus='p710 client demo transcripts'"), fmt.Sprintf("P7.10 docs do not point to the current transcript spec"))
	gomega.Expect(doc).To(gomega.ContainSubstring("-ginkgo.fail-on-empty"), fmt.Sprintf("P7.10 docs allow a stale transcript selector to pass without executing a spec"))

	for _, transcript := range transcriptSet.Transcripts {
		ginkgo.By(string(transcript.Classification))
		func() {
			assertP710TranscriptShape(transcript, doc)

			example, ok := p76ByClassification[transcript.Classification]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("transcript uses classification without P7.6 fixture coverage: %q", transcript.Classification))

			gomega.Expect(transcript.P76CommandID).To(gomega.BeEquivalentTo(example.commandID), fmt.Sprintf("transcript %s command coverage = %q, want P7.6 command %q", transcript.ID, transcript.P76CommandID, example.commandID))

			root := t.TempDir()
			writeP76ClassificationWorkspace(root)

			configPath := writeMainConfigRoot(root)
			if transcript.Classification == autonomy.ClientLocalModel {
				stageDoctorConfigWithBudget(root, 4096, 512)

				configPath = filepath.Join(root, ".idiom", "config.json")
			}

			for _, command := range transcript.Commands {
				var stdout, stderr bytes.Buffer

				code := runMain(p710DemoArgs(command.Argv, configPath), &stdout, &stderr)

				gomega.Expect(code).To(gomega.BeEquivalentTo(command.ExitCode), fmt.Sprintf("%s exit code = %d, want %d\nstdout=%s\nstderr=%s", command.ID, code, command.ExitCode, stdout.String(), stderr.String()))

				assertP710NoForbiddenExecutedEvidence(command.ID, "stdout", stdout.String())
				assertP710NoForbiddenExecutedEvidence(command.ID, "stderr", stderr.String())

				gomega.Expect(stderr.Len()).To(gomega.BeEquivalentTo(0), fmt.Sprintf("%s should keep diagnostics in structured stdout, stderr=%s", command.ID, stderr.String()))

				assertP710StdoutExpectations(command, stdout.String())

				if command.JSON {
					assertP710ExpectedJSONShape(command, stdout.Bytes(), transcript.Classification)
				}
			}
		}()
	}
})
var _ = ginkgo.It("p717 client demo transcript schema strictness", func() {
	repoRoot := sidecarValidationRepoRoot()

	data, err := os.ReadFile(filepath.Join(repoRoot, "testdata", "sidecar", "client-demo-transcripts.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read P7.10 transcripts: %v", err))

	transcriptSet, err := decodeP710ClientDemoTranscriptSet(data)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("decode valid P7.10 transcripts: %v", err))

	gomega.Expect(validateP710TranscriptSetIdentityAndClassifications(transcriptSet)).To(gomega.Succeed(), "validate valid transcript set")

	withUnknownField := bytes.Replace(data, []byte(`"description":`), []byte(`"unexpected_field": true, "description":`), 1)
	{
		_, err := decodeP710ClientDemoTranscriptSet(withUnknownField)
		gomega.Expect(err).NotTo(gomega.BeNil(), fmt.Sprintf("P7.17 strict decoder accepted an unknown top-level field"))
	}

	wrongIdentity := transcriptSet

	wrongIdentity.SchemaVersion = "idiom.client_demo_transcripts.v0"

	gomega.Expect(validateP710TranscriptSetIdentityAndClassifications(wrongIdentity)).NotTo(gomega.Succeed(), fmt.Sprintf("P7.17 validation accepted an unexpected schema_version"))

	wrongIdentity = transcriptSet
	wrongIdentity.Name = "renamed-demo-transcripts"

	gomega.Expect(validateP710TranscriptSetIdentityAndClassifications(wrongIdentity)).NotTo(gomega.Succeed(), fmt.Sprintf("P7.17 validation accepted an unexpected name"))

	duplicate := transcriptSet

	duplicate.Transcripts = append(append([]p710ClientDemoTranscript(nil), transcriptSet.Transcripts...), transcriptSet.Transcripts[0])

	gomega.Expect(validateP710TranscriptSetIdentityAndClassifications(duplicate)).NotTo(gomega.Succeed(), fmt.Sprintf("P7.17 validation accepted a duplicate classification"))
})

func validateP710TranscriptSetIdentityAndClassifications(transcriptSet p710ClientDemoTranscriptSet) error {
	if transcriptSet.SchemaVersion != "idiom.client_demo_transcripts.v1" || transcriptSet.Name != "p7.10-client-demo-transcripts" {
		return fmt.Errorf("unexpected transcript identity: schema=%q name=%q", transcriptSet.SchemaVersion, transcriptSet.Name)
	}

	for _, classification := range autonomy.DefaultClientClassifications() {
		if !p710TranscriptSetHasClassification(transcriptSet.Transcripts, classification) {
			return fmt.Errorf("missing transcript for classification %q", classification)
		}
	}

	seen := map[autonomy.ClientClassification]bool{}
	for _, transcript := range transcriptSet.Transcripts {
		if seen[transcript.Classification] {
			return fmt.Errorf("duplicate transcript classification %q", transcript.Classification)
		}

		seen[transcript.Classification] = true
	}

	if len(seen) != len(autonomy.DefaultClientClassifications()) {
		return fmt.Errorf("transcripts contain non-default classifications: %#v", seen)
	}

	return nil
}

func p710TranscriptSetHasClassification(transcripts []p710ClientDemoTranscript, classification autonomy.ClientClassification) bool {
	for _, transcript := range transcripts {
		if transcript.Classification == classification {
			return true
		}
	}

	return false
}

func decodeP710ClientDemoTranscriptSet(data []byte) (p710ClientDemoTranscriptSet, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var transcriptSet p710ClientDemoTranscriptSet

	if err := dec.Decode(&transcriptSet); err != nil {
		return p710ClientDemoTranscriptSet{}, err
	}

	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return p710ClientDemoTranscriptSet{}, fmt.Errorf("unexpected trailing JSON after transcript set")
	}

	return transcriptSet, nil
}

func assertP710SharedContract(shared p710SharedDemoContract) {
	ginkgo.GinkgoHelper()

	gomega.Expect(shared.SidecarSchemaVersion).To(gomega.BeEquivalentTo("idiom.tool.v1"), fmt.Sprintf("P7.10 demos are not tied to sidecar v1: %#v", shared))

	for _, required := range []string{
		"model owns strategy",
		"harness owns",
	} {
		gomega.Expect(strings.ToLower(shared.DecisionBoundary)).To(gomega.ContainSubstring(required), fmt.Sprintf("shared decision boundary missing %q: %q", required, shared.DecisionBoundary))
	}

	for _, required := range []string{
		"tool_sidecar_envelope",
		"default_client_classification_metadata",
		"discovery_profile_policy",
		"context_pack",
	} {
		gomega.Expect(stringSliceContains(shared.GenericPrimitives, required)).To(gomega.BeTrue(), fmt.Sprintf("shared primitives missing %q: %#v", required, shared.GenericPrimitives))
	}

	for _, required := range []string{
		"sidecar.schema_version",
		"sidecar.contract.id",
		"sidecar.audit_event_ids",
		"sidecar.error",
	} {
		gomega.Expect(stringSliceContains(shared.SharedSecurityContracts, required)).To(gomega.BeTrue(), fmt.Sprintf("shared security contract missing %q: %#v", required, shared.SharedSecurityContracts))
	}

	for _, forbidden := range []string{
		"raw_prompt",
		"raw_file_body",
		"secret_value",
		"private_repo_url",
		"unredacted_log_excerpt",
		"customer_identifier",
	} {
		gomega.Expect(stringSliceContains(shared.ForbiddenEvidenceFields, forbidden)).To(gomega.BeTrue(), fmt.Sprintf("shared forbidden evidence fields missing %q: %#v", forbidden, shared.ForbiddenEvidenceFields))
	}
}

func assertP710TranscriptShape(transcript p710ClientDemoTranscript, doc string) {
	ginkgo.GinkgoHelper()

	gomega.Expect(transcript.ID).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("transcript missing identity fields: %#v", transcript))
	gomega.Expect(transcript.ClassificationLabel).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("transcript missing identity fields: %#v", transcript))
	gomega.Expect(transcript.Stance).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("transcript missing identity fields: %#v", transcript))
	gomega.Expect(transcript.P76CommandID).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("transcript missing identity fields: %#v", transcript))

	for _, requiredText := range []string{
		transcript.ID,
		string(transcript.Classification),
		transcript.P76CommandID,
	} {
		gomega.Expect(doc).To(gomega.ContainSubstring(requiredText), fmt.Sprintf("P7.10 docs missing %q for transcript %s", requiredText, transcript.ID))
	}

	for fieldName, value := range map[string]string{
		"value_claim":           transcript.ValueClaim,
		"selling_point":         transcript.SellingPoint,
		"mvp_limitation":        transcript.MVPLimitation,
		"final_state_expansion": transcript.FinalStateExpansion,
		"privacy_posture":       transcript.PrivacyPosture,
		"release_claim_impact":  transcript.ReleaseClaimImpact,
	} {
		gomega.Expect(strings.TrimSpace(value)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("transcript %s missing %s", transcript.ID, fieldName))
	}

	for _, required := range []string{"no raw prompt", "no raw file", "secret", "private"} {
		gomega.Expect(strings.ToLower(transcript.PrivacyPosture)).To(gomega.ContainSubstring(required), fmt.Sprintf("transcript %s privacy posture missing %q: %q", transcript.ID, required, transcript.PrivacyPosture))
	}

	gomega.Expect(transcript.GenericPrimitives).NotTo(gomega.HaveLen(0), fmt.Sprintf("transcript %s must cite primitives, security contracts, and evidence fields", transcript.ID))
	gomega.Expect(transcript.SharedSecurityContracts).NotTo(gomega.HaveLen(0), fmt.Sprintf("transcript %s must cite primitives, security contracts, and evidence fields", transcript.ID))
	gomega.Expect(transcript.EvidenceFields).NotTo(gomega.HaveLen(0), fmt.Sprintf("transcript %s must cite primitives, security contracts, and evidence fields", transcript.ID))

	assertP710NonBlankList(transcript.ID, "generic_primitives", transcript.GenericPrimitives)
	assertP710NonBlankList(transcript.ID, "shared_security_contracts", transcript.SharedSecurityContracts)
	assertP710NonBlankList(transcript.ID, "evidence_fields", transcript.EvidenceFields)

	gomega.Expect(transcript.PassNoGoCriteria.Pass).NotTo(gomega.HaveLen(0), fmt.Sprintf("transcript %s missing pass/no-go criteria: %#v", transcript.ID, transcript.PassNoGoCriteria))
	gomega.Expect(transcript.PassNoGoCriteria.NoGo).NotTo(gomega.HaveLen(0), fmt.Sprintf("transcript %s missing pass/no-go criteria: %#v", transcript.ID, transcript.PassNoGoCriteria))

	assertP710NonBlankList(transcript.ID, "pass_no_go_criteria.pass", transcript.PassNoGoCriteria.Pass)
	assertP710NonBlankList(transcript.ID, "pass_no_go_criteria.no_go", transcript.PassNoGoCriteria.NoGo)

	gomega.Expect(transcript.Commands).NotTo(gomega.BeEmpty(), fmt.Sprintf("transcript %s has no runnable commands", transcript.ID))

	for _, command := range transcript.Commands {
		gomega.Expect(command.ID).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("transcript %s has non-runnable command structure: %#v", transcript.ID, command))
		gomega.Expect(strings.HasPrefix(command.Command, "idiom ")).To(gomega.BeTrue(), fmt.Sprintf("transcript %s has non-runnable command structure: %#v", transcript.ID, command))
		gomega.Expect(command.Argv).NotTo(gomega.HaveLen(0), fmt.Sprintf("transcript %s has non-runnable command structure: %#v", transcript.ID, command))

		assertP710NonBlankList(transcript.ID, "commands."+command.ID+".argv", command.Argv)

		gomega.Expect(command.ExpectedOutputShape.StableFields).NotTo(gomega.BeEmpty(), fmt.Sprintf("transcript %s command %s does not declare stable expected fields", transcript.ID, command.ID))

		assertP710ExpectedOutputShapeNonBlank(transcript.ID, command.ID, command.ExpectedOutputShape)
	}
}

func assertP710NonBlankList(transcriptID string, fieldName string, values []string) {
	ginkgo.GinkgoHelper()

	for index, value := range values {
		gomega.Expect(strings.TrimSpace(value)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("transcript %s has blank %s[%d]", transcriptID, fieldName, index))
	}
}

func assertP710ExpectedOutputShapeNonBlank(transcriptID string, commandID string, shape p710ExpectedOutputShape) {
	ginkgo.GinkgoHelper()

	for _, list := range []struct {
		name   string
		values []string
	}{
		{name: "stable_fields", values: shape.StableFields},
		{name: "json_present", values: shape.JSONPresent},
		{name: "json_non_empty", values: shape.JSONNonEmpty},
		{name: "json_null", values: shape.JSONNull},
		{name: "stdout_includes", values: shape.StdoutIncludes},
		{name: "stdout_excludes", values: shape.StdoutExcludes},
	} {
		assertP710NonBlankList(transcriptID, "commands."+commandID+".expected_output_shape."+list.name, list.values)
	}

	for key, value := range shape.JSONString {
		gomega.Expect(strings.TrimSpace(key)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("transcript %s command %s has blank expected_output_shape.json_string entry", transcriptID, commandID))
		gomega.Expect(strings.TrimSpace(value)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("transcript %s command %s has blank expected_output_shape.json_string entry", transcriptID, commandID))
	}
}

func assertP710StdoutExpectations(command p710ClientDemoCommand, stdout string) {
	ginkgo.GinkgoHelper()

	for _, want := range command.ExpectedOutputShape.StdoutIncludes {
		gomega.Expect(stdout).To(gomega.ContainSubstring(want), fmt.Sprintf("%s stdout missing %q:\n%s", command.ID, want, stdout))
	}

	for _, forbidden := range command.ExpectedOutputShape.StdoutExcludes {
		gomega.Expect(stdout).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("%s stdout unexpectedly contains %q:\n%s", command.ID, forbidden, stdout))
	}
}

func assertP710ExpectedJSONShape(command p710ClientDemoCommand, stdout []byte, classification autonomy.ClientClassification) {
	ginkgo.GinkgoHelper()

	var payload any

	gomega.Expect(json.Unmarshal(stdout, &payload)).To(gomega.Succeed(), "%s did not emit JSON:\n%s", command.ID, string(stdout))

	for _, path := range command.ExpectedOutputShape.StableFields {
		{
			_, ok := jsonValueAtDottedPath(payload, path)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("%s missing stable expected JSON field %s in %s", command.ID, path, string(stdout)))
		}
	}

	for path, want := range command.ExpectedOutputShape.JSONString {
		got, ok := jsonValueAtDottedPath(payload, path)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("%s missing JSON path %s in %s", command.ID, path, string(stdout)))

		gomega.Expect(fmt.Sprint(got)).To(gomega.BeEquivalentTo(want), fmt.Sprintf("%s JSON path %s = %q, want %q", command.ID, path, fmt.Sprint(got), want))
	}

	for _, path := range command.ExpectedOutputShape.JSONPresent {
		{
			_, ok := jsonValueAtDottedPath(payload, path)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("%s missing JSON path %s in %s", command.ID, path, string(stdout)))
		}
	}

	for _, path := range command.ExpectedOutputShape.JSONNonEmpty {
		got, ok := jsonValueAtDottedPath(payload, path)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("%s missing JSON path %s in %s", command.ID, path, string(stdout)))

		gomega.Expect(jsonValueEmpty(got)).To(gomega.BeFalse(), fmt.Sprintf("%s JSON path %s is empty: %#v", command.ID, path, got))
	}

	for _, path := range command.ExpectedOutputShape.JSONNull {
		got, ok := jsonValueAtDottedPath(payload, path)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("%s missing JSON path %s in %s", command.ID, path, string(stdout)))

		gomega.Expect(got).To(gomega.BeNil(), fmt.Sprintf("%s JSON path %s = %#v, want null", command.ID, path, got))
	}

	if strings.HasPrefix(command.ID, "tooling-vendor") {
		assertP710SchemaExportCoversClassifications(payload, classification)

		return
	}

	if strings.HasPrefix(command.ID, "local-model") {
		assertP710LocalOpsPrivacyShape(payload)

		return
	}

	if strings.HasPrefix(command.ID, "open-source") {
		assertP710OpenSourceQuickstartShape(payload)

		return
	}

	if values, ok := p76StringSliceAtPath(payload.(map[string]any), "sidecar.contract.metadata.client_classifications"); ok {
		for _, want := range autonomy.DefaultClientClassifications() {
			gomega.Expect(p76ContainsString(values, string(want))).To(gomega.BeTrue(), fmt.Sprintf("%s sidecar metadata missing classification %q: %#v", command.ID, want, values))
		}
	}
}

func assertP710OpenSourceQuickstartShape(payload any) {
	ginkgo.GinkgoHelper()

	for _, path := range []string{
		"quickstart_smoke.first_useful_packet.packet_id",
		"quickstart_smoke.first_useful_packet.index_id",
		"quickstart_smoke.first_useful_packet.freshness_state",
		"quickstart_smoke.first_useful_packet.candidate_file_count",
		"quickstart_smoke.first_useful_packet.candidate_symbol_count",
		"quickstart_smoke.first_useful_packet.candidate_test_count",
		"quickstart_smoke.freshness_check.state",
		"quickstart_smoke.suggested_verification.suggested_commands",
		"quickstart_smoke.privacy_redaction_status.forbidden_field_check",
	} {
		{
			_, ok := jsonValueAtDottedPath(payload, path)
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("open-source quickstart output missing %s: %#v", path, payload))
		}
	}

	for path, want := range map[string]string{
		"quickstart_smoke.client_classification":                              "open-source",
		"quickstart_smoke.status":                                             "pass",
		"quickstart_smoke.first_useful_packet.tool":                           "context_pack",
		"quickstart_smoke.first_useful_packet.task.raw_task_captured":         "false",
		"quickstart_smoke.privacy_redaction_status.private_path_captured":     "false",
		"quickstart_smoke.product_evidence_safe_output.forbidden_field_check": "pass",
	} {
		got, ok := jsonValueAtDottedPath(payload, path)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("open-source quickstart output missing %s: %#v", path, payload))

		gomega.Expect(fmt.Sprint(got)).To(gomega.BeEquivalentTo(want), fmt.Sprintf("open-source quickstart %s = %q, want %q", path, fmt.Sprint(got), want))
	}
}

func assertP710SchemaExportCoversClassifications(payload any, classification autonomy.ClientClassification) {
	ginkgo.GinkgoHelper()

	root, ok := payload.(map[string]any)
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("schema export payload is not an object: %#v", payload))

	values, ok := p76StringSliceAtPath(root, "metadata.client_classifications")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("schema export missing classification metadata: %#v", root))

	for _, want := range autonomy.DefaultClientClassifications() {
		gomega.Expect(p76ContainsString(values, string(want))).To(gomega.BeTrue(), fmt.Sprintf("schema export missing classification %q: %#v", want, values))
	}

	gomega.Expect(p76ContainsString(values, string(classification))).To(gomega.BeTrue(), fmt.Sprintf("schema export missing current classification %q: %#v", classification, values))

	signals, ok := p76StringSliceAtPath(root, "metadata.product_signals")
	gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("schema export missing integration product signal: %#v", root))
	gomega.Expect(p76ContainsString(signals, string(autonomy.ProductIntegrationProof))).To(gomega.BeTrue(), fmt.Sprintf("schema export missing integration product signal: %#v", root))
}

func assertP710LocalOpsPrivacyShape(payload any) {
	ginkgo.GinkgoHelper()

	for path, want := range map[string]string{
		"privacy.metadata_only":          "true",
		"privacy.raw_prompt_captured":    "false",
		"privacy.raw_file_body_captured": "false",
		"privacy.secret_capture_allowed": "false",
	} {
		got, ok := jsonValueAtDottedPath(payload, path)
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("local-ops output missing %s: %#v", path, payload))

		gomega.Expect(fmt.Sprint(got)).To(gomega.BeEquivalentTo(want), fmt.Sprintf("local-ops %s = %q, want %q", path, fmt.Sprint(got), want))
	}
}

func p710DemoArgs(args []string, configPath string) []string {
	copied := append([]string(nil), args...)
	for index, arg := range copied {
		if arg == "$IDIOM_CONFIG" {
			copied[index] = configPath
		}
	}

	return copied
}

func assertP710NoForbiddenFixtureEvidence(text string) {
	ginkgo.GinkgoHelper()

	for _, forbidden := range []string{
		"/Users/",
		"CustomerX",
		"private-repo",
		"sk-",
		"ghp_",
		"github_pat_",
		"xoxb-",
		"BEGIN PRIVATE KEY",
		"patient_name:",
		"account_number:",
		"unredacted_telemetry_path",
		"logs/telemetry/",
		"logs/conversations/",
	} {
		gomega.Expect(text).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("P7.10 transcript fixture contains forbidden privacy pattern %q", forbidden))
	}

	for _, phrase := range []string{
		"must call",
		"next tool",
		"choose the model",
		"compliance attestation",
		"HIPAA compliant",
		"FINRA compliant",
		"FedRAMP ready",
		"MCP protocol support",
	} {
		gomega.Expect(strings.ToLower(text)).NotTo(gomega.ContainSubstring(strings.ToLower(phrase)), fmt.Sprintf("P7.10 transcript fixture contains unsupported or strategy-prescriptive phrase %q", phrase))
	}
}

func assertP710NoForbiddenExecutedEvidence(commandID string, streamName string, text string) {
	ginkgo.GinkgoHelper()

	if strings.TrimSpace(text) == "" {
		return
	}

	{
		finding := localops.ForbiddenEvidenceLeak(text)
		gomega.Expect(finding).To(gomega.BeEquivalentTo(""), fmt.Sprintf("%s %s leaked forbidden evidence class %q:\n%s", commandID, streamName, finding, text))
	}

	for _, forbidden := range []string{
		"/private/var",
		"/var/folders",
		"/tmp/",
		"/Users/",
		"\\Users\\",
		"CustomerX",
		"private-repo",
		"BEGIN PRIVATE KEY",
		"patient_name:",
		"account_number:",
		"unredacted_telemetry_path",
	} {
		gomega.Expect(text).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("%s %s contains forbidden privacy pattern %q:\n%s", commandID, streamName, forbidden, text))
	}

	{
		token := p710CredentialToken(text)
		gomega.Expect(token).To(gomega.BeEquivalentTo(""), fmt.Sprintf("%s %s contains credential-shaped token %q:\n%s", commandID, streamName, token, text))
	}

	for _, forbidden := range []string{
		`"raw_prompt_captured": true`,
		`"raw_file_body_captured": true`,
		`"secret_capture_allowed": true`,
		`"secret_captured": true`,
		`"private_path_captured": true`,
		`"private_repo_url_captured": true`,
		`"unredacted_log_captured": true`,
		`"hosted_telemetry_required": true`,
		`"raw_task_captured": true`,
	} {
		gomega.Expect(text).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("%s %s reports forbidden capture flag %q:\n%s", commandID, streamName, forbidden, text))
	}

	for _, marker := range []string{
		"/logs/telemetry/",
		"/logs/conversations/",
		"\\logs\\telemetry\\",
		"\\logs\\conversations\\",
	} {
		gomega.Expect(text).NotTo(gomega.ContainSubstring(marker), fmt.Sprintf("%s %s contains unredacted telemetry/log path marker %q:\n%s", commandID, streamName, marker, text))
	}
}

func p710CredentialToken(text string) string {
	for _, field := range strings.Fields(text) {
		token := strings.Trim(field, " \t\r\n.,:;()[]{}\"'`")
		lower := strings.ToLower(token)

		for _, prefix := range []string{"sk-", "sk_", "ghp_", "github_pat_", "xoxb-"} {
			if strings.HasPrefix(lower, prefix) && len(token) > len(prefix) {
				return token
			}
		}
	}

	return ""
}
