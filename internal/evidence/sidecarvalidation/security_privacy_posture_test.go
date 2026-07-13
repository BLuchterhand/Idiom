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
	"idiom/internal/capabilities/workspace"
	"idiom/internal/contracts/autonomy"
	"idiom/internal/runtime/localops"
)

type p79PostureMatrix struct {
	SchemaVersion                string                       `json:"schema_version"`
	ArtifactID                   string                       `json:"artifact_id"`
	Scope                        p79PostureScope              `json:"scope"`
	RequiredControls             []p79PostureControl          `json:"required_controls"`
	ClientClassificationCoverage []p79ClientClassificationRow `json:"client_classification_coverage"`
}

type p79PostureScope struct {
	MVPStance        string `json:"mvp_stance"`
	DecisionBoundary string `json:"decision_boundary"`
}

type p79PostureControl struct {
	ID             string               `json:"id"`
	Claim          string               `json:"claim"`
	MVPLimitations string               `json:"mvp_limitations"`
	Evidence       []p79PostureEvidence `json:"evidence"`
}

type p79PostureEvidence struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Path    string `json:"path"`
	CheckID string `json:"check_id"`
}

type p79ClientClassificationRow struct {
	Classification    autonomy.ClientClassification `json:"classification"`
	Stance            string                        `json:"stance"`
	P76CommandID      string                        `json:"p7_6_command_id"`
	CoveredControlIDs []string                      `json:"covered_control_ids"`
	Limitation        string                        `json:"limitation"`
}

