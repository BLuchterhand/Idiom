package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadRepresentativeSWESuiteCoversFrontierFamilies(t *testing.T) {
	suitePath := filepath.Join("..", "..", "testdata", "swe", "representative-suite.json")
	suite, err := LoadSWESuite(suitePath)
	if err != nil {
		t.Fatalf("LoadSWESuite: %v", err)
	}
	coverage := AnalyzeSWESuiteCoverage(suite)
	if !coverage.Passed {
		t.Fatalf("representative suite coverage failed: %v", coverage.Findings)
	}
	if findings := LintSWESuite(suite); len(findings) > 0 {
		t.Fatalf("representative suite lint failed: %v", findings)
	}
	requiredFamilies := []string{
		"direct_question",
		"debug_logs",
		"verification_only",
		"locate",
		"locate_then_implement",
		"explore_then_implement",
		"investigate_then_implement",
		"review_then_implement",
		"security_review",
		"performance_review",
		"architecture",
		"architecture_then_implement",
		"implementation",
		"test_authoring",
		"config_edit",
		"script_edit",
		"docs_edit",
		"prompt_edit_security",
		"failure_injection",
		"multi_turn_state",
	}
	for _, family := range requiredFamilies {
		if !suiteHasFamily(suite, family) {
			t.Fatalf("representative suite missing family %q", family)
		}
	}
	for _, task := range suite.Tasks {
		// RequireRoutePlan / RequirePhases used to be mandatory, but
		// the codebase deliberately removed the route_plan architecture
		// in favor of mode-based routing. Scenarios that still assert
		// those fields would never match, so the suite no longer
		// requires them — check for any meaningful assertion instead.
		hasAssertion := task.Scenario.RequireRoutePlan != nil ||
			len(task.Scenario.RequirePhases) > 0 ||
			len(task.Scenario.PromptContains) > 0 ||
			len(task.Scenario.FinalAssistantContains) > 0 ||
			len(task.Scenario.RequireTools) > 0
		if !hasAssertion {
			t.Fatalf("task %q scenario has no observable assertion; add one", task.ID)
		}
		if len(task.Turns) > 0 && !task.Scenario.RequireAssistantForEachUser {
			t.Fatalf("multi-turn task %q should require an assistant response for each user turn", task.ID)
		}
		if strings.TrimSpace(task.Fixture) != "" {
			fixturePath := filepath.Join(filepath.Dir(suitePath), task.Fixture)
			if _, err := filepath.Abs(fixturePath); err != nil {
				t.Fatalf("task %q fixture path is invalid: %v", task.ID, err)
			}
			if !pathExists(fixturePath) {
				t.Fatalf("task %q fixture does not exist: %s", task.ID, fixturePath)
			}
		}
	}
}

