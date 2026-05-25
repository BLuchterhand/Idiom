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

func TestTaskScenarioAssertionsIncludeContractReplay(t *testing.T) {
	task := SWETask{
		ID:     "contract-replay",
		Family: "plan_execute",
		Prompt: "Run contract replay fixture.",
		Scenario: Scenario{
			Name: "contract-replay",
			RequireContractReplay: &ContractReplayExpectation{
				RequirePacks:            []string{"completion/readiness"},
				RequireEnforcementModes: []string{"observe_only"},
				RequireAtoms:            []string{"gate_decision"},
			},
		},
	}
	assertions := taskScenarioAssertions(task)
	if !containsStringFold(assertions, "contract_replay") {
		t.Fatalf("expected contract_replay assertion, got %#v", assertions)
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
			{
				ID:     "reserved-env",
				Family: "direct_question",
				Prompt: "Explain.",
				Env: map[string]string{
					"IDIOM_SWE_LOG": "override.jsonl",
				},
				Scenario: Scenario{
					Name:           "reserved-env",
					PromptContains: []string{"Explain"},
				},
				Oracle: SWEOracle{
					RequireNoChangedFiles: true,
				},
			},
			{
				ID:         "bad-runner-mode",
				Family:     "implementation",
				Prompt:     "Edit.",
				RunnerMode: "surprise",
				Scenario: Scenario{
					Name:           "bad-runner-mode",
					PromptContains: []string{"Edit"},
				},
				Oracle: SWEOracle{
					RequireChangedFiles: []string{"target.txt"},
				},
			},
		},
	}
	findings := LintSWESuite(suite)
	joined := strings.Join(findings, "\n")
	for _, want := range []string{"read-only task must require no changed files", "direct implementation task must require changed files or approval", "tool-error task must bound errors", "negative-evidence task must require no changed files", "fixture path is invalid", "multi-turn task must require at least 2 user turns", "multi-turn task must require an assistant response for each user turn", "file content oracle path is invalid", "file content oracle for \"../README.md\" must assert required or forbidden text", "env key \"IDIOM_SWE_LOG\" is reserved by the live runner", "runner_mode \"surprise\" is invalid"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected lint finding %q, got %q", want, joined)
		}
	}
	if err := ValidateSWESuite(suite); err == nil {
		t.Fatal("expected suite validation to reject weak oracles")
	}
}

