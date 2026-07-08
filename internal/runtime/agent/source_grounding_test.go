package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"idiom/internal/capabilities/workspace"
	"idiom/internal/platform/config"
	"idiom/internal/runtime/workerruntime"
)

func readOnlySourceGroundingRunner() Runner {
	return Runner{
		RuntimeBrief: workerruntime.WorkerRuntimeBrief{
			TaskFamily:        "direct_answer",
			Contract:          "direct_answer",
			PermissionProfile: string(workerruntime.PermissionReadOnlyWorkspace),
			Objective:         "explain the inspected implementation",
		},
	}
}

func TestSourceGroundingEscapingMentionedPaths(t *testing.T) {
	answer := "The bug is in `/tmp/outside/labels.go:6`, and a related path is ../other/config.yaml."

	paths := sourceGroundingEscapingMentionedPaths(answer)
	if len(paths) != 2 {
		t.Fatalf("escaping paths = %#v, want two", paths)
	}
	if paths[0] != "../other/config.yaml" || paths[1] != "/tmp/outside/labels.go" {
		t.Fatalf("escaping paths = %#v", paths)
	}
}

func TestSourceGroundingEscapingUnsupportedPathsAllowUnverifiedQualification(t *testing.T) {
	answer := "The outside path `/tmp/outside/labels.go:6` is unverified and outside the workspace."

	if paths := sourceGroundingEscapingMentionedPaths(answer); len(paths) != 1 || paths[0] != "/tmp/outside/labels.go" {
		t.Fatalf("escaping path detection changed: %#v", paths)
	}

	if unsupported := sourceGroundingUnsupportedEscapingMentionedPaths(answer, nil); len(unsupported) != 0 {
		t.Fatalf("unverified outside-workspace citation should not block source grounding: %#v", unsupported)
	}
}

func TestSourceGroundingAllowsBackedExternalDocumentCitation(t *testing.T) {
	docPath := filepath.Join(t.TempDir(), "Mobile Documents", "Website Project", "conversation brief.md")
	runner := readOnlySourceGroundingRunner()
	runner.RuntimeBrief.Objective = "What does the Website failure brief say about duplicate surfaces?"
	results := []workspace.ToolResult{
		readFileSourceBodyResult(docPath, 1, 4, 4, "1:The Website failure brief says duplicate surfaces caused drift.\n2:The fix is to consolidate final-answer acceptance and completion-gate projection."),
	}
	answer := "The Website failure brief says duplicate surfaces caused drift, and that is grounded in `" + filepath.ToSlash(filepath.Clean(docPath)) + "`."

	if reason := runner.sourceGroundingRejectionReason(answer, results); reason != "" {
		t.Fatalf("body-backed external document citation should not be rejected: %q", reason)
	}
}

func TestSourceGroundingRejectsUnbackedExternalDocumentCitation(t *testing.T) {
	docPath := filepath.ToSlash(filepath.Join(t.TempDir(), "Mobile Documents", "Website Project", "conversation brief.md"))
	runner := readOnlySourceGroundingRunner()
	runner.RuntimeBrief.Objective = "What does the Website failure brief say about duplicate surfaces?"
	answer := "The Website failure brief says duplicate surfaces caused drift, and that is grounded in `" + docPath + "`."

	reason := runner.sourceGroundingRejectionReason(answer, nil)
	if !strings.Contains(reason, docPath) {
		t.Fatalf("unbacked external document citation should be rejected, got %q", reason)
	}
}

func TestSourceGroundingAllowsRelativeCitationToBackedExternalDocument(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Mobile Documents", "com~apple~CloudDocs", "Idiom Book Project")
	backMatterPath := filepath.Join(root, "front-back", "back-matter.md")
	bookMatterPath := filepath.Join(root, "front-back", "book-matter-and-cover-copy-proposal.md")
	runner := readOnlySourceGroundingRunner()
	runner.RuntimeBrief.Objective = "What can be added to the Website from the iCloud Idiom book project?"
	results := []workspace.ToolResult{
		readFileSourceBodyResult(bookMatterPath, 1, 80, 120, "1:The book matter proposes Trust the Process, Not the Prompt as the book title.\n"),
		readFileSourceBodyResult(backMatterPath, 1, 80, 95, "1:The back matter includes author biography notes for the website.\n"),
	}
	answer := "Add book-positioning copy from `front-back/book-matter-and-cover-copy-proposal.md` and author bio material from `front-back/back-matter.md`."

	if reason := runner.sourceGroundingRejectionReason(answer, results); reason != "" {
		t.Fatalf("relative citations to body-backed external documents should not be rejected: %q", reason)
	}
}

