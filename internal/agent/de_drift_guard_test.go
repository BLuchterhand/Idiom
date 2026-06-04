package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestDeDriftGuidanceDocumentsBoundaryChecklist(t *testing.T) {
	root := filepath.Join("..", "..")
	required := map[string][]string{
		filepath.Join(root, ".codex", "common-traps.md"): {
			"Drift is cheap",
			"De-Drift Checklist",
			"Canonical owner",
			"Consumer type",
			"Preserve lanes",
			"Cleanup lanes",
			"Negative oracle",
			"Test contradiction scan",
			"Machine-consumed plan/repair/mutation terminal phases",
		},
		filepath.Join(root, "docs", "idiom-maintenance-best-practices.md"): {
			"Required De-Drift Gate",
			"Drift is cheap",
			"Canonical owner",
			"Consumer matrix",
			"Preserve lanes",
			"Cleanup lanes",
			"Negative oracle",
			"machine-consumed protocol remains machine-readable",
			"canonical owner/duplicate authority",
			"test-oracle quality",
		},
	}

	for path, markers := range required {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(body)
		for _, marker := range markers {
			if !strings.Contains(text, marker) {
				t.Fatalf("%s missing de-drift marker %q", path, marker)
			}
		}
	}
}

func TestDeDriftGuardRejectsParallelQuestionSanitizerNames(t *testing.T) {
	root := filepath.Join("..", "..")
	productionFiles := agentProductionFiles(t, root)
	forbidden := []string{
		"completionGateFinalTransportAnswer",
		"userFacingCompletionBlockerReason",
		"stripInternalBlockerClause",
		"sanitizesUserFacingQuestionBlockers",
		"readOnlyBehaviorLogObjectiveApplies",
		"readOnlyDiagnosticLogEvidenceApplies",
		"readOnlyCompleteDiagnosticLogEvidencePaths",
		"complete diagnostic log evidence",
		"bounded codebase evidence",
	}

	for _, path := range productionFiles {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(body)
		for _, marker := range forbidden {
			if strings.Contains(text, marker) {
				t.Fatalf("%s contains drift-prone helper or stale wording %q", path, marker)
			}
		}
	}
}

func TestDeDriftGuardRejectsRecurringDriftCategories(t *testing.T) {
	root := filepath.Join("..", "..")
	findings := deDriftProductionFindings(t, agentProductionFiles(t, root))
	if len(findings) > 0 {
		sort.Strings(findings)
		t.Fatalf("production drift patterns found:\n- %s", strings.Join(findings, "\n- "))
	}
}