var _ = ginkgo.It("p79 security privacy posture artifacts cover controls and classifications", func() {
	root := sidecarValidationRepoRoot()
	matrix := readP79PostureMatrix(filepath.Join(root, "docs", "security-privacy-posture.matrix.json"))
	markdown := readP79PostureMarkdown(filepath.Join(root, "docs", "security-privacy-posture.md"))

	gomega.Expect(matrix.SchemaVersion).To(gomega.BeEquivalentTo("idiom.security_privacy_posture.v1"), fmt.Sprintf("unexpected posture matrix identity: %#v", matrix))
	gomega.Expect(matrix.ArtifactID).To(gomega.BeEquivalentTo("p7.9-security-privacy-posture"), fmt.Sprintf("unexpected posture matrix identity: %#v", matrix))

	for _, field := range []string{matrix.Scope.MVPStance, matrix.Scope.DecisionBoundary} {
		gomega.Expect(strings.TrimSpace(field)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("posture matrix scope must state stance and boundary: %#v", matrix.Scope))
	}

	gomega.Expect(markdown).To(gomega.ContainSubstring("P7.9"), fmt.Sprintf("posture markdown missing required review language:\n%s", markdown))
	gomega.Expect(markdown).To(gomega.ContainSubstring("security-privacy-posture.matrix.json"), fmt.Sprintf("posture markdown missing required review language:\n%s", markdown))
	gomega.Expect(markdown).To(gomega.ContainSubstring("not a SOC 2"), fmt.Sprintf("posture markdown missing required review language:\n%s", markdown))
	gomega.Expect(markdown).To(gomega.ContainSubstring("MVP limitation"), fmt.Sprintf("posture markdown missing required review language:\n%s", markdown))

	requiredControlIDs := []string{
		"protected_path_denial",
		"redaction_no_raw_prompt_or_file_body",
		"retention_local_artifact_scope",
		"telemetry_minimization_no_hosted_default",
		"side_effect_policy_read_only_discovery",
	}
	controls := map[string]p79PostureControl{}

	for _, control := range matrix.RequiredControls {
		controls[control.ID] = control
		gomega.Expect(strings.TrimSpace(control.Claim)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("control %q must carry claim and MVP limitation: %#v", control.ID, control))
		gomega.Expect(strings.TrimSpace(control.MVPLimitations)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("control %q must carry claim and MVP limitation: %#v", control.ID, control))

		gomega.Expect(control.Evidence).NotTo(gomega.BeEmpty(), fmt.Sprintf("control %q must carry executable evidence", control.ID))

		for _, evidence := range control.Evidence {
			gomega.Expect(evidence.Kind).To(gomega.BeEquivalentTo("go_test"), fmt.Sprintf("control %q has non-executable evidence row: %#v", control.ID, evidence))
			gomega.Expect(strings.HasPrefix(evidence.Command, "go test ")).To(gomega.BeTrue(), fmt.Sprintf("control %q has non-executable evidence row: %#v", control.ID, evidence))
			gomega.Expect(strings.TrimSpace(evidence.Path)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("control %q has non-executable evidence row: %#v", control.ID, evidence))
			gomega.Expect(strings.TrimSpace(evidence.CheckID)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("control %q has non-executable evidence row: %#v", control.ID, evidence))

			gomega.Expect(evidence.Path).NotTo(gomega.ContainSubstring("/Users/"), fmt.Sprintf("evidence path should be repo-relative and privacy-safe: %#v", evidence))
			gomega.Expect(filepath.IsAbs(evidence.Path)).To(gomega.BeFalse(), fmt.Sprintf("evidence path should be repo-relative and privacy-safe: %#v", evidence))
		}
	}

	for _, id := range requiredControlIDs {
		{
			_, ok := controls[id]
			gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("posture matrix missing required control %q", id))
		}

		if !strings.Contains(markdown, strings.ReplaceAll(id, "_", " ")) {
			gomega.Expect(markdown).To(gomega.ContainSubstring(p79ControlMarkdownName(id)), fmt.Sprintf("posture markdown does not mention control %q", id))
		}
	}

	rows := map[autonomy.ClientClassification]p79ClientClassificationRow{}
	for _, row := range matrix.ClientClassificationCoverage {
		rows[row.Classification] = row
		gomega.Expect(strings.TrimSpace(row.Stance)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("classification row must carry stance, limitation, P7.6 command, and controls: %#v", row))
		gomega.Expect(strings.TrimSpace(row.Limitation)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("classification row must carry stance, limitation, P7.6 command, and controls: %#v", row))
		gomega.Expect(strings.TrimSpace(row.P76CommandID)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("classification row must carry stance, limitation, P7.6 command, and controls: %#v", row))
		gomega.Expect(row.CoveredControlIDs).NotTo(gomega.HaveLen(0), fmt.Sprintf("classification row must carry stance, limitation, P7.6 command, and controls: %#v", row))

		for _, controlID := range row.CoveredControlIDs {
			{
				_, ok := controls[controlID]
				gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("classification %q references unknown control %q", row.Classification, controlID))
			}
		}
	}

	p76Examples := map[autonomy.ClientClassification]string{}
	for _, example := range p76ClassificationExamples() {
		p76Examples[example.classification] = example.commandID
	}

	for _, classification := range autonomy.DefaultClientClassifications() {
		row, ok := rows[classification]
		gomega.Expect(ok).To(gomega.BeTrue(), fmt.Sprintf("posture matrix missing classification %q", classification))

		gomega.Expect(row.P76CommandID).To(gomega.BeEquivalentTo(p76Examples[classification]), fmt.Sprintf("classification %q P7.6 command = %q, want %q", classification, row.P76CommandID, p76Examples[classification]))

		gomega.Expect(markdown).To(gomega.ContainSubstring("`"+string(classification)+"`"), fmt.Sprintf("posture markdown missing classification %q", classification))
	}

	p79AssertArtifactPrivacySafe(string(mustMarshalJSON(matrix)) + markdown)
})
var _ = ginkgo.It("p79 security privacy controls execute generic evidence", func() {
	t := ginkgo.GinkgoT()
	t.Setenv("IDIOM_TELEMETRY", "0")

	root := t.TempDir()
	protectedPath := filepath.Join(root, ".env")
	forbiddenPath := filepath.Join(root, ".git", "config")

	resolved, err := autonomy.ResolvePathRoles(autonomy.PathRoleResolverInput{
		WorkspaceRoot: root,
		PathRoles: []autonomy.PathRole{
			{Role: autonomy.PathRoleProtected, PathPattern: ".env", Reason: "secret-bearing fixture"},
			{Role: autonomy.PathRoleForbidden, PathPattern: ".git/**", Reason: "repository metadata fixture"},
		},
		Metadata: autonomy.DefaultContractMetadata("p7.9", "security_privacy_posture"),
		Source:   "p79-posture-test",
	}, protectedPath, forbiddenPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ResolvePathRoles: %v", err))

	p79AssertPathDenied(resolved, ".env", autonomy.InvariantProtectedPath)
	p79AssertPathDenied(resolved, ".git/config", autonomy.InvariantForbiddenPath)

	configPath := writeMainConfigRoot(root)
	privateOutputPath := filepath.Join(root, "regulated", "private-output.txt")
	rawPrompt := "RAW_PROMPT_BODY_SENTINEL account_number=123456789"
	rawFileBody := "RAW_FILE_BODY_SENTINEL patient_name=Sample"

	var stdout, stderr bytes.Buffer

	code := runMain([]string{
		"tool", "write_file",
		"-config", configPath,
		"--profile", "discovery",
		"--caller-id", "p79-audit",
		"--request-id", "p79-denial",
		"--input", fmt.Sprintf(`{"path":%q,"content":%q}`, privateOutputPath, rawPrompt+"\n"+rawFileBody),
	}, &stdout, &stderr)

	gomega.Expect(code).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected discovery-profile write denial, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String()))

	payload := decodeSidecarCompatJSON(stdout.Bytes())
	assertP76JSONPath(payload, "sidecar.profile", "discovery")
	assertP76JSONPath(payload, "sidecar.error.class", "profile_denied")

	for _, forbidden := range []string{privateOutputPath, root, rawPrompt, rawFileBody, "account_number=123456789", "patient_name=Sample"} {
		gomega.Expect(stdout.String()+stderr.String()).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("denial payload leaked forbidden value %q\nstdout=%s\nstderr=%s", forbidden, stdout.String(), stderr.String()))
	}

	{
		_, err := os.Stat(privateOutputPath)
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("denied discovery write should not create %s; stat err=%v", privateOutputPath, err))
	}

	for _, req := range []workspace.ToolRequest{
		{Tool: "write_file", Path: "regulated/private-output.txt", Content: "x"},
		{Tool: "shell", Command: "printf touched"},
		{Tool: "context_pack", Task: "artifact-writing discovery"},
		{Tool: "context_status", Refresh: true},
		{Tool: "context_query", Query: "callers of Run"},
	} {
		decision := workspace.ToolRequestAllowedForProfile(req, workspace.ToolProfileDiscovery)
		gomega.Expect(decision.Allowed).To(gomega.BeFalse(), fmt.Sprintf("discovery profile should deny request %#v", req))

		gomega.Expect(decision.Code).To(gomega.BeEquivalentTo("profile_denied"), fmt.Sprintf("discovery denial should carry structured reason: %#v", decision))
		gomega.Expect(strings.TrimSpace(decision.Reason)).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("discovery denial should carry structured reason: %#v", decision))
	}

	allowed := workspace.ToolRequestAllowedForProfile(workspace.ToolRequest{
		Tool:            "context_status",
		FreshnessPolicy: "report_only",
	}, workspace.ToolProfileDiscovery)
	gomega.Expect(allowed.Allowed).To(gomega.BeTrue(), fmt.Sprintf("report-only context_status should remain allowed for discovery profile: %#v", allowed))

	localRoot := t.TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(localRoot, "go.mod"), []byte("module example.com/p79\n\ngo 1.22\n"), 0o644)).To(gomega.Succeed(), "write local go.mod")

	stageDoctorConfigWithBudget(localRoot, 16384, 1024)

	report, err := localops.BuildBenchmarkReport(localops.BenchmarkOptions{
		ConfigPath:        filepath.Join(localRoot, ".idiom", "config.json"),
		Profile:           "sidecar",
		OutputPath:        filepath.Join(localRoot, "evidence", "p79-posture.json"),
		QuickstartSmoke:   true,
		QuickstartTask:    "P79 private repo task /home/example/secret token=abc123",
		QuickstartCleanup: "remove-local-state",
	}, localops.QuickstartDeps{})
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("build local-ops report: %v", err))

	gomega.Expect(report.Privacy.MetadataOnly).To(gomega.BeTrue(), fmt.Sprintf("local-ops privacy evidence must remain metadata-only: %#v", report.Privacy))
	gomega.Expect(report.Privacy.RawPromptCaptured).To(gomega.BeFalse(), fmt.Sprintf("local-ops privacy evidence must remain metadata-only: %#v", report.Privacy))
	gomega.Expect(report.Privacy.RawFileBodyCaptured).To(gomega.BeFalse(), fmt.Sprintf("local-ops privacy evidence must remain metadata-only: %#v", report.Privacy))
	gomega.Expect(report.Privacy.SecretCaptureAllowed).To(gomega.BeFalse(), fmt.Sprintf("local-ops privacy evidence must remain metadata-only: %#v", report.Privacy))

	gomega.Expect(strings.HasPrefix(report.Privacy.LocalArtifactDirectory, "path_id:")).To(gomega.BeTrue(), fmt.Sprintf("local artifact directory should be represented as a stable path id, got %#v", report.Privacy))

	gomega.Expect(report.Telemetry).To(gomega.BeNil(), fmt.Sprintf("posture report should not require telemetry when no recorder is supplied: %#v", report.Telemetry))

	gomega.Expect(report.Quickstart).NotTo(gomega.BeNil(), fmt.Sprint("expected quickstart privacy evidence"))

	smoke := report.Quickstart
	gomega.Expect(smoke.ProductEvidenceSafeOutput.HostedTelemetryDefault).To(gomega.BeFalse(), fmt.Sprintf("quickstart posture evidence should minimize telemetry and raw content: %#v", smoke))
	gomega.Expect(smoke.PrivacyRedactionStatus.HostedTelemetryRequired).To(gomega.BeFalse(), fmt.Sprintf("quickstart posture evidence should minimize telemetry and raw content: %#v", smoke))
	gomega.Expect(smoke.FirstUsefulPacket.Task.RawTaskCaptured).To(gomega.BeFalse(), fmt.Sprintf("quickstart posture evidence should minimize telemetry and raw content: %#v", smoke))
	gomega.Expect(smoke.PrivacyRedactionStatus.RawPromptCaptured).To(gomega.BeFalse(), fmt.Sprintf("quickstart posture evidence should minimize telemetry and raw content: %#v", smoke))
	gomega.Expect(smoke.PrivacyRedactionStatus.RawFileBodyCaptured).To(gomega.BeFalse(), fmt.Sprintf("quickstart posture evidence should minimize telemetry and raw content: %#v", smoke))
	gomega.Expect(smoke.PrivacyRedactionStatus.SecretCaptured).To(gomega.BeFalse(), fmt.Sprintf("quickstart posture evidence should minimize telemetry and raw content: %#v", smoke))
	gomega.Expect(smoke.PrivacyRedactionStatus.PrivatePathCaptured).To(gomega.BeFalse(), fmt.Sprintf("quickstart posture evidence should minimize telemetry and raw content: %#v", smoke))

	renderedReport := string(mustMarshalJSON(report))
	for _, forbidden := range []string{"/home/example/secret", "token=abc123", "P79 private repo task"} {
		gomega.Expect(renderedReport).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("local-ops posture evidence leaked %q in %s", forbidden, renderedReport))
	}

	{
		finding := localops.ForbiddenEvidenceLeak(renderedReport)
		gomega.Expect(finding).To(gomega.BeEquivalentTo(""), fmt.Sprintf("local-ops posture evidence failed forbidden-field scan: %q", finding))
	}
})