func TestAnalyzeSWESuiteCoverageReportsMissingContractItems(t *testing.T) {
	minTasks := 2
	suite := SWESuite{
		Name: "coverage",
		Coverage: SWECoverageContract{
			MinTasks:                   &minTasks,
			RequiredFamilies:           []string{"implementation", "security_review"},
			RequiredTags:               []string{"implement", "adversarial"},
			RequiredFixtures:           []string{"fixtures/adversarial"},
			RequiredTaskShapes:         []string{"fixture_backed", "expected_failure"},
			RequiredInitialLanes:       []string{"review"},
			RequiredRouteReasons:       []string{"review_request"},
			RequiredContracts:          []string{"review_findings"},
			RequiredThoroughness:       []string{"thorough"},
			RequiredVerification:       []string{"failed"},
			RequiredScenarioAssertions: []string{"tools"},
			RequiredOracleAssertions:   []string{"no_shell"},
			RequiredReadinessActions:   []string{"answer"},
			RequiredDeepeningActions:   []string{"retry_with_evidence"},
			RequiredFinalCriticActions: []string{"accept"},
			RequiredEffortLevels:       []string{"thorough"},
			RequiredLongHorizonActions: []string{"persist"},
			RequiredLongHorizonKinds:   []string{"partial_repair"},
			RequiredNegativeEvidence:   []string{"no_match"},
			RequiredTools:              []string{"shell"},
			RequiredForbiddenTools:     []string{"edit_file"},
			RequiredFileSurfaces:       []string{"go"},
			RequiredEvidenceSurfaces:   []string{"go"},
			MinTasksPerFamily: map[string]int{
				"implementation": 2,
			},
		},
		Tasks: []SWETask{{
			ID:     "edit",
			Family: "implementation",
			Prompt: "Edit code",
			Tags:   []string{"implement"},
			Scenario: Scenario{
				Name:           "edit",
				PromptContains: []string{"Edit code"},
			},
		}},
	}
	coverage := AnalyzeSWESuiteCoverage(suite)
	if coverage.Passed {
		t.Fatal("expected coverage contract to fail")
	}
	joined := strings.Join(coverage.Findings, "\n")
	for _, want := range []string{
		"task count below coverage minimum",
		"required family \"security_review\" is missing",
		"required tag \"adversarial\" is missing",
		"required fixture \"fixtures/adversarial\" is missing",
		"required task shape \"fixture_backed\" is missing",
		"required task shape \"expected_failure\" is missing",
		"required initial lane \"review\" is missing",
		"required route reason \"review_request\" is missing",
		"required answer contract \"review_findings\" is missing",
		"required thoroughness \"thorough\" is missing",
		"required verification state \"failed\" is missing",
		"required scenario assertion \"tools\" is missing",
		"required oracle assertion \"no_shell\" is missing",
		"required readiness action \"answer\" is missing",
		"required deepening action \"retry_with_evidence\" is missing",
		"required final critic action \"accept\" is missing",
		"required effort level \"thorough\" is missing",
		"required long-horizon action \"persist\" is missing",
		"required long-horizon kind \"partial_repair\" is missing",
		"required negative-evidence outcome \"no_match\" is missing",
		"required tool \"shell\" is missing",
		"required forbidden tool \"edit_file\" is missing",
		"required file surface \"go\" is missing",
		"required evidence surface \"go\" is missing",
		"family \"implementation\" below coverage minimum",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected finding %q, got %q", want, joined)
		}
	}
	if err := ValidateSWESuite(suite); err == nil {
		t.Fatal("expected suite validation to reject failed coverage contract")
	}
}

func TestEvaluateSWESuiteDefinitionDoesNotRequireLogs(t *testing.T) {
	maxTools := 2
	suite := SWESuite{
		Name: "definition",
		Tasks: []SWETask{{
			ID:     "direct",
			Family: "direct_question",
			Prompt: "Explain this.",
			Tags:   []string{"read-only"},
			Scenario: Scenario{
				Name:           "direct",
				PromptContains: []string{"Explain"},
			},
			Oracle: SWEOracle{
				RequireNoChangedFiles: true,
				MaxToolRequests:       &maxTools,
			},
		}},
	}
	report := EvaluateSWESuiteDefinition(suite)
	if !report.Passed {
		t.Fatalf("expected definition-only report to pass, got %v", report.Findings)
	}
	if report.Summary.TotalTasks != 1 || !report.Summary.CoveragePassed || !report.Summary.LintPassed {
		t.Fatalf("unexpected definition summary: %#v", report.Summary)
	}
	if len(report.Tasks) != 0 {
		t.Fatalf("definition-only report should not require task logs: %#v", report.Tasks)
	}
}

func TestTaskScenarioAssertionsIncludeDiscoveryBalance(t *testing.T) {
	maxHeavy := 1
	task := SWETask{
		ID:     "discovery",
		Family: "security_review",
		Prompt: "Review security discovery usage.",
		Scenario: Scenario{
			Name:                      "discovery",
			RequireDiscoveryTools:     []string{"semgrep_scan"},
			RequireDiscoveryClasses:   []string{"security_scan"},
			MaxHeavyDiscoveryRequests: &maxHeavy,
		},
	}
	assertions := taskScenarioAssertions(task)
	for _, want := range []string{"discovery_tools", "discovery_classes", "heavy_discovery_budget"} {
		if !containsStringFold(assertions, want) {
			t.Fatalf("expected discovery assertion %q in %#v", want, assertions)
		}
	}
}

func TestTaskScenarioAssertionsIncludePlanExecution(t *testing.T) {
	task := SWETask{
		ID:     "plan-execution",
		Family: "plan_execute",
		Prompt: "Run a plan-execute drift fixture.",
		Scenario: Scenario{
			Name: "plan-execution",
			RequirePlanExecution: &PlanExecutionExpectation{
				RequireHandoffID:       true,
				RequireApprovalScopeID: true,
				RequireNoDrift:         true,
			},
		},
	}
	assertions := taskScenarioAssertions(task)
	if !containsStringFold(assertions, "plan_execution") {
		t.Fatalf("expected plan_execution assertion, got %#v", assertions)
	}
}