func TestValidSWERunnerModeAcceptsFeaturePlanner(t *testing.T) {
	if !validSWERunnerMode("feature-planner") {
		t.Fatal("feature-planner should be a valid SWE runner mode")
	}
	if !validSWERunnerMode("tui") {
		t.Fatal("tui should be a valid SWE runner mode")
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
	suite.Tasks[0].Fixture = `..\outside`
	if err := ValidateSWESuiteFixtures(suite, suitePath); err == nil {
		t.Fatal("expected backslash traversal fixture validation failure")
	}
}

func TestValidateSWESuiteFixturesChecksFixtureGitRefs(t *testing.T) {
	suitePath := filepath.Join(t.TempDir(), "suite.json")
	suite := SWESuite{
		Name: "git-ref-fixtures",
		Tasks: []SWETask{{
			ID:            "git-ref-task",
			Family:        "implementation",
			Prompt:        "Edit.",
			FixtureGitRef: "definitely-not-a-real-ref",
			Scenario: Scenario{
				Name:           "git-ref-task",
				PromptContains: []string{"Edit"},
			},
			Oracle: SWEOracle{
				RequireChangedFiles: []string{"target.txt"},
			},
		}},
	}
	if err := ValidateSWESuiteFixtures(suite, suitePath); err == nil {
		t.Fatal("expected unreachable fixture_git_ref validation failure")
	}

	suite.Tasks[0].FixtureGitRef = "-bad"
	if err := ValidateSWESuiteFixtures(suite, suitePath); err == nil {
		t.Fatal("expected unsafe fixture_git_ref validation failure")
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

	task.Oracle.RequireFileContent[0] = SWEFileContentExpectation{Path: `..\secret.md`, Contains: []string{"secret"}}
	report = EvaluateSWETask("docs.jsonl", records, task)
	if report.Passed || !strings.Contains(strings.Join(report.Findings, "\n"), "escapes workspace root") {
		t.Fatalf("expected unsafe file content path failure, got passed=%t findings=%#v", report.Passed, report.Findings)
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

func TestEvaluateSWETaskProductWorkflowBlockerDoesNotSatisfyImplementationAcceptance(t *testing.T) {
	task := SWETask{
		ID:     "product-workflow-coverage-gate-authority-reuse",
		Family: "plan_then_execute_planner",
		Scenario: Scenario{
			Name:                "product-workflow-coverage-gate-authority-reuse",
			RequireChangedFiles: []string{"cmd/idiom/product_workflow.go"},
			RequirePlanExecution: &PlanExecutionExpectation{
				RequireHandoffID: true,
			},
			RequireVerification: "passed",
		},
		Oracle: SWEOracle{
			RequireChangedFiles:       []string{"cmd/idiom/product_workflow.go"},
			RequireNoRunnerRejections: true,
			RequireExecutorHandoff:    true,
			RequireNoTimeout:          true,
		},
	}
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Can you implement the product workflow coverage gate feature?"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "<<QUESTION>>: exact source blocker"}},
		{Type: "turn_summary", Data: map[string]any{"rejected_actions": 0, "completion_gate_rejections": 0}},
		{Type: "idiom.live_contract.rollback", Data: map[string]any{"status": "clean", "changed_before_reset": []string{}, "changed_after_reset": []string{}}},
	}

	report := EvaluateSWETask("product-workflow.jsonl", records, task)
	if report.Passed {
		t.Fatal("typed blocker without executor handoff, changed files, or verification must not satisfy implementation acceptance")
	}
	joined := strings.Join(report.Findings, "\n")
	for _, want := range []string{"changed files do not include", "plan execution handoff id was required", "executor handoff was required", "verification did not pass", "blocker-only without allow_no_write"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected finding %q, got %q", want, joined)
		}
	}
	if strings.Contains(joined, "runner rejections were forbidden") {
		t.Fatalf("typed blocker fixture should fail implementation acceptance, not runner-rejection oracle: %q", joined)
	}
}

func TestEvaluateSWETaskSourceSatisfiedNeedsExplicitAllowanceForImplementationRequiredTask(t *testing.T) {
	task := SWETask{
		ID:     "source-satisfied-implementation",
		Family: "plan_then_execute_planner",
		Scenario: Scenario{
			Name:                "source-satisfied-implementation",
			RequireChangedFiles: []string{"internal/labels/labels.go"},
		},
		Oracle: SWEOracle{
			RequireChangedFiles: []string{"internal/labels/labels.go"},
		},
	}
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Decision: source-satisfied\ninternal/labels/labels.go already returns Save."}},
		{Type: "idiom.plan_execute.source_satisfied", Data: map[string]any{"source_satisfied_evidence_paths": []any{"internal/labels/labels.go"}}},
	}

	report := EvaluateSWETask("source-satisfied.jsonl", records, task)
	if report.Passed {
		t.Fatal("source-satisfied-only must not satisfy implementation-required task without explicit allowance")
	}
	joined := strings.Join(report.Findings, "\n")
	for _, want := range []string{"source-satisfied-only without allow_source_satisfied", "changed files do not include"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected finding %q, got %q", want, joined)
		}
	}
	if report.Metrics.SourceSatisfiedOutcomes == 0 {
		t.Fatalf("expected source-satisfied outcome metric, got %#v", report.Metrics)
	}
}