func readP79PostureMatrix(path string) p79PostureMatrix {
	ginkgo.GinkgoHelper()

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read posture matrix: %v", err))

	var matrix p79PostureMatrix

	gomega.Expect(json.Unmarshal(data, &matrix)).To(gomega.Succeed(), "decode posture matrix:\n%s", string(data))

	return matrix
}

func readP79PostureMarkdown(path string) string {
	ginkgo.GinkgoHelper()

	data, err := os.ReadFile(path)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("read posture markdown: %v", err))

	return string(data)
}

func p79ControlMarkdownName(id string) string {
	switch id {
	case "protected_path_denial":
		return "Protected-path denial"
	case "redaction_no_raw_prompt_or_file_body":
		return "Redaction and no raw prompt/file body"
	case "retention_local_artifact_scope":
		return "Retention and local artifact scope"
	case "telemetry_minimization_no_hosted_default":
		return "Telemetry minimization"
	case "side_effect_policy_read_only_discovery":
		return "Side-effect policy"
	default:
		return id
	}
}

func p79AssertPathDenied(set autonomy.PathRoleResolutionSet, path string, invariant autonomy.PathInvariantID) {
	ginkgo.GinkgoHelper()

	normalized := filepath.ToSlash(filepath.Clean(path))
	for _, resolution := range set.Resolutions {
		if resolution.Path != normalized && resolution.RelativePath != normalized {
			continue
		}

		gomega.Expect(resolution.Decision).To(gomega.BeEquivalentTo(autonomy.PolicyDeny), fmt.Sprintf("path %q should deny via %q, got %#v", path, invariant, resolution))
		gomega.Expect(resolution.EffectiveInvariantID).To(gomega.BeEquivalentTo(invariant), fmt.Sprintf("path %q should deny via %q, got %#v", path, invariant, resolution))

		return
	}
	ginkgo.Fail(fmt.Sprintf("missing resolved path %q in %#v", path, set.Resolutions), 1)
}

func p79AssertArtifactPrivacySafe(content string) {
	ginkgo.GinkgoHelper()

	for _, forbidden := range []string{
		"/Users/",
		"RAW_PROMPT_BODY_SENTINEL",
		"RAW_FILE_BODY_SENTINEL",
		"P76_FINANCE_SENTINEL_DO_NOT_LEAK",
		"P76_HEALTHCARE_SENTINEL_DO_NOT_LEAK",
		"sk-proj-",
		"github_pat_",
		"ghp_",
		"xoxb-",
		"AcmeCustomer",
		"CustomerX",
	} {
		gomega.Expect(content).NotTo(gomega.ContainSubstring(forbidden), fmt.Sprintf("posture artifact leaked forbidden marker %q", forbidden))
	}
}

func mustMarshalJSON(value any) []byte {
	ginkgo.GinkgoHelper()

	data, err := json.Marshal(value)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("marshal JSON: %v", err))

	return data
}
