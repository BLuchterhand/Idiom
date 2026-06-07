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
	root := agentTestRepoRoot(t)
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
	root := agentTestRepoRoot(t)
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
	root := agentTestRepoRoot(t)
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
			path: filepath.Join("internal", "runtime", "agent", "answer_composer.go"),
			body: `func questionExpectedEvidencePaths(question string) []string {
		if delegatedObjectiveIsSubagentArchitecture(question) {
		return []string{"internal/runtime/agent/runner.go", "internal/runtime/agent/runner_child_tasks.go"}
	}
	return nil
}`,
			want: "topic-specific evidence path authority",
		},
		{
			name: "topic specific evidence path authority outside agent package",
			path: filepath.Join("internal", "runtime", "agent", "answer_composer.go"),
			body: `func questionExpectedEvidencePaths(question string) []string {
		if delegatedObjectiveIsDiscoveryToolUsefulness(question) {
			return []string{"internal/capabilities/workspace/discovery_query.go"}
		}
		return nil
}`,
			want: "topic-specific evidence path authority",
		},
		{
			name: "broad diagnostic answerability promotion",
			path: filepath.Join("internal", "runtime", "agent", "read_only_answerability.go"),
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
			path: filepath.Join("internal", "runtime", "agent", "read_only_answerability.go"),
			body: `func readOnlyDiagnosticObjectiveApplies(objective string) bool {
		return containsAny(strings.ToLower(objective), "error", "failed", "build")
}`,
			want: "broad diagnostic objective classifier",
		},
		{
			name: "broad diagnostic objective classifier with unrelated typed text",
			path: filepath.Join("internal", "runtime", "agent", "read_only_answerability.go"),
			body: `type DiagnosticContract struct { TargetRefs []string }
func readOnlyDiagnosticObjectiveApplies(objective string) bool {
	return containsAny(strings.ToLower(objective), "error", "failed", "build")
}`,
			want: "broad diagnostic objective classifier",
		},
		{
			name: "broad final-answer blocker classifier",
			path: filepath.Join("internal", "runtime", "agent", "runner.go"),
			body: `func looksLikeNonAnswerProse(answer string) bool {
	lower := strings.ToLower(answer)
	return strings.HasPrefix(lower, "blocker:")
}`,
			want: "broad final-answer blocker classifier",
		},
		{
			name: "duplicate question sanitizer authority",
			path: filepath.Join("internal", "runtime", "agent", "runner.go"),
			body: `func sanitizeUserQuestionBlockerForDisplay(answer string) string {
		return strings.TrimPrefix(answer, "<<QUESTION>>")
}`,
			want: "duplicate question sanitizer authority",
		},
		{
			name: "duplicate question sanitizer authority via replace",
			path: filepath.Join("internal", "runtime", "agent", "runner.go"),
			body: `func finalTransport(answer string) string {
	return strings.ReplaceAll(answer, "<<QUESTION>>:", "")
}`,
			want: "duplicate question sanitizer authority",
		},
		{
			name: "duplicate read-only closeout presenter authority",
			path: filepath.Join("internal", "runtime", "agent", "runner.go"),
			body: `func fallbackHostReadOnlyCloseoutAnswer(objective string, paths []string) string {
	var b strings.Builder
	b.WriteString("I could not produce a supported final answer from this run.")
	b.WriteString(" Evidence paths: ")
	b.WriteString(formatInlinePathList(paths, 4))
	return b.String()
}`,
			want: "duplicate read-only closeout presenter authority",
		},
		{
			name: "path prefix answerability promotion",
			path: filepath.Join("internal", "runtime", "agent", "runner.go"),
			body: `func isGroundedChildPacketEvidencePath(objective string, path string) bool {
	if strings.HasPrefix(path, "internal/") {
		return true
	}
	return false
}`,
			want: "path prefix answerability promotion",
		},
		{
			name: "duplicate objective classifier authority",
			path: filepath.Join("internal", "runtime", "agent", "runner.go"),
			body: `func readOnlyCauseObjective(objective string) bool {
	words := objectiveWordSet(objective)
	for _, word := range []string{"why", "bug", "failed", "error", "change"} {
		if _, ok := words[word]; ok {
			return true
		}
	}
	return false
}`,
			want: "duplicate objective classifier authority",
		},
		{
			name: "delegated discovery policy accumulation",
			path: filepath.Join("internal", "runtime", "agent", "runner.go"),
			body: `func delegatedDiscoveryPurpose(objective string) string {
	lower := strings.ToLower(objective)
	if containsAny(lower, "security", "unsafe", "vulnerability") {
		return "security_review"
	}
	return "architecture_boundary"
}`,
			want: "delegated discovery request-envelope policy accumulation",
		},
		{
			name: "duplicate action-class vocabulary",
			path: filepath.Join("internal", "runtime", "agent", "phase_contract.go"),
			body: `const (
	actionRead = "read"
	actionSubmitPlan = "submit_plan"
	actionPerEditWrite = "per_edit_write"
)`,
			want: "duplicate action-class vocabulary",
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
	dir := filepath.Join(root, "internal", "runtime", "agent")
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
	if duplicateReadOnlyCloseoutPresenterAuthority(file, text) {
		findings = append(findings, file+": duplicate read-only closeout presenter authority: ReadOnlyCloseout/read_only_closure owns read-only terminal decisions and user-visible uncertainty")
	}
	if pathPrefixAnswerabilityPromotion(file, text) {
		findings = append(findings, file+": path prefix answerability promotion: path prefixes may filter or rank evidence but must not make read-only evidence answerable")
	}
	if duplicateObjectiveClassifierAuthority(file, text) {
		findings = append(findings, file+": duplicate objective classifier authority: objective classification must delegate to the canonical read-only objective relevance contract, not broad local word lists")
	}
	if delegatedDiscoveryPolicyAccumulation(file, text) {
		findings = append(findings, file+": delegated discovery request-envelope policy accumulation: delegatedDiscoveryPlan/runner_lane_policy owns delegated discovery request-envelope normalization")
	}
	if duplicateActionClassVocabulary(file, text) {
		findings = append(findings, file+": duplicate action-class vocabulary: autonomy ActionClass constants/helpers own action-class vocabulary")
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
	if strings.HasSuffix(file, "/internal/runtime/agent/final_answer_decorator.go") {
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

func duplicateReadOnlyCloseoutPresenterAuthority(file, text string) bool {
	if strings.HasSuffix(file, "/internal/runtime/agent/read_only_closure.go") {
		return false
	}
	if !agentProductionSource(file) || !containsAny(text, "ReadOnlyCloseout", "readOnlyCloseout", "readOnlyClosure", "CloseoutAnswer", "ClosureUncertainty") {
		return false
	}
	for _, token := range []string{"ReadOnlyCloseout", "readOnlyCloseout", "readOnlyClosure", "CloseoutAnswer", "ClosureUncertainty"} {
		for _, window := range deDriftFunctionWindows(text, token, 900) {
			namesUserVisibleUncertainty := containsAny(window,
				"presentableUnsupportedFinalAnswerLead",
				"supported final answer",
				"not have enough supported evidence",
				"Source-body evidence is still missing",
				"candidate paths",
				"Evidence paths:",
				"gathered evidence supports answering",
			)
			if namesUserVisibleUncertainty && containsAny(window, "return ", "WriteString", "fmt.Sprintf") {
				return true
			}
		}
	}
	return false
}

func pathPrefixAnswerabilityPromotion(file, text string) bool {
	if !agentProductionSource(file) {
		return false
	}
	for _, token := range []string{"Answerable", "Answerability", "ObjectiveRelevant", "EvidencePath", "Grounded", "Accepted", "Closeout"} {
		for _, window := range deDriftFunctionWindows(text, token, 1100) {
			if !containsAny(window, `HasPrefix(path, "internal/")`, `HasPrefix(path, "cmd/")`, `HasPrefix(path, "pkg/")`, `HasPrefix(path, "src/")`, `HasPrefix(path, "scripts/")`) {
				continue
			}
			if !containsAny(window, "return true", "Answerable", "Relevant", "Accepted", "Grounded", "Objective") {
				continue
			}
			if containsAny(window, "filter", "priority", "sort", "drop", "reject", "protected", "forbidden") {
				continue
			}
			return true
		}
	}
	return false
}

func duplicateObjectiveClassifierAuthority(file, text string) bool {
	if strings.HasSuffix(file, "/internal/runtime/agent/read_only_objective_relevance.go") {
		return false
	}
	if !agentProductionSource(file) {
		return false
	}
	for _, token := range []string{"Objective(", "objectiveAsks", "ObjectiveIs", "QuestionIs", "ObjectiveApplies"} {
		for _, window := range deDriftFunctionWindows(text, token, 900) {
			if !regexp.MustCompile(`func\s+\w*(Objective|objectiveAsks|ObjectiveIs|QuestionIs|ObjectiveApplies)\w*\s*\(`).MatchString(window) {
				continue
			}
			if strings.Contains(window, "DiagnosticObjective") {
				continue
			}
			if !broadObjectiveClassifierWindow(window) {
				continue
			}
			if containsAny(window, "readOnlyClosureDecisionForResults", "DirectAnswerRelevance", "readOnlyAnswerabilityForResults", "readOnlyEvidencePacketAnswerableForObjective", "SourceBodyObjective", "EvidencePath", "DiagnosticContract", "Target") {
				continue
			}
			return true
		}
	}
	return false
}

func broadObjectiveClassifierWindow(window string) bool {
	broadTerms := []string{
		`"why"`, `"cause"`, `"bug"`, `"failure"`, `"failed"`, `"failing"`, `"regression"`,
		`"fix"`, `"broken"`, `"error"`, `"debugging"`, `"diagnose"`, `"investigate"`,
		`"what would it take"`, `"what needs to change"`, `"how would"`, `"how should"`,
	}
	return containsAny(window, broadTerms...) && containsAny(window, "containsAny", "objectiveWordSet", "strings.Contains", "HasPrefix")
}

func delegatedDiscoveryPolicyAccumulation(file, text string) bool {
	if strings.HasSuffix(file, "/internal/runtime/agent/runner_lane_policy.go") {
		return false
	}
	if !agentProductionSource(file) || !containsAny(text, "delegatedDiscovery", "delegatedReadOnlyDiscovery", "discovery_query") {
		return false
	}
	for _, token := range []string{"delegatedReadOnlyDiscovery", "delegatedDiscovery", "discovery_query"} {
		for _, window := range deDriftFunctionWindows(text, token, 1100) {
			normalizesEnvelope := containsAny(window, ".Purpose", ".Path", ".Pattern", "MaxDepth", "Named workspace scope", "security_review", "history_context", "architecture_boundary")
			classifiesObjective := containsAny(window, "containsAny", "strings.Contains", "HasPrefix", "MentionedWorkspacePaths")
			if normalizesEnvelope && classifiesObjective {
				return true
			}
		}
	}
	return false
}

func duplicateActionClassVocabulary(file, text string) bool {
	if !agentProductionSource(file) {
		return false
	}
	if !containsAny(text, `"per_edit_write"`, `"source_satisfied"`, `"generic_probe"`, `"final_answer"`, `"submit_plan"`) {
		return false
	}
	definesLocalActionConstants := regexp.MustCompile(`action[A-Z]\w*\s*=\s*"(read|search|submit_plan|source_satisfied|blocker|review_pass|per_edit_write|deliverable_write|verify|final_answer|prose_final|generic_probe|delegate)"`).MatchString(text)
	if definesLocalActionConstants {
		return true
	}
	for _, token := range []string{"ActionClass", "actionClass"} {
		for _, window := range deDriftFunctionWindows(text, token, 900) {
			if !regexp.MustCompile(`func\s+(?:\([^)]*\)\s*)?\w*(ActionClass|actionClass)\w*\s*\(`).MatchString(window) {
				continue
			}
			if regexp.MustCompile(`return\s+"(read|search|submit_plan|source_satisfied|blocker|review_pass|per_edit_write|deliverable_write|verify|final_answer|prose_final|generic_probe|delegate)"`).MatchString(window) {
				return true
			}
		}
	}
	return false
}

func deDriftFunctionWindow(text string, token string, maxLen int) string {
	idx := strings.Index(text, token)
	if idx < 0 {
		return ""
	}
	return deDriftFunctionWindowAt(text, idx, token, maxLen)
}

func deDriftFunctionWindowAt(text string, idx int, token string, maxLen int) string {
	start := strings.LastIndex(text[:idx], "func ")
	if start < 0 {
		start = strings.LastIndex(text[:idx], "const ")
	}
	if start < 0 {
		start = max(0, idx-160)
	}
	end := idx + maxLen
	if end > len(text) {
		end = len(text)
	}
	if nextFunc := strings.Index(text[idx+len(token):end], " func "); nextFunc >= 0 {
		end = idx + len(token) + nextFunc
	}
	return text[start:end]
}

func deDriftFunctionWindows(text string, token string, maxLen int) []string {
	var windows []string
	offset := 0
	for {
		idx := strings.Index(text[offset:], token)
		if idx < 0 {
			return windows
		}
		absolute := offset + idx
		window := deDriftFunctionWindowAt(text, absolute, token, maxLen)
		if regexp.MustCompile(`"(read|search|submit_plan|source_satisfied|blocker|review_pass|per_edit_write|deliverable_write|verify|final_answer|prose_final|generic_probe|delegate)"`).MatchString(window) {
			windows = append(windows, window)
		} else if window != "" {
			windows = append(windows, window)
		}
		offset = absolute + len(token)
	}
}

func agentProductionSource(file string) bool {
	file = filepath.ToSlash(file)
	return strings.Contains(file, "internal/runtime/agent/") && strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go")
}

func agentTestRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repo root not found from cwd")
		}
		dir = parent
	}
}