func TestEvaluateSWETaskAllowsExplicitSourceSatisfiedNoWriteFixture(t *testing.T) {
	task := SWETask{
		ID:     "source-satisfied-no-write",
		Family: "plan_then_execute_planner",
		Scenario: Scenario{
			Name: "source-satisfied-no-write",
			RequirePlanExecution: &PlanExecutionExpectation{
				RequireNoDrift: true,
			},
		},
		Oracle: SWEOracle{
			RequireNoChangedFiles:     true,
			AllowSourceSatisfied:      true,
			RequireNoTimeout:          true,
			RequireCleanRollback:      true,
			RequireNoRunnerRejections: true,
		},
	}
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Decision: source-satisfied\nNo edits needed."}},
		{Type: "idiom.plan_execute.source_satisfied", Data: map[string]any{"source_satisfied_evidence_paths": []any{"internal/labels/labels.go"}}},
		{Type: "turn_summary", Data: map[string]any{"rejected_actions": 0, "completion_gate_rejections": 0}},
		{Type: "idiom.live_contract.rollback", Data: map[string]any{
			"status":               "clean",
			"baseline_tree":        "abc",
			"post_reset_tree":      "abc",
			"changed_before_reset": []any{},
			"changed_after_reset":  []any{},
		}},
	}

	report := EvaluateSWETask("source-satisfied-no-write.jsonl", records, task)
	if !report.Passed {
		t.Fatalf("explicit source-satisfied no-write fixture should pass, findings: %v", report.Findings)
	}
}

func TestEvaluateSWETaskRequiresCleanRollback(t *testing.T) {
	task := SWETask{
		ID:       "rollback",
		Family:   "implementation",
		Scenario: Scenario{Name: "rollback"},
		Oracle:   SWEOracle{RequireCleanRollback: true},
	}
	records := []Record{
		{Type: "idiom.live_contract.rollback", Data: map[string]any{
			"status":               "dirty_after_reset",
			"baseline_tree":        "abc",
			"post_reset_tree":      "def",
			"changed_before_reset": []any{"internal/labels/labels.go"},
			"changed_after_reset":  []any{"internal/labels/labels.go"},
		}},
	}

	report := EvaluateSWETask("rollback.jsonl", records, task)
	if report.Passed {
		t.Fatal("expected dirty rollback to fail")
	}
	joined := strings.Join(report.Findings, "\n")
	if !strings.Contains(joined, "rollback status was not clean") {
		t.Fatalf("expected rollback finding, got %q", joined)
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

func TestEvaluateSWETaskRequiresNoRunnerRejections(t *testing.T) {
	task := SWETask{
		ID:       "no-rejections",
		Family:   "direct_question",
		Scenario: Scenario{Name: "no-rejections"},
		Oracle:   SWEOracle{RequireNoRunnerRejections: true},
	}
	records := []Record{{
		Type: "turn_summary",
		Data: map[string]any{
			"rejected_actions":           1,
			"completion_gate_rejections": 0,
		},
	}}

	report := EvaluateSWETask("rejected.jsonl", records, task)
	if report.Passed {
		t.Fatal("expected runner rejection to fail")
	}
	joined := strings.Join(report.Findings, "\n")
	if !strings.Contains(joined, "runner rejections were forbidden") {
		t.Fatalf("expected runner rejection finding, got %q", joined)
	}
	if !containsStringFold(taskOracleAssertions(task), "no_runner_rejections") {
		t.Fatalf("expected no_runner_rejections assertion")
	}
}

func TestEvaluateSWETaskRequiresTurnSummaryForNoRunnerRejections(t *testing.T) {
	task := SWETask{
		ID:       "no-rejections-summary",
		Family:   "direct_question",
		Scenario: Scenario{Name: "no-rejections-summary"},
		Oracle:   SWEOracle{RequireNoRunnerRejections: true},
	}

	report := EvaluateSWETask("missing-summary.jsonl", []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Can we shorten this?"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Yes."}},
	}, task)

	if report.Passed {
		t.Fatal("expected missing turn_summary to fail no-runner-rejections oracle")
	}
	joined := strings.Join(report.Findings, "\n")
	if !strings.Contains(joined, "no turn_summary was present") {
		t.Fatalf("expected missing turn_summary finding, got %q", joined)
	}
}