func TestComprehensionMemorySuiteCoversContextTooling(t *testing.T) {
	suite := ComprehensionMemorySuite()
	report := EvaluateSWESuiteDefinition(suite)
	if !report.Passed {
		t.Fatalf("expected comprehension memory suite definition to pass, findings=%#v lint=%#v coverage=%#v", report.Findings, report.LintFindings, report.Coverage)
	}
	for _, tool := range []string{"context_status", "context_query", "context_zoom", "context_pack", "context_trace_pack", "discovery_query"} {
		if report.Coverage.Tools[tool] == 0 {
			t.Fatalf("expected comprehension suite coverage for tool %q, got %#v", tool, report.Coverage.Tools)
		}
	}
	for _, family := range []string{"orientation", "localization", "planning", "failing_test"} {
		if report.Coverage.Families[family] == 0 {
			t.Fatalf("expected comprehension suite family %q, got %#v", family, report.Coverage.Families)
		}
	}
	for _, assertion := range []string{"tools", "discovery_classes", "broad_tool_budget", "heavy_discovery_budget"} {
		if report.Coverage.ScenarioAssertions[assertion] == 0 {
			t.Fatalf("expected scenario assertion %q, got %#v", assertion, report.Coverage.ScenarioAssertions)
		}
	}
}

func TestEvaluateSWESuiteDefinitionWithFixturesReportsMissingFixture(t *testing.T) {
	dir := t.TempDir()
	suite := SWESuite{
		Name: "definition-fixtures",
		Tasks: []SWETask{{
			ID:      "fixture-task",
			Family:  "security_review",
			Prompt:  "Audit fixture.",
			Fixture: "fixtures/missing",
			Scenario: Scenario{
				Name:           "fixture-task",
				PromptContains: []string{"Audit"},
			},
			Oracle: SWEOracle{
				RequireNoChangedFiles: true,
			},
		}},
	}
	report := EvaluateSWESuiteDefinitionWithFixtures(suite, filepath.Join(dir, "suite.json"))
	if report.Passed {
		t.Fatal("expected suite definition with missing fixture to fail")
	}
	joined := strings.Join(report.Findings, "\n")
	if !strings.Contains(joined, "fixture missing") {
		t.Fatalf("expected fixture missing finding, got %q", joined)
	}
}

func TestLintSWESuiteReportsWeakOracles(t *testing.T) {
	suite := SWESuite{
		Name: "lint",
		Tasks: []SWETask{
			{
				ID:     "read-only-without-no-change",
				Family: "architecture",
				Prompt: "Analyze this.",
				Tags:   []string{"read-only"},
				Scenario: Scenario{
					Name:           "read-only-without-no-change",
					PromptContains: []string{"Analyze"},
				},
			},
			{
				ID:     "direct-edit-without-target",
				Family: "implementation",
				Prompt: "Edit this.",
				Tags:   []string{"implement"},
				Scenario: Scenario{
					Name:           "direct-edit-without-target",
					PromptContains: []string{"Edit"},
					RequireRoutePlan: &RoutePlanExpectation{
						InitialLane: "implement",
					},
				},
			},
			{
				ID:     "tool-error-unbounded",
				Family: "failure_injection",
				Prompt: "Recover from a tool error.",
				Tags:   []string{"tool-error"},
				Scenario: Scenario{
					Name:           "tool-error-unbounded",
					PromptContains: []string{"tool error"},
				},
			},
			{
				ID:     "negative-evidence-weak",
				Family: "locate",
				Prompt: "Find a nonexistent target.",
				Tags:   []string{"negative-evidence", "no-match"},
				Scenario: Scenario{
					Name:           "negative-evidence-weak",
					PromptContains: []string{"nonexistent"},
				},
			},
			{
				ID:      "fixture-escape",
				Family:  "security_review",
				Prompt:  "Audit fixture.",
				Fixture: "../outside",
				Tags:    []string{"security"},
				Scenario: Scenario{
					Name:           "fixture-escape",
					PromptContains: []string{"Audit"},
				},
				Oracle: SWEOracle{
					RequireNoChangedFiles: true,
				},
			},
			{
				ID:     "multi-turn-no-assistant-contract",
				Family: "multi_turn_state",
				Prompt: "Continue.",
				Turns:  []string{"first", "second"},
				Scenario: Scenario{
					Name:           "multi-turn-no-assistant-contract",
					PromptContains: []string{"second"},
				},
				Oracle: SWEOracle{
					RequireNoChangedFiles: true,
				},
			},
			{
				ID:     "file-content-escape",
				Family: "docs_edit",
				Prompt: "Update docs.",
				Scenario: Scenario{
					Name:           "file-content-escape",
					PromptContains: []string{"Update"},
				},
				Oracle: SWEOracle{
					RequireFileContent: []SWEFileContentExpectation{{
						Path: "../README.md",
					}},
				},
			},
		},
	}
	findings := LintSWESuite(suite)
	joined := strings.Join(findings, "\n")
	for _, want := range []string{"read-only task must require no changed files", "direct implementation task must require changed files or approval", "tool-error task must bound errors", "negative-evidence task must require no changed files", "fixture path is invalid", "multi-turn task must require at least 2 user turns", "multi-turn task must require an assistant response for each user turn", "file content oracle path is invalid", "file content oracle for \"../README.md\" must assert required or forbidden text"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected lint finding %q, got %q", want, joined)
		}
	}
	if err := ValidateSWESuite(suite); err == nil {
		t.Fatal("expected suite validation to reject weak oracles")
	}
}

