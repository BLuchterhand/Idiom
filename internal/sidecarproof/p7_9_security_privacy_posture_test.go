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
	localopscmd "idiom/internal/cmd/localops"
	"idiom/internal/localops"
	"idiom/internal/workspace"
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

func TestP79SecurityPrivacyPostureArtifactsCoverControlsAndClassifications(t *testing.T) {
	root := p79RepoRoot(t)
	matrix := readP79PostureMatrix(t, filepath.Join(root, "docs", "security-privacy-posture.matrix.json"))
	markdown := readP79PostureMarkdown(t, filepath.Join(root, "docs", "security-privacy-posture.md"))

	if matrix.SchemaVersion != "idiom.security_privacy_posture.v1" || matrix.ArtifactID != "p7.9-security-privacy-posture" {
		t.Fatalf("unexpected posture matrix identity: %#v", matrix)
	}
	for _, field := range []string{matrix.Scope.MVPStance, matrix.Scope.DecisionBoundary} {
		if strings.TrimSpace(field) == "" {
			t.Fatalf("posture matrix scope must state stance and boundary: %#v", matrix.Scope)
		}
	}
	if !strings.Contains(markdown, "P7.9") ||
		!strings.Contains(markdown, "security-privacy-posture.matrix.json") ||
		!strings.Contains(markdown, "not a SOC 2") ||
		!strings.Contains(markdown, "MVP limitation") {
		t.Fatalf("posture markdown missing required review language:\n%s", markdown)
	}

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
		if strings.TrimSpace(control.Claim) == "" || strings.TrimSpace(control.MVPLimitations) == "" {
			t.Fatalf("control %q must carry claim and MVP limitation: %#v", control.ID, control)
		}
		if len(control.Evidence) == 0 {
			t.Fatalf("control %q must carry executable evidence", control.ID)
		}
		for _, evidence := range control.Evidence {
			if evidence.Kind != "go_test" || !strings.HasPrefix(evidence.Command, "go test ") ||
				strings.TrimSpace(evidence.Path) == "" || strings.TrimSpace(evidence.CheckID) == "" {
				t.Fatalf("control %q has non-executable evidence row: %#v", control.ID, evidence)
			}
			if strings.Contains(evidence.Path, "/Users/") || filepath.IsAbs(evidence.Path) {
				t.Fatalf("evidence path should be repo-relative and privacy-safe: %#v", evidence)
			}
		}
	}
	for _, id := range requiredControlIDs {
		if _, ok := controls[id]; !ok {
			t.Fatalf("posture matrix missing required control %q", id)
		}
		if !strings.Contains(markdown, strings.ReplaceAll(id, "_", " ")) &&
			!strings.Contains(markdown, p79ControlMarkdownName(id)) {
			t.Fatalf("posture markdown does not mention control %q", id)
		}
	}

	rows := map[autonomy.ClientClassification]p79ClientClassificationRow{}
	for _, row := range matrix.ClientClassificationCoverage {
		rows[row.Classification] = row
		if strings.TrimSpace(row.Stance) == "" || strings.TrimSpace(row.Limitation) == "" ||
			strings.TrimSpace(row.P76CommandID) == "" || len(row.CoveredControlIDs) == 0 {
			t.Fatalf("classification row must carry stance, limitation, P7.6 command, and controls: %#v", row)
		}
		for _, controlID := range row.CoveredControlIDs {
			if _, ok := controls[controlID]; !ok {
				t.Fatalf("classification %q references unknown control %q", row.Classification, controlID)
			}
		}
	}
	p76Examples := map[autonomy.ClientClassification]string{}
	for _, example := range p76ClassificationExamples() {
		p76Examples[example.classification] = example.commandID
	}
	for _, classification := range autonomy.DefaultClientClassifications() {
		row, ok := rows[classification]
		if !ok {
			t.Fatalf("posture matrix missing classification %q", classification)
		}
		if row.P76CommandID != p76Examples[classification] {
			t.Fatalf("classification %q P7.6 command = %q, want %q", classification, row.P76CommandID, p76Examples[classification])
		}
		if !strings.Contains(markdown, "`"+string(classification)+"`") {
			t.Fatalf("posture markdown missing classification %q", classification)
		}
	}

	p79AssertArtifactPrivacySafe(t, string(mustMarshalJSON(t, matrix))+markdown)
}