func TestDeDriftPatternDetectorCoversRecurringCategories(t *testing.T) {
	cases := []struct {
		name string
		path string
		body string
		want string
	}{
			{
				name: "topic specific evidence path authority",
				path: filepath.Join("internal", "agent", "answer_composer.go"),
				body: `func questionExpectedEvidencePaths(question string) []string {
		if delegatedObjectiveIsSubagentArchitecture(question) {
		return []string{"internal/agent/runner.go", "internal/agent/runner_child_tasks.go"}
	}
	return nil
}`,
				want: "topic-specific evidence path authority",
			},
			{
				name: "topic specific evidence path authority outside agent package",
				path: filepath.Join("internal", "agent", "answer_composer.go"),
				body: `func questionExpectedEvidencePaths(question string) []string {
		if delegatedObjectiveIsDiscoveryToolUsefulness(question) {
			return []string{"internal/workspace/discovery_query.go"}
		}
		return nil
}`,
				want: "topic-specific evidence path authority",
			},
		{
			name: "broad diagnostic answerability promotion",
			path: filepath.Join("internal", "agent", "read_only_answerability.go"),
			body: `func (l readOnlyEvidenceLedger) Answerable() bool {
	return l.SourceRelevant || l.DiagnosticRelevant
}
func readOnlyDiagnosticObjectiveApplies(objective string) bool {
	return containsAny(strings.ToLower(objective), "error", "failed", "build")
}`,
			want: "diagnostic evidence promoted to answerability",
		},
			{
				name: "broad diagnostic objective classifier",
				path: filepath.Join("internal", "agent", "read_only_answerability.go"),
				body: `func readOnlyDiagnosticObjectiveApplies(objective string) bool {
		return containsAny(strings.ToLower(objective), "error", "failed", "build")
}`,
				want: "broad diagnostic objective classifier",
			},
			{
				name: "broad diagnostic objective classifier with unrelated typed text",
				path: filepath.Join("internal", "agent", "read_only_answerability.go"),
				body: `type DiagnosticContract struct { TargetRefs []string }
func readOnlyDiagnosticObjectiveApplies(objective string) bool {
	return containsAny(strings.ToLower(objective), "error", "failed", "build")
}`,
				want: "broad diagnostic objective classifier",
			},
		{
			name: "broad final-answer blocker classifier",
			path: filepath.Join("internal", "agent", "runner.go"),
			body: `func looksLikeNonAnswerProse(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.HasPrefix(lower, "blocker:")
}`,
			want: "broad final-answer blocker classifier",
		},
			{
				name: "duplicate question sanitizer authority",
				path: filepath.Join("internal", "agent", "runner.go"),
				body: `func sanitizeUserQuestionBlockerForDisplay(answer string) string {
		return strings.TrimPrefix(answer, "<<QUESTION>>")
}`,
				want: "duplicate question sanitizer authority",
			},
			{
				name: "duplicate question sanitizer authority via replace",
				path: filepath.Join("internal", "agent", "runner.go"),
				body: `func finalTransport(answer string) string {
	return strings.ReplaceAll(answer, "<<QUESTION>>:", "")
}`,
				want: "duplicate question sanitizer authority",
			},
		}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := deDriftFindingsForSource(tc.path, tc.body)
			for _, finding := range findings {
				if strings.Contains(finding, tc.want) {
					return
				}
			}
			t.Fatalf("expected finding containing %q, got %#v", tc.want, findings)
		})
	}
}

func agentProductionFiles(t *testing.T, root string) []string {
	t.Helper()
	dir := filepath.Join(root, "internal", "agent")
	var files []string
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	sort.Strings(files)
	return files
}

func deDriftProductionFindings(t *testing.T, paths []string) []string {
	t.Helper()
	var findings []string
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		findings = append(findings, deDriftFindingsForSource(path, string(body))...)
	}
	return findings
}

func deDriftFindingsForSource(path, body string) []string {
	file := filepath.ToSlash(path)
	if strings.Contains(file, "_test.go") {
		return nil
	}
	text := normalizeDeDriftWhitespace(body)
	var findings []string

	if topicSpecificEvidencePathAuthority(file, text) {
		findings = append(findings, file+": topic-specific evidence path authority: expected evidence paths must come from typed scope or discovery results, not question templates")
	}
	if diagnosticEvidencePromotedToAnswerability(file, text) {
		findings = append(findings, file+": diagnostic evidence promoted to answerability: diagnostic evidence needs a typed target/path contract before it can satisfy read-only completion")
	}
	if broadDiagnosticObjectiveClassifier(file, text) {
		findings = append(findings, file+": broad diagnostic objective classifier: words like error/build/failed cannot turn unrelated diagnostics into objective evidence")
	}
	if broadFinalAnswerBlockerClassifier(file, text) {
		findings = append(findings, file+": broad final-answer blocker classifier: transport cleanup must preserve legitimate user-facing blocker answers")
	}
	if duplicateQuestionSanitizerAuthority(file, text) {
		findings = append(findings, file+": duplicate question sanitizer authority: user-facing <<QUESTION>> cleanup belongs in final_answer_decorator.go")
	}
	return findings
}

func normalizeDeDriftWhitespace(body string) string {
	return regexp.MustCompile(`\s+`).ReplaceAllString(body, " ")
}