func TestValidateSWESuiteFixturesChecksRelativeExistingDirectories(t *testing.T) {
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	fixtureDir := filepath.Join(dir, "fixtures", "repo")
	if err := os.MkdirAll(fixtureDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	suite := SWESuite{
		Name: "fixtures",
		Tasks: []SWETask{{
			ID:      "fixture-task",
			Family:  "security_review",
			Prompt:  "Audit fixture.",
			Fixture: "fixtures/repo",
			Scenario: Scenario{
				Name:           "fixture-task",
				PromptContains: []string{"Audit"},
			},
		}},
	}
	if err := ValidateSWESuiteFixtures(suite, suitePath); err != nil {
		t.Fatalf("expected fixture validation to pass: %v", err)
	}
	suite.Tasks[0].Fixture = "fixtures/missing"
	if err := ValidateSWESuiteFixtures(suite, suitePath); err == nil {
		t.Fatal("expected missing fixture validation failure")
	}
	suite.Tasks[0].Fixture = "/tmp/absolute"
	if err := ValidateSWESuiteFixtures(suite, suitePath); err == nil {
		t.Fatal("expected absolute fixture validation failure")
	}
}

func TestEvaluateSWETaskAppliesOracle(t *testing.T) {
	maxTools := 2
	task := SWETask{
		ID:     "direct-debuglog-question",
		Family: "direct_question",
		Prompt: "What does the debug log redacted mode do?",
		Scenario: Scenario{
			Name:           "direct-debuglog-question",
			PromptContains: []string{"debug log redacted mode"},
			RequireRoutePlan: &RoutePlanExpectation{
				InitialLane: "main",
				Reason:      "direct_conversation",
				Contract:    "direct_answer",
			},
			RequireVerification: "none",
		},
		Oracle: SWEOracle{
			RequireNoChangedFiles: true,
			RequireNoApproval:     true,
			MaxToolRequests:       &maxTools,
		},
	}
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "What does the debug log redacted mode do?"}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=main reason=direct_conversation contract=direct_answer"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "It masks sensitive prompt and tool payload content."}},
	}
	report := EvaluateSWETask("direct.jsonl", records, task)
	if !report.Passed {
		t.Fatalf("expected SWE task to pass, got %#v", report.Findings)
	}
}

func TestEvaluateSWETaskAppliesFileContentOracle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("debug logs are searchable\n"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	task := SWETask{
		ID:     "docs-edit",
		Family: "docs_edit",
		Prompt: "Update README.md.",
		Scenario: Scenario{
			Name:           "docs-edit",
			PromptContains: []string{"Update README"},
		},
		Oracle: SWEOracle{
			RequireFileContent: []SWEFileContentExpectation{{
				Path:           "README.md",
				Contains:       []string{"debug logs are searchable"},
				ForbidContains: []string{"TODO"},
			}},
		},
	}
	records := []Record{
		{Type: "session_start", Data: map[string]any{"workspace_root": root}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "Update README.md."}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Changed files:\n- README.md"}},
	}
	report := EvaluateSWETask("docs.jsonl", records, task)
	if !report.Passed {
		t.Fatalf("expected file content oracle to pass, got %#v", report.Findings)
	}

	task.Oracle.RequireFileContent[0].Contains = []string{"missing text"}
	report = EvaluateSWETask("docs.jsonl", records, task)
	if report.Passed || !strings.Contains(strings.Join(report.Findings, "\n"), "does not contain required text") {
		t.Fatalf("expected file content failure, got passed=%t findings=%#v", report.Passed, report.Findings)
	}
}