func TestP79SecurityPrivacyControlsExecuteGenericEvidence(t *testing.T) {
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
	if err != nil {
		t.Fatalf("ResolvePathRoles: %v", err)
	}
	p79AssertPathDenied(t, resolved, ".env", autonomy.InvariantProtectedPath)
	p79AssertPathDenied(t, resolved, ".git/config", autonomy.InvariantForbiddenPath)

	configPath := writeMainConfigRoot(t, root)
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
	if code != 1 {
		t.Fatalf("expected discovery-profile write denial, got %d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	payload := decodeSidecarCompatJSON(t, stdout.Bytes())
	assertP76JSONPath(t, payload, "sidecar.profile", "discovery")
	assertP76JSONPath(t, payload, "sidecar.error.class", "profile_denied")
	for _, forbidden := range []string{privateOutputPath, root, rawPrompt, rawFileBody, "account_number=123456789", "patient_name=Sample"} {
		if strings.Contains(stdout.String()+stderr.String(), forbidden) {
			t.Fatalf("denial payload leaked forbidden value %q\nstdout=%s\nstderr=%s", forbidden, stdout.String(), stderr.String())
		}
	}
	if _, err := os.Stat(privateOutputPath); !os.IsNotExist(err) {
		t.Fatalf("denied discovery write should not create %s; stat err=%v", privateOutputPath, err)
	}

	for _, req := range []workspace.ToolRequest{
		{Tool: "write_file", Path: "regulated/private-output.txt", Content: "x"},
		{Tool: "shell", Command: "printf touched"},
		{Tool: "context_pack", Task: "artifact-writing discovery"},
		{Tool: "context_status", Refresh: true},
		{Tool: "context_query", Query: "callers of Run"},
	} {
		decision := workspace.ToolRequestAllowedForProfile(req, workspace.ToolProfileDiscovery)
		if decision.Allowed {
			t.Fatalf("discovery profile should deny request %#v", req)
		}
		if decision.Code != "profile_denied" || strings.TrimSpace(decision.Reason) == "" {
			t.Fatalf("discovery denial should carry structured reason: %#v", decision)
		}
	}
	allowed := workspace.ToolRequestAllowedForProfile(workspace.ToolRequest{
		Tool:            "context_status",
		FreshnessPolicy: "report_only",
	}, workspace.ToolProfileDiscovery)
	if !allowed.Allowed {
		t.Fatalf("report-only context_status should remain allowed for discovery profile: %#v", allowed)
	}

	localRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(localRoot, "go.mod"), []byte("module example.com/p79\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write local go.mod: %v", err)
	}
	stageDoctorConfigWithBudget(t, localRoot, 16384, 1024)
	report, err := localopscmd.BuildBenchmarkReport(localopscmd.BenchmarkOptions{
		ConfigPath:        filepath.Join(localRoot, ".idiom", "config.json"),
		Profile:           "sidecar",
		OutputPath:        filepath.Join(localRoot, "evidence", "p79-posture.json"),
		QuickstartSmoke:   true,
		QuickstartTask:    "P79 private repo task /Users/example/secret token=abc123",
		QuickstartCleanup: "remove-local-state",
	}, nil)
	if err != nil {
		t.Fatalf("build local-ops report: %v", err)
	}
	if !report.Privacy.MetadataOnly || report.Privacy.RawPromptCaptured ||
		report.Privacy.RawFileBodyCaptured || report.Privacy.SecretCaptureAllowed {
		t.Fatalf("local-ops privacy evidence must remain metadata-only: %#v", report.Privacy)
	}
	if !strings.HasPrefix(report.Privacy.LocalArtifactDirectory, "path_id:") {
		t.Fatalf("local artifact directory should be represented as a stable path id, got %#v", report.Privacy)
	}
	if report.Telemetry != nil {
		t.Fatalf("posture report should not require telemetry when no recorder is supplied: %#v", report.Telemetry)
	}
	if report.Quickstart == nil {
		t.Fatal("expected quickstart privacy evidence")
	}
	smoke := report.Quickstart
	if smoke.ProductEvidenceSafeOutput.HostedTelemetryDefault ||
		smoke.PrivacyRedactionStatus.HostedTelemetryRequired ||
		smoke.FirstUsefulPacket.Task.RawTaskCaptured ||
		smoke.PrivacyRedactionStatus.RawPromptCaptured ||
		smoke.PrivacyRedactionStatus.RawFileBodyCaptured ||
		smoke.PrivacyRedactionStatus.SecretCaptured ||
		smoke.PrivacyRedactionStatus.PrivatePathCaptured {
		t.Fatalf("quickstart posture evidence should minimize telemetry and raw content: %#v", smoke)
	}
	renderedReport := string(mustMarshalJSON(t, report))
	for _, forbidden := range []string{"/Users/example/secret", "token=abc123", "P79 private repo task"} {
		if strings.Contains(renderedReport, forbidden) {
			t.Fatalf("local-ops posture evidence leaked %q in %s", forbidden, renderedReport)
		}
	}
	if finding := localops.ForbiddenEvidenceLeak(renderedReport); finding != "" {
		t.Fatalf("local-ops posture evidence failed forbidden-field scan: %q", finding)
	}
}

func readP79PostureMatrix(t *testing.T, path string) p79PostureMatrix {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read posture matrix: %v", err)
	}
	var matrix p79PostureMatrix
	if err := json.Unmarshal(data, &matrix); err != nil {
		t.Fatalf("decode posture matrix: %v\n%s", err, string(data))
	}
	return matrix
}

func readP79PostureMarkdown(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read posture markdown: %v", err)
	}
	return string(data)
}

func p79RepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "../.."))
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

func p79AssertPathDenied(t *testing.T, set autonomy.PathRoleResolutionSet, path string, invariant autonomy.PathInvariantID) {
	t.Helper()
	normalized := filepath.ToSlash(filepath.Clean(path))
	for _, resolution := range set.Resolutions {
		if resolution.Path != normalized && resolution.RelativePath != normalized {
			continue
		}
		if resolution.Decision != autonomy.PolicyDeny || resolution.EffectiveInvariantID != invariant {
			t.Fatalf("path %q should deny via %q, got %#v", path, invariant, resolution)
		}
		return
	}
	t.Fatalf("missing resolved path %q in %#v", path, set.Resolutions)
}

func p79AssertArtifactPrivacySafe(t *testing.T, content string) {
	t.Helper()
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
		if strings.Contains(content, forbidden) {
			t.Fatalf("posture artifact leaked forbidden marker %q", forbidden)
		}
	}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return data
}