func TestSourceGroundingAllowsBacktickedExternalDocumentPathWithSpaces(t *testing.T) {
	docPath := filepath.ToSlash(filepath.Join(t.TempDir(), "Mobile Documents", "com~apple~CloudDocs", "Idiom Book Project", "front-back", "back-matter.md"))
	runner := readOnlySourceGroundingRunner()
	runner.RuntimeBrief.Objective = "What can be added to the Website from the Idiom book and author material?"
	results := []workspace.ToolResult{
		readFileSourceBodyResult(docPath, 1, 80, 95, "1:The back matter includes author biography notes for the website.\n"),
	}
	answer := "The author material is grounded in `" + docPath + "`."

	if paths := sourceGroundingMentionedPaths(answer); !slices.Contains(paths, docPath) || slices.Contains(paths, "Project/front-back/back-matter.md") {
		t.Fatalf("mentioned paths = %#v, want intact external path without truncated Project fragment", paths)
	}

	if reason := runner.sourceGroundingRejectionReason(answer, results); reason != "" {
		t.Fatalf("backticked body-backed external document path with spaces should not be rejected: %q", reason)
	}
}

func TestSourceGroundingMentionedPathsIgnoreBacktickedCommands(t *testing.T) {
	answer := "| Test file | Command |\n| `tests/test_models.py` | `pytest tests/test_models.py` |"

	paths := sourceGroundingMentionedPaths(answer)
	if !slices.Contains(paths, "tests/test_models.py") {
		t.Fatalf("mentioned paths = %#v, want tests/test_models.py", paths)
	}
	if slices.Contains(paths, "pytest tests/test_models.py") {
		t.Fatalf("mentioned paths should not include backticked command span: %#v", paths)
	}
}

func TestSourceGroundingAllowsQuotedLineRangeForBackedSourceBody(t *testing.T) {
	runner := readOnlySourceGroundingRunner()
	results := []workspace.ToolResult{
		readFileSourceBodyResult(
			"docs/decisioning-north-star.md",
			26,
			53,
			0,
			"28:Read-only lanes are strategy lanes.\n30:The model owns strategy; the harness owns contracts.",
		),
	}
	answer := "From `docs/decisioning-north-star.md:28-34`, the north star says the model owns strategy and the harness owns contracts."

	paths := sourceGroundingMentionedPaths(answer)
	if !slices.Contains(paths, "docs/decisioning-north-star.md") {
		t.Fatalf("mentioned paths = %#v, want line range stripped to docs/decisioning-north-star.md", paths)
	}
	if slices.Contains(paths, "docs/decisioning-north-star.md:28-34") {
		t.Fatalf("mentioned paths should not include literal line range: %#v", paths)
	}

	if reason := runner.sourceGroundingRejectionReason(answer, results); reason != "" {
		t.Fatalf("body-backed quoted line range should not be rejected: %q", reason)
	}
}

func TestSourceGroundingRejectsAmbiguousRelativeExternalDocumentCitation(t *testing.T) {
	tempDir := t.TempDir()
	firstPath := filepath.Join(tempDir, "Project A", "front-back", "back-matter.md")
	secondPath := filepath.Join(tempDir, "Project B", "front-back", "back-matter.md")
	runner := readOnlySourceGroundingRunner()
	runner.RuntimeBrief.Objective = "What can be added to the Website from the iCloud Idiom book project?"
	results := []workspace.ToolResult{
		readFileSourceBodyResult(firstPath, 1, 10, 10, "1:first back matter\n"),
		readFileSourceBodyResult(secondPath, 1, 10, 10, "1:second back matter\n"),
	}
	answer := "Use the author material from `front-back/back-matter.md`."

	reason := runner.sourceGroundingRejectionReason(answer, results)
	if !strings.Contains(reason, "front-back/back-matter.md") {
		t.Fatalf("ambiguous relative external citation should remain rejected, got %q", reason)
	}
}

func TestSourceGroundingAllowsUniqueBareExternalDocumentCitation(t *testing.T) {
	docPath := filepath.Join(t.TempDir(), "Mobile Documents", "Idiom Book Project", "front-back", "back-matter.md")
	runner := readOnlySourceGroundingRunner()
	results := []workspace.ToolResult{
		readFileSourceBodyResult(docPath, 1, 10, 10, "1:The back matter includes author biography notes.\n"),
	}
	answer := "`back-matter.md:1` contains the author notes."

	if reason := runner.sourceGroundingRejectionReason(answer, results); reason != "" {
		t.Fatalf("unique bare external document citation should be accepted, got %q", reason)
	}
}