func TestEvaluateSWETaskReportsOracleFailures(t *testing.T) {
	maxTools := 0
	maxDurationMillis := int64(1)
	task := SWETask{
		ID:     "security-audit-prompt-injection",
		Family: "security_review",
		Prompt: "Audit this repo for prompt injection or command execution vulnerabilities without changing files.",
		Scenario: Scenario{
			Name:           "security-audit-prompt-injection",
			PromptContains: []string{"prompt injection"},
			RequireRoutePlan: &RoutePlanExpectation{
				InitialLane: "review",
				Reason:      "review_request",
				Contract:    "review_findings",
			},
		},
		Oracle: SWEOracle{
			RequireNoChangedFiles: true,
			RequireNoShell:        true,
			MaxToolRequests:       &maxTools,
			MaxDurationMillis:     &maxDurationMillis,
		},
	}
	records := []Record{
		{Type: "message", Data: map[string]any{"ts": "2026-04-18T12:00:00Z", "role": "user", "content": "Audit this repo for prompt injection or command execution vulnerabilities without changing files."}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=review reason=review_request contract=review_findings"}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "shell"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "shell", "summary": "ran command"}},
		{Type: "message", Data: map[string]any{"ts": "2026-04-18T12:00:02Z", "role": "assistant", "content": "Changed files:\n- prompts/base.md"}},
	}
	report := EvaluateSWETask("security.jsonl", records, task)
	if report.Passed {
		t.Fatalf("expected SWE task to fail")
	}
	joined := strings.Join(report.Findings, "\n")
	for _, want := range []string{"changed files were forbidden", "shell use was forbidden", "tool requests exceeded", "duration exceeded"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected finding %q, got %q", want, joined)
		}
	}
}

func TestEvaluateSWETaskGroundednessBoostsContextFiles(t *testing.T) {
	root := t.TempDir()
	contextPath := "internal/runner.go"
	full := filepath.Join(root, contextPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte("the runner enforces request budget through ledger snapshot"), 0o644); err != nil {
		t.Fatalf("write context: %v", err)
	}
	task := SWETask{
		ID:       "grounded-task",
		Family:   "direct_question",
		Prompt:   "Explain runner budget.",
		Scenario: Scenario{Name: "grounded-task", PromptContains: []string{"runner"}},
		Oracle: SWEOracle{
			Groundedness: &SWEGroundednessOracle{
				MinScore:     0.5,
				ContextFiles: []string{contextPath},
			},
		},
	}
	records := []Record{
		{Type: "session_start", Data: map[string]any{"workspace_root": root}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "Explain runner budget."}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "the runner enforces request budget through ledger snapshot"}},
	}
	report := EvaluateSWETask("grounded.jsonl", records, task)
	if report.LearningUpdate == nil {
		t.Fatalf("expected learning update record, got nil; findings=%#v", report.Findings)
	}
	if report.LearningUpdate.Action != "boost" {
		t.Fatalf("expected boost action, got %q (score=%v)", report.LearningUpdate.Action, report.LearningUpdate.Score)
	}
	if !report.LearningUpdate.Applied {
		t.Fatalf("expected applied=true for boost band")
	}
	if report.LearningUpdate.EventID == "" || report.LearningUpdate.State != "accepted" {
		t.Fatalf("expected accepted learning event, got %#v", report.LearningUpdate)
	}
	weightsPath := filepath.Join(root, ".idiom", "ranker", "weights.json")
	if _, err := os.Stat(weightsPath); err != nil {
		t.Fatalf("weights.json missing: %v", err)
	}
	logPath := filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("learning log missing: %v", err)
	}
	if !strings.Contains(string(data), "\"source\":\"eval\"") ||
		!strings.Contains(string(data), "\"action\":\"boost\"") ||
		!strings.Contains(string(data), "\"state\":\"accepted\"") ||
		!strings.Contains(string(data), "\"before_weights\"") ||
		!strings.Contains(string(data), "\"rollback\"") {
		t.Fatalf("learning log missing expected entry: %s", string(data))
	}
}