func topicSpecificEvidencePathAuthority(file, text string) bool {
	if !agentProductionSource(file) || !strings.Contains(text, "[]string{") {
		return false
	}
	hasQuestionOrObjectiveClassifier := strings.Contains(text, "questionExpectedEvidencePaths") ||
		strings.Contains(text, "delegatedObjectiveIs") ||
		strings.Contains(text, "ObjectiveIs") ||
		strings.Contains(text, "QuestionIs")
	return hasQuestionOrObjectiveClassifier && regexp.MustCompile(`"internal/[^"]+\.go"`).MatchString(text)
}

func diagnosticEvidencePromotedToAnswerability(file, text string) bool {
	if !agentProductionSource(file) || !strings.Contains(text, "DiagnosticRelevant") {
		return false
	}
	promotesDiagnostic := regexp.MustCompile(`Answerable\s*\(\s*\).*DiagnosticRelevant`).MatchString(text) ||
		regexp.MustCompile(`return[^;\n{}]*DiagnosticRelevant`).MatchString(text)
	if !promotesDiagnostic {
		return false
	}
	return broadDiagnosticObjectiveClassifier(file, text) ||
		strings.Contains(text, "readOnlyDiagnosticObjectiveApplies")
}

func broadDiagnosticObjectiveClassifier(file, text string) bool {
	if !agentProductionSource(file) || !strings.Contains(text, "DiagnosticObjective") {
		return false
	}
	classifier := diagnosticObjectiveClassifierWindow(text)
	if classifier == "" {
		return false
	}
	broadTerms := []string{`"error"`, `"errors"`, `"failure"`, `"failures"`, `"failing"`, `"failed"`, `"build"`, `"broken"`}
	if !containsAny(classifier, broadTerms...) {
		return false
	}
	return !strings.Contains(classifier, "Target") &&
		!strings.Contains(classifier, "EvidencePath") &&
		!strings.Contains(classifier, "DiagnosticContract")
}

func diagnosticObjectiveClassifierWindow(text string) string {
	idx := strings.Index(text, "DiagnosticObjective")
	if idx < 0 {
		return ""
	}
	start := strings.LastIndex(text[:idx], "func ")
	if start < 0 {
		start = max(0, idx-120)
	}
	end := idx + 600
	if end > len(text) {
		end = len(text)
	}
	if nextFunc := strings.Index(text[idx+len("DiagnosticObjective"):end], "\nfunc "); nextFunc >= 0 {
		end = idx + len("DiagnosticObjective") + nextFunc
	}
	return text[start:end]
}

func broadFinalAnswerBlockerClassifier(file, text string) bool {
	if !agentProductionSource(file) || !strings.Contains(text, "looksLikeNonAnswerProse") {
		return false
	}
	return strings.Contains(text, `HasPrefix(lower, "blocker:")`) ||
		strings.Contains(text, `Contains(lower, "\nblocker:")`) ||
		strings.Contains(text, `Contains(lower, "blocker:")`)
}

func duplicateQuestionSanitizerAuthority(file, text string) bool {
	if strings.HasSuffix(file, "/internal/agent/final_answer_decorator.go") {
		return false
	}
	if !agentProductionSource(file) || !strings.Contains(text, "<<QUESTION>>") {
		return false
	}
	functionNameLooksLikeUserQuestionCleanup := regexp.MustCompile(`func\s+\w*(Sanitize|sanitize|Clean|clean|Strip|strip|Presentable|presentable|UserFacing|userFacing)\w*(Question|UserFacing)\w*(Blocker|Answer|Marker|Display)?\s*\(`).MatchString(text)
	bodyStripsQuestionMarker := regexp.MustCompile(`Trim(Prefix|Left)\s*\([^)]*<<QUESTION>>`).MatchString(text)
	bodyReplacesQuestionMarker := regexp.MustCompile(`Replace(All)?\s*\([^)]*<<QUESTION>>`).MatchString(text)
	return functionNameLooksLikeUserQuestionCleanup || bodyStripsQuestionMarker || bodyReplacesQuestionMarker
}

func agentProductionSource(file string) bool {
	file = filepath.ToSlash(file)
	return strings.Contains(file, "internal/agent/") && strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go")
}