func TestEvaluateSWETaskRequiresNoRunnerRejectionsFromGateEvents(t *testing.T) {
	task := SWETask{
		ID:       "no-rejections-events",
		Family:   "direct_question",
		Scenario: Scenario{Name: "no-rejections-events"},
		Oracle:   SWEOracle{RequireNoRunnerRejections: true},
	}
	records := []Record{
		{
			Type: "run_event",
			Data: map[string]any{
				"role":    "autonomy_event",
				"content": `{"type":"gate_decision","gate":{"gate":"completion_gate","decision":"reject"}}`,
			},
		},
		{
			Type: "turn_summary",
			Data: map[string]any{"rejected_actions": 0, "completion_gate_rejections": 0},
		},
	}

	report := EvaluateSWETask("gate-rejected.jsonl", records, task)
	if report.Passed {
		t.Fatal("expected gate rejection event to fail no-runner-rejections oracle")
	}
	joined := strings.Join(report.Findings, "\n")
	if !strings.Contains(joined, "completion_gate_rejections=1") {
		t.Fatalf("expected completion gate rejection finding, got %q", joined)
	}
}

func TestEvaluateSWETaskContractReplayUsesAutonomyEvents(t *testing.T) {
	maxViolations := 0
	task := SWETask{
		ID:     "contract-replay-clean",
		Family: "plan_then_execute_planner",
		Scenario: Scenario{
			Name: "contract-replay-clean",
			RequireContractReplay: &ContractReplayExpectation{
				RequirePacks:            []string{"completion/readiness"},
				RequireEnforcementModes: []string{"observe_only"},
				RequireAtoms:            []string{"pack_seen", "gate_decision", "observed_fact"},
				MaxViolations:           &maxViolations,
			},
		},
	}
	records := []Record{
		{
			Type: "message",
			Data: map[string]any{
				"role": "autonomy_event",
				"content": `{
					"schema_version":"idiom.autonomy.event.v1",
					"event_id":"evt-compiled",
					"type":"contract_compiled",
					"effect":"observe_only",
					"occurred_at":"2026-05-24T12:00:00Z",
					"sequence":1,
					"contract_id":"contract-1",
					"lifecycle":{},
					"metadata":{"tags":["atom:contract_compiled"]}
				}`,
			},
		},
		{
			Type: "run_event",
			Data: map[string]any{
				"role": "autonomy_event",
				"content": `{
					"schema_version":"idiom.autonomy.event.v1",
					"event_id":"evt-gate",
					"type":"gate_decision",
					"effect":"observe_only",
					"occurred_at":"2026-05-24T12:00:01Z",
					"sequence":2,
					"contract_id":"contract-1",
					"gate":{
						"gate":"completion_gate",
						"decision":"allow",
						"pack_id":"completion/readiness",
						"enforcement_mode":"observe_only",
						"observed_facts":["status:decision=allow"]
					},
					"metadata":{"tags":["atom:gate_decision"]}
				}`,
			},
		},
	}

	report := EvaluateSWETask("contract-replay.jsonl", records, task)
	if !report.Passed {
		t.Fatalf("expected contract replay task to pass, got %#v", report.Findings)
	}
	if report.Metrics.ContractReplay == nil {
		t.Fatal("expected contract replay metrics")
	}
	if report.Metrics.ContractReplay.TotalEvents != 2 || !report.Metrics.ContractReplay.AllClear {
		t.Fatalf("unexpected contract replay report: %#v", report.Metrics.ContractReplay)
	}
	if report.Metrics.ContractReplay.Packs["completion/readiness"] != 1 {
		t.Fatalf("expected replay pack count, got %#v", report.Metrics.ContractReplay.Packs)
	}
}