func TestEvaluateSWETaskGroundednessPenalizesLowScore(t *testing.T) {
	root := t.TempDir()
	contextPath := "spec.md"
	if err := os.WriteFile(filepath.Join(root, contextPath), []byte("workspace ranker hub demotion penalty multipliers"), 0o644); err != nil {
		t.Fatalf("write context: %v", err)
	}
	task := SWETask{
		ID:       "ungrounded",
		Family:   "direct_question",
		Prompt:   "explain.",
		Scenario: Scenario{Name: "ungrounded", PromptContains: []string{"explain"}},
		Oracle: SWEOracle{
			Groundedness: &SWEGroundednessOracle{MinScore: 0.0, ContextFiles: []string{contextPath}},
		},
	}
	records := []Record{
		{Type: "session_start", Data: map[string]any{"workspace_root": root}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "explain."}},
		// Final assistant text has zero overlap with the context tokens.
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "completely unrelated commentary about cats and birds"}},
	}
	report := EvaluateSWETask("ungrounded.jsonl", records, task)
	if report.LearningUpdate == nil {
		t.Fatalf("expected learning update, got nil")
	}
	if report.LearningUpdate.Action != "penalize" {
		t.Fatalf("expected penalize action, got %q (score=%v)", report.LearningUpdate.Action, report.LearningUpdate.Score)
	}
}

func TestEvaluateSWETaskGroundednessLearningHonorsDisable(t *testing.T) {
	t.Setenv("IDIOM_RANKER_LEARNING", "0")
	root := t.TempDir()
	contextPath := "spec.md"
	if err := os.WriteFile(filepath.Join(root, contextPath), []byte("runner budget ledger snapshot"), 0o644); err != nil {
		t.Fatalf("write context: %v", err)
	}
	task := SWETask{
		ID:       "learning-disabled",
		Family:   "direct_question",
		Prompt:   "Explain runner budget.",
		Scenario: Scenario{Name: "learning-disabled", PromptContains: []string{"runner"}},
		Oracle: SWEOracle{
			Groundedness: &SWEGroundednessOracle{MinScore: 0.0, ContextFiles: []string{contextPath}},
		},
	}
	records := []Record{
		{Type: "session_start", Data: map[string]any{"workspace_root": root}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "Explain runner budget."}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "runner budget ledger snapshot"}},
	}
	report := EvaluateSWETask("disabled.jsonl", records, task)
	if report.LearningUpdate == nil {
		t.Fatalf("expected learning update")
	}
	if report.LearningUpdate.Applied || report.LearningUpdate.State != "rejected" || report.LearningUpdate.Reason != "learning_disabled" {
		t.Fatalf("disabled learning should be rejected without applying: %#v", report.LearningUpdate)
	}
	if _, err := os.Stat(filepath.Join(root, ".idiom", "ranker", "weights.json")); !os.IsNotExist(err) {
		t.Fatalf("disabled learning should not create weights.json, err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	if err != nil {
		t.Fatalf("learning log missing: %v", err)
	}
	if !strings.Contains(string(data), "\"state\":\"rejected\"") || !strings.Contains(string(data), "\"reason\":\"learning_disabled\"") {
		t.Fatalf("learning log missing disabled rejection: %s", string(data))
	}
}

func TestLintSWESuiteFlagsGroundednessConfigErrors(t *testing.T) {
	suite := SWESuite{
		Name: "groundedness-lint",
		Tasks: []SWETask{
			{
				ID:       "no-context",
				Family:   "direct_question",
				Prompt:   "explain.",
				Scenario: Scenario{Name: "no-context", PromptContains: []string{"explain"}},
				Oracle: SWEOracle{
					Groundedness: &SWEGroundednessOracle{MinScore: 0.5}, // missing context
				},
			},
			{
				ID:       "out-of-range",
				Family:   "direct_question",
				Prompt:   "explain.",
				Scenario: Scenario{Name: "out-of-range", PromptContains: []string{"explain"}},
				Oracle: SWEOracle{
					Groundedness: &SWEGroundednessOracle{MinScore: 1.5, ContextFragments: []string{"frag"}},
				},
			},
			{
				ID:       "absolute-path",
				Family:   "direct_question",
				Prompt:   "explain.",
				Scenario: Scenario{Name: "absolute-path", PromptContains: []string{"explain"}},
				Oracle: SWEOracle{
					Groundedness: &SWEGroundednessOracle{MinScore: 0.5, ContextFiles: []string{"/absolute"}},
				},
			},
		},
	}
	findings := LintSWESuite(suite)
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "no-context") || !strings.Contains(joined, "context_files or context_fragments") {
		t.Fatalf("expected no-context lint finding, got %v", findings)
	}
	if !strings.Contains(joined, "out-of-range") || !strings.Contains(joined, "must be in [0, 1]") {
		t.Fatalf("expected out-of-range lint finding, got %v", findings)
	}
	if !strings.Contains(joined, "absolute-path") {
		t.Fatalf("expected absolute-path lint finding, got %v", findings)
	}
}