func TestSourceGroundingRejectsBareCitationAmbiguousAcrossWorkspaceAndExternalBodyReads(t *testing.T) {
	root := t.TempDir()
	workspacePath := "docs/back-matter.md"
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir workspace docs: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(workspacePath)), []byte("workspace back matter\n"), 0o644); err != nil {
		t.Fatalf("write workspace back matter: %v", err)
	}

	externalPath := filepath.Join(t.TempDir(), "Mobile Documents", "Idiom Book Project", "front-back", "back-matter.md")
	runner := readOnlySourceGroundingRunner()
	runner.Workspace = workspace.NewManager(config.Workspace{Root: root, CWD: root})
	results := []workspace.ToolResult{
		readFileSourceBodyResult(workspacePath, 1, 5, 5, "1:workspace back matter\n"),
		readFileSourceBodyResult(externalPath, 1, 10, 10, "1:external back matter\n"),
	}
	answer := "`back-matter.md:1` contains the author notes."

	reason := runner.sourceGroundingRejectionReason(answer, results)
	if !strings.Contains(reason, "back-matter.md") {
		t.Fatalf("bare citation ambiguous across workspace and external body reads should be rejected, got %q", reason)
	}
}

func TestSourceGroundingRejectsExistingWorkspaceCitationEvenWhenExternalSuffixMatches(t *testing.T) {
	root := t.TempDir()
	workspacePath := "front-back/back-matter.md"
	if err := os.MkdirAll(filepath.Join(root, "front-back"), 0o755); err != nil {
		t.Fatalf("mkdir workspace front-back: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(workspacePath)), []byte("workspace back matter\n"), 0o644); err != nil {
		t.Fatalf("write workspace back matter: %v", err)
	}

	externalPath := filepath.Join(t.TempDir(), "Mobile Documents", "Idiom Book Project", "front-back", "back-matter.md")
	runner := readOnlySourceGroundingRunner()
	runner.Workspace = workspace.NewManager(config.Workspace{Root: root, CWD: root})
	results := []workspace.ToolResult{
		readFileSourceBodyResult(externalPath, 1, 10, 10, "1:external back matter\n"),
	}
	answer := "`front-back/back-matter.md:1` defines the workspace author bio copy."

	reason := runner.sourceGroundingRejectionReason(answer, results)
	if !strings.Contains(reason, workspacePath) {
		t.Fatalf("existing workspace citation without workspace body evidence should not be laundered through external suffix, got %q", reason)
	}
}

func TestSourceGroundingAssessmentPayloadCarriesMissingPaths(t *testing.T) {
	assessment := sourceGroundingMissingCandidateAssessment([]string{
		"essay-series.md",
		"front-back/back-matter.md",
		"front-back/book-matter-and-cover-copy-proposal.md",
	})

	result := annotateSourceGroundingAssessment(workspace.ToolResult{Tool: "completion_gate"}, assessment)
	got, ok := sourceGroundingAssessmentFromResult(result)
	if !ok {
		t.Fatal("expected typed source-grounding payload")
	}

	want := []string{
		"essay-series.md",
		"front-back/back-matter.md",
		"front-back/book-matter-and-cover-copy-proposal.md",
	}
	if got.Kind != sourceGroundingRejectionMissingCandidate {
		t.Fatalf("kind = %q, want %q", got.Kind, sourceGroundingRejectionMissingCandidate)
	}
	if !slices.Equal(got.Paths, want) {
		t.Fatalf("source-grounding paths = %#v, want %#v", got.Paths, want)
	}
}

func TestSourceGroundingAssessmentPayloadRequiresCompletionGatePayloadType(t *testing.T) {
	assessment := sourceGroundingMissingCandidateAssessment([]string{"essay-series.md"})
	result := annotateSourceGroundingAssessment(workspace.ToolResult{Tool: "completion_gate"}, assessment)

	wrongTool := result
	wrongTool.Tool = workspace.ToolNameReadFile
	if _, ok := sourceGroundingAssessmentFromResult(wrongTool); ok {
		t.Fatal("source-grounding payload on a non-completion-gate result must not carry authority")
	}

	wrongPayloadType := result
	wrongPayloadType.PayloadType = "agent.source_grounding_assessment.v0"
	if _, ok := sourceGroundingAssessmentFromResult(wrongPayloadType); ok {
		t.Fatal("source-grounding payload with an unexpected payload type must not carry authority")
	}
}

func TestSourceGroundingRejectsPathOnlyCitationSupport(t *testing.T) {
	runner := readOnlySourceGroundingRunner()
	results := []workspace.ToolResult{{
		Tool:    "read_file",
		Paths:   []string{"internal/workflows/productworkflow/coverage_gate.go"},
		Summary: "Read internal/workflows/productworkflow/coverage_gate.go",
		Output:  "package productworkflow\nfunc EvaluateCoverageGate() {}\n",
	}}

	reason := runner.sourceGroundingRejectionReason(
		"The current source `internal/workflows/productworkflow/coverage_gate.go` defines the coverage gate behavior.",
		results,
	)
	if !strings.Contains(reason, "internal/workflows/productworkflow/coverage_gate.go") {
		t.Fatalf("path-only read metadata must not satisfy source-body citation support, got %q", reason)
	}
}

func TestSourceGroundingRawBodyEvidenceSatisfiesCitationSupport(t *testing.T) {
	runner := readOnlySourceGroundingRunner()
	results := []workspace.ToolResult{
		readFileSourceBodyResult("internal/workflows/productworkflow/coverage_gate.go", 1, 80, 552, "func EvaluateCoverageGate() {}\n"),
	}

	reason := runner.sourceGroundingRejectionReason(
		"The current source `internal/workflows/productworkflow/coverage_gate.go` defines the coverage gate behavior.",
		results,
	)
	if reason != "" {
		t.Fatalf("expected raw read_file source-body evidence to satisfy citation support, got %q", reason)
	}
}

func TestSourceGroundingBasenameResolutionUsesResultBackedEvidence(t *testing.T) {
	writeFixture := func(root string, path string) {
		t.Helper()
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir fixture path %q: %v", path, err)
		}
		if err := os.WriteFile(fullPath, []byte("package app\nfunc targetBehavior() {}\n"), 0o644); err != nil {
			t.Fatalf("write fixture path %q: %v", path, err)
		}
	}

	const uniquePath = "internal/app/target.go"
	root := t.TempDir()
	writeFixture(root, uniquePath)

	rawRunner := readOnlySourceGroundingRunner()
	rawRunner.Workspace = workspace.NewManager(config.Workspace{Root: root, CWD: root})
	rawResults := []workspace.ToolResult{
		readFileSourceBodyResult("internal/app/target.go", 1, 20, 0, "package app\nfunc targetBehavior() {}\n"),
	}
	if reason := rawRunner.sourceGroundingRejectionReason("`target.go:2` defines target behavior.", rawResults); reason != "" {
		t.Fatalf("unique basename backed by raw source-body evidence blocked: %q", reason)
	}

	ambiguousRoot := t.TempDir()
	writeFixture(ambiguousRoot, uniquePath)
	writeFixture(ambiguousRoot, "internal/other/target.go")
	ambiguousRunner := readOnlySourceGroundingRunner()
	ambiguousRunner.Workspace = workspace.NewManager(config.Workspace{Root: ambiguousRoot, CWD: ambiguousRoot})
	ambiguousResults := []workspace.ToolResult{
		readFileSourceBodyResult(uniquePath, 1, 20, 0, "package app\nfunc targetBehavior() {}\n"),
		readFileSourceBodyResult("internal/other/target.go", 1, 20, 0, "package other\nfunc targetBehavior() {}\n"),
	}
	reason := ambiguousRunner.sourceGroundingRejectionReason("`target.go:2` defines target behavior.", ambiguousResults)
	if !strings.Contains(reason, "target.go") {
		t.Fatalf("ambiguous basename should remain unsupported, got %q", reason)
	}
}

func TestSourceCitationLocalWindowReturnsEmptyWhenPathAbsent(t *testing.T) {
	answer := "The current source `internal/app/current.go` defines the active behavior."

	if got := sourceCitationLocalWindow(answer, "internal/app/missing.go", 80, 140); got != "" {
		t.Fatalf("absent citation local window = %q, want empty", got)
	}
}

func TestSourceCitationClaimsDoNotBorrowTextFromAnotherCitation(t *testing.T) {
	answer := "The current source `internal/app/current.go` defines the active behavior. Future work could add `internal/app/future.go`."

	if !sourceCitationClaimsCurrentSourcePath(answer, "internal/app/current.go") {
		t.Fatal("current-source claim next to cited path should be detected")
	}

	if sourceCitationClaimsCurrentSourcePath(answer, "internal/app/missing.go") {
		t.Fatal("absent path must not inherit current-source wording from another citation")
	}
}