func TestEvaluateSWETaskContractReplayFailsExpectations(t *testing.T) {
	maxViolations := 0
	task := SWETask{
		ID:     "contract-replay-violations",
		Family: "plan_then_execute_planner",
		Scenario: Scenario{
			Name: "contract-replay-violations",
			RequireContractReplay: &ContractReplayExpectation{
				RequirePacks:       []string{"completion/readiness"},
				RequireAtoms:       []string{"gate_decision"},
				MaxViolations:      &maxViolations,
				ForbidViolationIDs: []string{"contract_event_unmatched_contract_reference"},
			},
		},
	}
	records := []Record{{
		Type: "run_event",
		Data: map[string]any{
			"role": "autonomy_event",
			"content": `{
				"schema_version":"idiom.autonomy.event.v1",
				"event_id":"evt-unmatched-gate",
				"type":"gate_decision",
				"effect":"observe_only",
				"occurred_at":"2026-05-24T12:00:01Z",
				"sequence":2,
				"contract_id":"missing-contract",
				"gate":{
					"gate":"completion_gate",
					"decision":"allow",
					"pack_id":"completion/readiness",
					"enforcement_mode":"observe_only"
				}
			}`,
		},
	}}

	report := EvaluateSWETask("contract-replay-violation.jsonl", records, task)
	if report.Passed {
		t.Fatal("expected contract replay violation to fail")
	}
	joined := strings.Join(report.Findings, "\n")
	for _, want := range []string{"contract replay violations exceeded limit", "contract replay violation \"contract_event_unmatched_contract_reference\" was forbidden"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected finding %q, got %q", want, joined)
		}
	}
}

func TestEvaluateSWETaskContractReplayMaxViolationsIgnoresAuditDiagnostics(t *testing.T) {
	maxViolations := 0
	task := SWETask{
		ID:     "contract-replay-audit-diagnostic",
		Family: "plan_then_execute_planner",
		Scenario: Scenario{
			Name: "contract-replay-audit-diagnostic",
			RequireContractReplay: &ContractReplayExpectation{
				RequirePacks:  []string{"completion/readiness"},
				RequireAtoms:  []string{"gate_decision", "observed_fact"},
				MaxViolations: &maxViolations,
			},
		},
	}
	records := []Record{{
		Type: "run_event",
		Data: map[string]any{
			"role": "autonomy_event",
			"content": `{
				"schema_version":"idiom.autonomy.event.v1",
				"event_id":"evt-unmatched-gate",
				"type":"gate_decision",
				"effect":"observe_only",
				"occurred_at":"2026-05-24T12:00:01Z",
				"sequence":2,
				"contract_id":"missing-contract",
				"gate":{
					"gate":"completion_gate",
					"decision":"allow",
					"pack_id":"completion/readiness",
					"enforcement_mode":"observe_only",
					"observed_facts":["status:decision=allow"]
				}
			}`,
		},
	}}

	report := EvaluateSWETask("contract-replay-audit-diagnostic.jsonl", records, task)
	if !report.Passed {
		t.Fatalf("expected audit-only diagnostics not to count against replay max violations, got %#v", report.Findings)
	}
	if report.Metrics.ContractReplay == nil || len(report.Metrics.ContractReplay.Violations) == 0 {
		t.Fatalf("expected audit diagnostic to remain visible, got %#v", report.Metrics.ContractReplay)
	}
	if got := len(report.Metrics.ContractReplay.Replay.Violations); got != 0 {
		t.Fatalf("expected no deterministic replay violations, got %d", got)
	}
}