func TestTaskOracleAssertionsCoversNewOracles(t *testing.T) {
	task := SWETask{
		Oracle: SWEOracle{
			Groundedness:         &SWEGroundednessOracle{MinScore: 0.5, ContextFragments: []string{"x"}},
			ForbidGenericPhrases: true,
		},
	}
	got := taskOracleAssertions(task)
	if !containsStr(got, "groundedness") {
		t.Fatalf("expected groundedness in assertions, got %v", got)
	}
	if !containsStr(got, "forbid_generic_phrases") {
		t.Fatalf("expected forbid_generic_phrases in assertions, got %v", got)
	}
}

func containsStr(set []string, want string) bool {
	for _, v := range set {
		if v == want {
			return true
		}
	}
	return false
}

func TestEvaluateSWESuiteReportsMissingLogs(t *testing.T) {
	suite := SWESuite{
		Name: "small",
		Tasks: []SWETask{{
			ID:     "missing",
			Family: "direct_question",
			Prompt: "What is this?",
			Scenario: Scenario{
				Name:           "missing",
				PromptContains: []string{"What is this"},
			},
		}},
	}
	report := EvaluateSWESuite(suite, nil, nil)
	if report.Passed {
		t.Fatal("expected missing task log to fail suite")
	}
	if !report.Coverage.Passed {
		t.Fatalf("coverage should pass when no contract is set: %#v", report.Coverage)
	}
	if report.Summary.FailedTasks != 1 || report.Summary.MissingLogs != 1 {
		t.Fatalf("unexpected missing-log summary: %#v", report.Summary)
	}
	if bucket := report.Summary.ByFamily["direct_question"]; bucket.Total != 1 || bucket.Failed != 1 {
		t.Fatalf("unexpected family bucket: %#v", bucket)
	}
	if bucket := report.Summary.ByTaskShape["read_only"]; bucket.Total != 0 || bucket.Failed != 0 {
		t.Fatalf("unexpected task-shape bucket for untagged task: %#v", bucket)
	}
	if len(report.Tasks) != 1 || report.Tasks[0].Passed {
		t.Fatalf("unexpected suite report: %#v", report)
	}
}

func TestEvaluateSWESuiteReportsLoadErrors(t *testing.T) {
	suite := SWESuite{
		Name: "small",
		Tasks: []SWETask{{
			ID:     "bad-log",
			Family: "direct_question",
			Prompt: "What is this?",
			Scenario: Scenario{
				Name:           "bad-log",
				PromptContains: []string{"What is this"},
			},
		}},
	}
	report := EvaluateSWESuiteWithLoadErrors(
		suite,
		nil,
		map[string]string{"bad-log": "bad-log.jsonl"},
		map[string]error{"bad-log": errSynthetic("decode log record: invalid character")},
	)
	if report.Passed {
		t.Fatal("expected load error to fail suite")
	}
	joined := strings.Join(report.Findings, "\n")
	if !strings.Contains(joined, "task log could not be loaded") {
		t.Fatalf("expected load error finding, got %q", joined)
	}
	if report.Summary.LoadErrors != 1 || report.Summary.FailedTasks != 1 {
		t.Fatalf("unexpected load-error summary: %#v", report.Summary)
	}
}

func suiteHasFamily(suite SWESuite, family string) bool {
	for _, task := range suite.Tasks {
		if strings.EqualFold(task.Family, family) {
			return true
		}
	}
	return false
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type errSynthetic string

func (err errSynthetic) Error() string {
	return string(err)
}