func TestEvaluateSWETaskWithoutAutonomyEventsKeepsReplayOptional(t *testing.T) {
	task := SWETask{
		ID:       "no-contract-replay",
		Family:   "direct_question",
		Scenario: Scenario{Name: "no-contract-replay"},
	}

	report := EvaluateSWETask("no-contract-replay.jsonl", []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Explain."}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Done."}},
	}, task)
	if !report.Passed {
		t.Fatalf("expected task without contract replay expectation to pass, got %#v", report.Findings)
	}
	if report.Metrics.ContractReplay != nil {
		t.Fatalf("expected no contract replay metrics without autonomy events, got %#v", report.Metrics.ContractReplay)
	}

	task.Scenario.RequireContractReplay = &ContractReplayExpectation{}
	report = EvaluateSWETask("no-contract-replay.jsonl", nil, task)
	if report.Passed || !strings.Contains(strings.Join(report.Findings, "\n"), "required contract replay was not observed") {
		t.Fatalf("expected missing contract replay finding, got passed=%t findings=%#v", report.Passed, report.Findings)
	}
}

func TestEvaluateSWETaskDistinguishesLowValueActionsFromRunnerRejections(t *testing.T) {
	maxLowValue := 0
	task := SWETask{
		ID:       "low-value",
		Family:   "plan_then_execute_planner",
		Scenario: Scenario{Name: "low-value"},
		Oracle: SWEOracle{
			RequireNoRunnerRejections: true,
			MaxLowValueActions:        &maxLowValue,
		},
	}
	records := []Record{
		{
			Type: "run_event",
			Data: map[string]any{
				"role":    "plan_then_execute_low_value_action",
				"content": `{"tool":"read_file","path":"internal/labels/labels.go","reason":"duplicate read"}`,
			},
		},
		{Type: "turn_summary", Data: map[string]any{"rejected_actions": 0, "completion_gate_rejections": 0}},
	}

	report := EvaluateSWETask("low-value.jsonl", records, task)
	if report.Passed {
		t.Fatal("expected low-value action limit to fail")
	}
	joined := strings.Join(report.Findings, "\n")
	if !strings.Contains(joined, "low-value actions exceeded") {
		t.Fatalf("expected low-value finding, got %q", joined)
	}
	if strings.Contains(joined, "runner rejections were forbidden") {
		t.Fatalf("low-value action must not be treated as hard runner rejection: %q", joined)
	}
	if report.Metrics.LowValueActions != 1 || report.Metrics.RejectedActions != 0 || report.Metrics.CompletionGateRejections != 0 {
		t.Fatalf("unexpected metrics: %#v", report.Metrics)
	}
}

func TestEvaluateSWETaskRequiresNoTimeout(t *testing.T) {
	task := SWETask{
		ID:       "timeout",
		Family:   "implementation",
		Scenario: Scenario{Name: "timeout"},
		Oracle:   SWEOracle{RequireNoTimeout: true},
	}
	records := []Record{
		{Type: "idiom.live_contract.timeout", Data: map[string]any{"timeout_seconds": 1, "exit_code": 143}},
		{Type: "turn_summary", Data: map[string]any{"rejected_actions": 0, "completion_gate_rejections": 0}},
	}

	report := EvaluateSWETask("timeout.jsonl", records, task)
	if report.Passed {
		t.Fatal("expected timeout oracle to fail")
	}
	joined := strings.Join(report.Findings, "\n")
	if !strings.Contains(joined, "timeouts were forbidden") {
		t.Fatalf("expected timeout finding, got %q", joined)
	}
	if strings.Contains(joined, "runner rejections were forbidden") {
		t.Fatalf("timeout must not be treated as runner rejection: %q", joined)
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
			{
				ID:       "backslash-traversal",
				Family:   "direct_question",
				Prompt:   "explain.",
				Scenario: Scenario{Name: "backslash-traversal", PromptContains: []string{"explain"}},
				Oracle: SWEOracle{
					Groundedness: &SWEGroundednessOracle{MinScore: 0.5, ContextFiles: []string{`..\outside.md`}},
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
	if !strings.Contains(joined, "backslash-traversal") {
		t.Fatalf("expected backslash-traversal lint finding, got %v", findings)
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
