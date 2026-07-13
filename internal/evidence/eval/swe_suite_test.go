package eval

import (
	"fmt"
	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var _ = ginkgo.It("read representative swe suite covers frontier families", func() {
	suitePath := evalTestdataPath("swe", "representative-suite.json")

	suite, err := ReadSWESuite(suitePath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ReadSWESuite: %v", err))

	coverage := AnalyzeSWESuiteCoverage(suite)
	gomega.Expect(coverage.Passed).To(gomega.BeTrue(), fmt.Sprintf("representative suite coverage failed: %v", coverage.Findings))

	{
		findings := LintSWESuite(suite)
		gomega.Expect(findings).To(gomega.BeEmpty(), fmt.Sprintf("representative suite lint failed: %v", findings))
	}
	gomega.Expect(ValidateSWESuiteFixtures(suite, suitePath)).To(gomega.Succeed(), "validate representative suite fixtures")

	requiredFamilies := []string{
		"direct_question",
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
		gomega.Expect(suiteHasFamily(suite, family)).To(gomega.BeTrue(), fmt.Sprintf("representative suite missing family %q", family))
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
			strings.TrimSpace(task.Scenario.PromptSHA256) != "" ||
			len(task.Scenario.PromptHistoryContains) > 0 ||
			len(task.Scenario.FinalAssistantContains) > 0 ||
			len(task.Scenario.RequireTools) > 0
		gomega.Expect(hasAssertion).To(gomega.BeTrue(), fmt.Sprintf("task %q scenario has no observable assertion; add one", task.ID))

		if len(task.Turns) > 0 {
			gomega.Expect(task.Scenario.RequireAssistantForEachUser).To(gomega.BeTrue(), fmt.Sprintf("multi-turn task %q should require an assistant response for each user turn", task.ID))
		}

		if strings.TrimSpace(task.Fixture) != "" {
			fixturePath := filepath.Join(filepath.Dir(suitePath), task.Fixture)
			{
				_, err := filepath.Abs(fixturePath)
				gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("task %q fixture path is invalid: %v", task.ID, err))
			}

			gomega.Expect(pathExists(fixturePath)).To(gomega.BeTrue(), fmt.Sprintf("task %q fixture does not exist: %s", task.ID, fixturePath))
		}
	}
})
var _ = ginkgo.It("accepts a privacy-safe prompt digest and rejects malformed digests", func() {
	validDigest := "sha256:" + strings.Repeat("a", 64)
	minTasks := 1
	base := SWESuite{
		Name:    "prompt-digest",
		Version: 1,
		Coverage: SWECoverageContract{
			MinTasks: &minTasks,
		},
		Tasks: []SWETask{{
			ID:         "redacted-prompt",
			Family:     "feature_planner",
			RunnerMode: "feature-planner",
			Prompt:     "sensitive feature request",
			Scenario: Scenario{
				Name:         "redacted-prompt",
				PromptSHA256: validDigest,
			},
			Oracle: SWEOracle{
				RequireNoChangedFiles: true,
				AllowNoWrite:          true,
			},
		}},
	}

	gomega.Expect(LintSWESuite(base)).To(gomega.BeEmpty())
	gomega.Expect(ValidateSWESuite(base)).To(gomega.Succeed())
	gomega.Expect(taskScenarioAssertions(base.Tasks[0])).To(gomega.ContainElement("prompt_sha256"))

	base.Tasks[0].Scenario.PromptSHA256 = "sha256:not-a-digest"
	gomega.Expect(strings.Join(LintSWESuite(base), "\n")).To(gomega.ContainSubstring("prompt_sha256"))
})
var _ = ginkgo.It("live subset assertions do not contradict representative suite for shared tasks", func() {
	liveSuite, err := ReadSWESuite(evalTestdataPath("swe", "live-subset.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ReadSWESuite live subset: %v", err))

	representativeSuite, err := ReadSWESuite(evalTestdataPath("swe", "representative-suite.json"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ReadSWESuite representative suite: %v", err))

	representativeByID := map[string]SWETask{}

	for _, task := range representativeSuite.Tasks {
		id := strings.TrimSpace(task.ID)
		if id != "" {
			representativeByID[id] = task
		}
	}

	for _, liveTask := range liveSuite.Tasks {
		id := strings.TrimSpace(liveTask.ID)
		representativeTask, ok := representativeByID[id]

		if !ok {
			continue
		}

		assertNoScenarioAssertionContradictions(id, "live-subset", liveTask.Scenario, "representative-suite", representativeTask.Scenario)
		assertNoScenarioAssertionContradictions(id, "representative-suite", representativeTask.Scenario, "live-subset", liveTask.Scenario)
	}
})

func assertNoScenarioAssertionContradictions(taskID, requiredSuite string, requiredScenario Scenario, forbiddenSuite string, forbiddenScenario Scenario) {
	ginkgo.GinkgoHelper()

	for _, required := range requiredScenario.FinalAssistantContains {
		required = strings.TrimSpace(required)
		if required == "" {
			continue
		}

		gomega.Expect(containsStringFold(forbiddenScenario.ForbidFinalAssistantContains, required)).To(gomega.BeFalse(), fmt.Sprintf("task %q assertion drift: %s requires final answer phrase %q, but %s forbids it", taskID, requiredSuite, required, forbiddenSuite))
	}
}

var _ = ginkgo.It("analyze swe suite coverage reports missing contract items", func() {
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

	gomega.Expect(coverage.Passed).To(gomega.BeFalse(), fmt.Sprint("expected coverage contract to fail"))

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
		gomega.Expect(joined).To(gomega.ContainSubstring(want), fmt.Sprintf("expected finding %q, got %q", want, joined))
	}

	gomega.Expect(ValidateSWESuite(suite)).NotTo(gomega.Succeed(), fmt.Sprint("expected suite validation to reject failed coverage contract"))
})
var _ = ginkgo.It("coverage cross-products require one task to satisfy every dimension", func() {
	required := SWECoverageCrossProduct{
		Name:                       "locate-read",
		RunnerMode:                 "auto",
		RequiredTags:               []string{"lane-locate", "read-success"},
		RequiredScenarioAssertions: []string{"route_plan", "tools"},
	}
	suite := SWESuite{
		Name:     "cross-product",
		Coverage: SWECoverageContract{RequiredCrossProducts: []SWECoverageCrossProduct{required}},
		Tasks: []SWETask{
			{
				ID:         "route-only",
				Family:     "locate",
				Prompt:     "Locate it.",
				RunnerMode: "auto",
				Tags:       []string{"lane-locate", "read-success"},
				Scenario: Scenario{
					Name:             "route-only",
					PromptContains:   []string{"Locate"},
					RequireRoutePlan: &RoutePlanExpectation{InitialLane: "locate"},
				},
			},
			{
				ID:         "tools-only",
				Family:     "locate",
				Prompt:     "Search it.",
				RunnerMode: "auto",
				Tags:       []string{"read-success"},
				Scenario: Scenario{
					Name:           "tools-only",
					PromptContains: []string{"Search"},
					RequireTools:   []string{"search_content"},
				},
			},
		},
	}

	coverage := AnalyzeSWESuiteCoverage(suite)
	gomega.Expect(coverage.Passed).To(gomega.BeFalse())
	gomega.Expect(coverage.MissingRequiredCrossProducts).To(gomega.Equal([]string{"locate-read"}))
	gomega.Expect(strings.Join(coverage.Findings, "\n")).To(gomega.ContainSubstring(`required cross-product "locate-read" is missing`))

	suite.Tasks[0].Scenario.RequireTools = []string{"search_content"}
	coverage = AnalyzeSWESuiteCoverage(suite)
	gomega.Expect(coverage.Passed).To(gomega.BeTrue(), fmt.Sprintf("cross-product should pass when one task owns every dimension: %v", coverage.Findings))
	gomega.Expect(coverage.CrossProducts["locate-read"]).To(gomega.Equal(1))
})
var _ = ginkgo.It("evaluate swe suite definition does not require logs", func() {
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

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected definition-only report to pass, got %v", report.Findings))

	gomega.Expect(report.Summary.TotalTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected definition summary: %#v", report.Summary))
	gomega.Expect(report.Summary.CoveragePassed).To(gomega.BeTrue(), fmt.Sprintf("unexpected definition summary: %#v", report.Summary))
	gomega.Expect(report.Summary.LintPassed).To(gomega.BeTrue(), fmt.Sprintf("unexpected definition summary: %#v", report.Summary))

	gomega.Expect(report.Tasks).To(gomega.BeEmpty(), fmt.Sprintf("definition-only report should not require task logs: %#v", report.Tasks))
})
var _ = ginkgo.It("task scenario assertions include discovery balance", func() {
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
		gomega.Expect(containsStringFold(assertions, want)).To(gomega.BeTrue(), fmt.Sprintf("expected discovery assertion %q in %#v", want, assertions))
	}
})
var _ = ginkgo.It("task scenario assertions include plan execution", func() {
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

	gomega.Expect(containsStringFold(assertions, "plan_execution")).To(gomega.BeTrue(), fmt.Sprintf("expected plan_execution assertion, got %#v", assertions))
})
var _ = ginkgo.It("task scenario assertions include contract replay", func() {
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

	gomega.Expect(containsStringFold(assertions, "contract_replay")).To(gomega.BeTrue(), fmt.Sprintf("expected contract_replay assertion, got %#v", assertions))
})
var _ = ginkgo.It("evaluate swe suite definition with fixtures reports missing fixture", func() {
	t := ginkgo.GinkgoT()
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

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected suite definition with missing fixture to fail"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("fixture missing"), fmt.Sprintf("expected fixture missing finding, got %q", joined))
})
var _ = ginkgo.It("read swe suite applies task defaults before validation", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	gomega.Expect(os.MkdirAll(filepath.Join(dir, "fixtures", "frozen-label-repo"), 0o755)).To(gomega.Succeed(), "create fixture directory")

	suitePath := filepath.Join(dir, "suite.json")
	gomega.Expect(os.WriteFile(suitePath, []byte(`{
  "name": "defaults-suite",
  "coverage": {
    "required_tags": ["deterministic", "fake-model", "custom"],
    "required_fixtures": ["fixtures/frozen-label-repo"],
    "required_scenario_assertions": ["run_events"]
  },
  "defaults": {
    "tags": ["deterministic", "fake-model"],
    "fixture": "fixtures/frozen-label-repo",
    "scenario": {
      "require_run_events": ["idiom.live_contract.rollback"]
    }
  },
  "tasks": [{
    "id": "defaulted",
    "family": "general_chat",
    "runner_mode": "auto",
    "prompt": "Explain defaults.",
    "tags": ["custom", "fake-model"],
    "scenario": {
      "name": "defaulted",
      "prompt_contains": ["defaults"],
      "require_run_events": ["idiom.live_contract.rollback", "custom_event"]
    }
  }]
}`), 0o644)).To(gomega.Succeed(), "write suite")

	suite, err := ReadSWESuite(suitePath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ReadSWESuite: %v", err))
	{
		findings := LintSWESuite(suite)
		gomega.Expect(findings).To(gomega.BeEmpty(), fmt.Sprintf("defaulted suite should lint cleanly: %v", findings))
	}
	gomega.Expect(ValidateSWESuiteFixtures(suite, suitePath)).To(gomega.Succeed(), "validate defaulted suite fixtures")

	{
		got := suite.Tasks[0].Fixture
		gomega.Expect(got).To(gomega.BeEquivalentTo("fixtures/frozen-label-repo"), fmt.Sprintf("default fixture = %q", got))
	}
	{
		got, want := suite.Tasks[0].Tags, []string{"deterministic", "fake-model", "custom"}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("defaulted tags = %#v, want %#v", got, want))
	}
	{
		got, want := suite.Tasks[0].Scenario.RequireRunEvents, []string{"idiom.live_contract.rollback", "custom_event"}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("defaulted run events = %#v, want %#v", got, want))
	}
})
var _ = ginkgo.It("read swe suite task defaults do not override explicit fixture", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	gomega.Expect(os.WriteFile(suitePath, []byte(`{
  "name": "defaults-suite",
  "defaults": {
    "fixture": "fixtures/frozen-label-repo",
    "scenario": {
      "require_run_events": ["idiom.live_contract.rollback"]
    }
  },
  "tasks": [{
    "id": "explicit",
    "family": "information_request",
    "prompt": "Read the explicit fixture.",
    "fixture": "fixtures/custom-repo",
    "scenario": {
      "name": "explicit",
      "prompt_contains": ["explicit"],
      "require_run_events": ["custom_event"]
    }
  }]
}`), 0o644)).To(gomega.Succeed(), "write suite")

	suite, err := ReadSWESuite(suitePath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("ReadSWESuite: %v", err))

	task := suite.Tasks[0]
	{
		got := task.Fixture
		gomega.Expect(got).To(gomega.BeEquivalentTo("fixtures/custom-repo"), fmt.Sprintf("explicit fixture was overridden: %q", got))
	}
	{
		got, want := task.Scenario.RequireRunEvents, []string{"idiom.live_contract.rollback", "custom_event"}
		gomega.Expect(slices.Equal(got, want)).To(gomega.BeTrue(), fmt.Sprintf("defaulted run events = %#v, want %#v", got, want))
	}
})
var _ = ginkgo.It("lint swe suite reports weak oracles", func() {
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
			{
				ID:     "live-errors-without-typed-recovery",
				Family: "implementation",
				Prompt: "Edit live.",
				Tags:   []string{"live"},
				Scenario: Scenario{
					Name:           "live-errors-without-typed-recovery",
					PromptContains: []string{"Edit live"},
				},
				Oracle: SWEOracle{
					MaxErrors: intPtr(1),
				},
			},
		},
	}
	findings := LintSWESuite(suite)
	joined := strings.Join(findings, "\n")

	for _, want := range []string{"read-only task must require no changed files", "direct implementation task must require changed files or approval", "tool-error task must bound errors", "negative-evidence task must require no changed files", "fixture path is invalid", "multi-turn task must require at least 2 user turns", "multi-turn task must require an assistant response for each user turn", "file content oracle path is invalid", "file content oracle for \"../README.md\" must assert required or forbidden text", "env key \"IDIOM_SWE_LOG\" is reserved by the live runner", "runner_mode \"surprise\" is invalid", "live task with max_errors > 0 must declare typed expected_recoveries"} {
		gomega.Expect(joined).To(gomega.ContainSubstring(want), fmt.Sprintf("expected lint finding %q, got %q", want, joined))
	}

	gomega.Expect(ValidateSWESuite(suite)).NotTo(gomega.Succeed(), fmt.Sprint("expected suite validation to reject weak oracles"))
})
var _ = ginkgo.It("valid swe runner mode accepts feature planner", func() {
	gomega.Expect(validSWERunnerMode("feature-planner")).To(gomega.BeTrue(), fmt.Sprint("feature-planner should be a valid SWE runner mode"))

	gomega.Expect(validSWERunnerMode("tui")).To(gomega.BeTrue(), fmt.Sprint("tui should be a valid SWE runner mode"))
})
var _ = ginkgo.It("validate swe suite fixtures checks relative existing directories", func() {
	t := ginkgo.GinkgoT()
	dir := t.TempDir()
	suitePath := filepath.Join(dir, "suite.json")
	fixtureDir := filepath.Join(dir, "fixtures", "repo")

	gomega.Expect(os.MkdirAll(fixtureDir, 0o755)).To(gomega.Succeed(), "create fixture directory")

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
	gomega.Expect(ValidateSWESuiteFixtures(suite, suitePath)).To(gomega.Succeed(), "validate suite fixtures")

	suite.Tasks[0].Fixture = "fixtures/missing"
	gomega.Expect(ValidateSWESuiteFixtures(suite, suitePath)).NotTo(gomega.Succeed(), fmt.Sprint("expected missing fixture validation failure"))

	suite.Tasks[0].Fixture = "/tmp/absolute"
	gomega.Expect(ValidateSWESuiteFixtures(suite, suitePath)).NotTo(gomega.Succeed(), fmt.Sprint("expected absolute fixture validation failure"))

	suite.Tasks[0].Fixture = `..\outside`
	gomega.Expect(ValidateSWESuiteFixtures(suite, suitePath)).NotTo(gomega.Succeed(), fmt.Sprint("expected backslash traversal fixture validation failure"))
})
var _ = ginkgo.It("validate swe suite fixtures checks fixture git refs", func() {
	t := ginkgo.GinkgoT()
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

	gomega.Expect(ValidateSWESuiteFixtures(suite, suitePath)).NotTo(gomega.Succeed(), fmt.Sprint("expected unreachable fixture_git_ref validation failure"))

	suite.Tasks[0].FixtureGitRef = "-bad"
	gomega.Expect(ValidateSWESuiteFixtures(suite, suitePath)).NotTo(gomega.Succeed(), fmt.Sprint("expected unsafe fixture_git_ref validation failure"))
})
var _ = ginkgo.It("evaluate swe task applies oracle", func() {
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

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected SWE task to pass, got %#v", report.Findings))
})
var _ = ginkgo.It("evaluate swe task require no approval rejects approval required", func() {
	task := SWETask{
		ID:       "no-approval",
		Family:   "implementation",
		Prompt:   "Update README.md.",
		Scenario: Scenario{Name: "no-approval", PromptContains: []string{"Update README.md"}},
		Oracle:   SWEOracle{RequireNoApproval: true},
	}
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Update README.md."}},
		{Type: "tool_result", Data: map[string]any{"tool": "edit_file", "summary": "write approval required for README.md"}},
	}

	report := EvaluateSWETask("no-approval.jsonl", records, task)

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected approval-required run to fail require_no_approval oracle"))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("approval was forbidden but required by run"), fmt.Sprintf("expected require_no_approval finding, got %#v", report.Findings))
})
var _ = ginkgo.It("evaluate swe task applies file content oracle", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	gomega.Expect(os.WriteFile(filepath.Join(root, "README.md"), []byte("debug logs are searchable\n"), 0o644)).To(gomega.Succeed(), "write readme")

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

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected file content oracle to pass, got %#v", report.Findings))

	task.Oracle.RequireFileContent[0].Contains = []string{"missing text"}
	report = EvaluateSWETask("docs.jsonl", records, task)

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected file content failure, got passed=%t findings=%#v", report.Passed, report.Findings))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("does not contain required text"), fmt.Sprintf("expected file content failure, got passed=%t findings=%#v", report.Passed, report.Findings))

	task.Oracle.RequireFileContent[0] = SWEFileContentExpectation{Path: `..\secret.md`, Contains: []string{"secret"}}
	report = EvaluateSWETask("docs.jsonl", records, task)

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected unsafe file content path failure, got passed=%t findings=%#v", report.Passed, report.Findings))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("escapes workspace root"), fmt.Sprintf("expected unsafe file content path failure, got passed=%t findings=%#v", report.Passed, report.Findings))
})
var _ = ginkgo.It("evaluate swe task file content oracle uses live snapshot", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	task := SWETask{
		ID:     "generated-adr",
		Family: "feature_planner",
		Prompt: "/feature-planner Add greeting package",
		Scenario: Scenario{
			Name:           "generated-adr",
			PromptContains: []string{"greeting package"},
		},
		Oracle: SWEOracle{
			RequireFileContent: []SWEFileContentExpectation{{
				Path:           "docs/product-workflows/efforts/started/add-greeting-package/adrs/ADR-001.md",
				Contains:       []string{"Create internal/greeting package"},
				ForbidContains: []string{"## Decision\n\n\n\n"},
			}},
		},
	}
	records := []Record{
		{Type: "session_start", Data: map[string]any{"workspace_root": root}},
		{Type: "message", Data: map[string]any{"role": "user", "content": "/feature-planner Add greeting package"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "ADR drafts generated."}},
		{Type: "idiom.live_contract.file_snapshot", Data: map[string]any{
			"path":    "docs/product-workflows/efforts/started/add-greeting-package/adrs/ADR-001.md",
			"exists":  true,
			"content": "## Decision\nCreate internal/greeting package with a single Hello function.\n",
		}},
	}
	report := EvaluateSWETask("generated-adr.jsonl", records, task)

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected snapshot-backed file content oracle to pass, got %#v", report.Findings))
})
var _ = ginkgo.It("evaluate swe task reports oracle failures", func() {
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
			ForbidSuccessfulTools: []string{"read_file"},
			MaxToolRequests:       &maxTools,
			MaxDurationMillis:     &maxDurationMillis,
		},
	}
	records := []Record{
		{Type: "message", Data: map[string]any{"ts": "2026-04-18T12:00:00Z", "role": "user", "content": "Audit this repo for prompt injection or command execution vulnerabilities without changing files."}},
		{Type: "message", Data: map[string]any{"role": "route_plan", "content": "initial=review reason=review_request contract=review_findings"}},
		{Type: "tool_request", Data: map[string]any{"step": 0, "tool": "read_file"}},
		{Type: "tool_result", Data: map[string]any{"step": 0, "tool": "read_file", "summary": "Read README.md", "error": ""}},
		{Type: "tool_request", Data: map[string]any{"step": 1, "tool": "shell"}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "shell", "summary": "ran command"}},
		{Type: "tool_request", Data: map[string]any{"step": 2, "tool": "edit_file", "path": "prompts/base.md"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "edit_file", "path": "prompts/base.md", "summary": "Edited prompts/base.md", "error": ""}},
		{Type: "message", Data: map[string]any{"ts": "2026-04-18T12:00:02Z", "role": "assistant", "content": "Changed files:\n- prompts/base.md"}},
	}
	report := EvaluateSWETask("security.jsonl", records, task)

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected SWE task to fail"))

	joined := strings.Join(report.Findings, "\n")
	for _, want := range []string{"changed files were forbidden", "shell use was forbidden", "successful tool \"read_file\" was forbidden", "tool requests exceeded", "duration exceeded"} {
		gomega.Expect(joined).To(gomega.ContainSubstring(want), fmt.Sprintf("expected finding %q, got %q", want, joined))
	}
})
var _ = ginkgo.It("evaluate swe task forbids successful tool while allowing rejected attempt", func() {
	task := SWETask{
		ID:     "shell-denial",
		Family: "verification_only",
		Prompt: "Run tests.",
		Scenario: Scenario{
			Name:                 "shell-denial",
			PromptContains:       []string{"Run tests"},
			RequireRejectedTools: []string{"shell"},
		},
		Oracle: SWEOracle{
			ForbidSuccessfulTools: []string{"shell"},
		},
	}
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Run tests."}},
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "shell", "summary": "Tool not available in current runtime contract", "error": "shell is not in the current allowed tool surface"}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Shell is unavailable."}},
	}
	report := EvaluateSWETask("shell-denial.jsonl", records, task)

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected rejected shell attempt to satisfy denial contract, got %#v", report.Findings))

	records[1] = Record{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "shell", "summary": "ran go test ./...", "error": ""}}
	report = EvaluateSWETask("shell-success.jsonl", records, task)

	joined := strings.Join(report.Findings, "\n")

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected successful shell to fail both denial and success-forbid checks, passed=%t findings=%#v", report.Passed, report.Findings))
	gomega.Expect(joined).To(gomega.ContainSubstring(`required rejected tool "shell" was not observed`), fmt.Sprintf("expected successful shell to fail both denial and success-forbid checks, passed=%t findings=%#v", report.Passed, report.Findings))
	gomega.Expect(joined).To(gomega.ContainSubstring(`successful tool "shell" was forbidden`), fmt.Sprintf("expected successful shell to fail both denial and success-forbid checks, passed=%t findings=%#v", report.Passed, report.Findings))
})
var _ = ginkgo.It("evaluate swe task product workflow blocker does not satisfy implementation acceptance", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("typed blocker without executor handoff, changed files, or verification must not satisfy implementation acceptance"))

	joined := strings.Join(report.Findings, "\n")
	for _, want := range []string{"changed files do not include", "plan execution handoff id was required", "executor handoff was required", "verification did not pass", "blocker-only without allow_no_write"} {
		gomega.Expect(joined).To(gomega.ContainSubstring(want), fmt.Sprintf("expected finding %q, got %q", want, joined))
	}

	gomega.Expect(joined).NotTo(gomega.ContainSubstring("runner rejections were forbidden"), fmt.Sprintf("typed blocker fixture should fail implementation acceptance, not runner-rejection oracle: %q", joined))
})
var _ = ginkgo.It("evaluate swe task counts explicit completion gate reject after post readiness gap", func() {
	task := SWETask{
		ID:     "read-only-gap-friction",
		Family: "direct_question",
		Scenario: Scenario{
			Name:                        "read-only-gap-friction",
			PromptContains:              []string{"save label"},
			FinalAssistantContains:      []string{"Save changes"},
			RequireAssistantForEachUser: true,
		},
		Oracle: SWEOracle{
			RequireNoChangedFiles:     true,
			RequireNoRunnerRejections: true,
			MaxErrors:                 intPtr(0),
		},
	}
	records := []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Can we shorten the save label?"}},
		{Type: "tool_request", Data: map[string]any{"tool": "read_file", "path": "internal/labels/labels.go"}},
		{Type: "tool_result", Data: map[string]any{"tool": "read_file", "summary": "Read internal/labels/labels.go lines 1-5", "error": ""}},
		{Type: "tool_request", Data: map[string]any{"tool": "code_navigate", "path": "internal/labels/labels.go"}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "code_navigate",
			"summary": "Post-readiness follow-up missing named gap",
			"error":   "post-readiness exact source probes require a named_gap",
		}},
		{Type: "run_event", Data: map[string]any{
			"role":    "failure_feedback",
			"content": `{"reason":"tool_rejected","failure_class":"tool_failure","result_summary":"Post-readiness follow-up missing named gap","failed_tool":"code_navigate"}`,
		}},
		{Type: "run_event", Data: map[string]any{
			"role":    "autonomy_event",
			"content": `{"type":"gate_decision","gate":{"gate":"completion_gate","decision":"reject","reason_code":"non_answer_prose"},"correlation":{"permission_profile":"read_only_workspace"}}`,
		}},
		{Type: "turn_summary", Data: map[string]any{
			"rejected_actions":           1,
			"completion_gate_rejections": 1,
			"stop_reason":                "post_readiness_gap_required_budget_exhausted",
		}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "The inspected source returns `Save changes`, so the label can be shortened. No files were edited."}},
	}

	report := EvaluateSWETask("read-only-gap-friction.jsonl", records, task)
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("explicit completion-gate rejection should still fail no-runner-rejection oracle"))

	gomega.Expect(report.Metrics.TotalErrorEvents()).To(gomega.BeEquivalentTo(0), fmt.Sprintf("recoverable post-readiness tool friction should not be scored as a tool error: %#v", report.Metrics))

	gomega.Expect(report.Metrics.RejectedActions).To(gomega.BeEquivalentTo(1), fmt.Sprintf("explicit completion gate rejection should remain visible: %#v", report.Metrics))
	gomega.Expect(report.Metrics.CompletionGateRejections).To(gomega.BeEquivalentTo(1), fmt.Sprintf("explicit completion gate rejection should remain visible: %#v", report.Metrics))
})
var _ = ginkgo.It("evaluate swe task source satisfied needs explicit allowance for implementation required task", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("source-satisfied-only must not satisfy implementation-required task without explicit allowance"))

	joined := strings.Join(report.Findings, "\n")
	for _, want := range []string{"source-satisfied-only without allow_source_satisfied", "changed files do not include"} {
		gomega.Expect(joined).To(gomega.ContainSubstring(want), fmt.Sprintf("expected finding %q, got %q", want, joined))
	}

	gomega.Expect(report.Metrics.SourceSatisfiedOutcomes).NotTo(gomega.BeEquivalentTo(0), fmt.Sprintf("expected source-satisfied outcome metric, got %#v", report.Metrics))
})
var _ = ginkgo.It("evaluate swe task allows explicit source satisfied no write fixture", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("explicit source-satisfied no-write fixture should pass, findings: %v", report.Findings))
})
var _ = ginkgo.It("evaluate swe task requires clean rollback", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected dirty rollback to fail"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("rollback status was not clean"), fmt.Sprintf("expected rollback finding, got %q", joined))
})
var _ = ginkgo.It("evaluate swe task groundedness boosts context files", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	contextPath := "internal/runtime/agent/runner.go"
	full := filepath.Join(root, contextPath)

	gomega.Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(gomega.Succeed(), "create context directory")

	gomega.Expect(os.WriteFile(full, []byte("the runner enforces request budget through ledger snapshot"), 0o644)).To(gomega.Succeed(), "write context")

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

	gomega.Expect(report.LearningUpdate).NotTo(gomega.BeNil(), fmt.Sprintf("expected learning update record, got nil; findings=%#v", report.Findings))

	gomega.Expect(report.LearningUpdate.Action).To(gomega.BeEquivalentTo("boost"), fmt.Sprintf("expected boost action, got %q (score=%v)", report.LearningUpdate.Action, report.LearningUpdate.Score))

	gomega.Expect(report.LearningUpdate.Applied).To(gomega.BeTrue(), fmt.Sprintf("expected applied=true for boost band"))

	gomega.Expect(report.LearningUpdate.EventID).NotTo(gomega.BeEquivalentTo(""), fmt.Sprintf("expected accepted learning event, got %#v", report.LearningUpdate))
	gomega.Expect(report.LearningUpdate.State).To(gomega.BeEquivalentTo("accepted"), fmt.Sprintf("expected accepted learning event, got %#v", report.LearningUpdate))

	weightsPath := filepath.Join(root, ".idiom", "ranker", "weights.json")
	{
		_, err := os.Stat(weightsPath)
		gomega.Expect(err).To(gomega.BeNil(), fmt.Sprintf("weights.json missing: %v", err))
	}

	logPath := filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl")

	data, err := os.ReadFile(logPath)
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("learning log missing: %v", err))

	gomega.Expect(string(data)).To(gomega.ContainSubstring("\"source\":\"eval\""), fmt.Sprintf("learning log missing expected entry: %s", string(data)))
	gomega.Expect(string(data)).To(gomega.ContainSubstring("\"action\":\"boost\""), fmt.Sprintf("learning log missing expected entry: %s", string(data)))
	gomega.Expect(string(data)).To(gomega.ContainSubstring("\"state\":\"accepted\""), fmt.Sprintf("learning log missing expected entry: %s", string(data)))
	gomega.Expect(string(data)).To(gomega.ContainSubstring("\"before_weights\""), fmt.Sprintf("learning log missing expected entry: %s", string(data)))
	gomega.Expect(string(data)).To(gomega.ContainSubstring("\"rollback\""), fmt.Sprintf("learning log missing expected entry: %s", string(data)))
})
var _ = ginkgo.It("evaluate swe task groundedness penalizes low score", func() {
	t := ginkgo.GinkgoT()
	root := t.TempDir()
	contextPath := "spec.md"

	gomega.Expect(os.WriteFile(filepath.Join(root, contextPath), []byte("workspace ranker hub demotion penalty multipliers"), 0o644)).To(gomega.Succeed(), "write context")

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

	gomega.Expect(report.LearningUpdate).NotTo(gomega.BeNil(), fmt.Sprintf("expected learning update, got nil"))

	gomega.Expect(report.LearningUpdate.Action).To(gomega.BeEquivalentTo("penalize"), fmt.Sprintf("expected penalize action, got %q (score=%v)", report.LearningUpdate.Action, report.LearningUpdate.Score))
})
var _ = ginkgo.It("evaluate swe task groundedness learning honors disable", func() {
	t := ginkgo.GinkgoT()
	t.Setenv("IDIOM_RANKER_LEARNING", "0")

	root := t.TempDir()
	contextPath := "spec.md"

	gomega.Expect(os.WriteFile(filepath.Join(root, contextPath), []byte("runner budget ledger snapshot"), 0o644)).To(gomega.Succeed(), "write context")

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

	gomega.Expect(report.LearningUpdate).NotTo(gomega.BeNil(), fmt.Sprintf("expected learning update"))

	gomega.Expect(report.LearningUpdate.Applied).To(gomega.BeFalse(), fmt.Sprintf("disabled learning should be rejected without applying: %#v", report.LearningUpdate))
	gomega.Expect(report.LearningUpdate.State).To(gomega.BeEquivalentTo("rejected"), fmt.Sprintf("disabled learning should be rejected without applying: %#v", report.LearningUpdate))
	gomega.Expect(report.LearningUpdate.Reason).To(gomega.BeEquivalentTo("learning_disabled"), fmt.Sprintf("disabled learning should be rejected without applying: %#v", report.LearningUpdate))

	{
		_, err := os.Stat(filepath.Join(root, ".idiom", "ranker", "weights.json"))
		gomega.Expect(os.IsNotExist(err)).To(gomega.BeTrue(), fmt.Sprintf("disabled learning should not create weights.json, err=%v", err))
	}

	data, err := os.ReadFile(filepath.Join(root, ".idiom", "ranker", "learning_log.jsonl"))
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("learning log missing: %v", err))

	gomega.Expect(string(data)).To(gomega.ContainSubstring("\"state\":\"rejected\""), fmt.Sprintf("learning log missing disabled rejection: %s", string(data)))
	gomega.Expect(string(data)).To(gomega.ContainSubstring("\"reason\":\"learning_disabled\""), fmt.Sprintf("learning log missing disabled rejection: %s", string(data)))
})
var _ = ginkgo.It("evaluate swe task requires no runner rejections", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected runner rejection to fail"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("runner rejections were forbidden"), fmt.Sprintf("expected runner rejection finding, got %q", joined))

	gomega.Expect(containsStringFold(taskOracleAssertions(task), "no_runner_rejections")).To(gomega.BeTrue(), fmt.Sprintf("expected no_runner_rejections assertion"))
})
var _ = ginkgo.It("evaluate swe task expected recovery can account for runner rejection", func() {
	maxErrors := 0
	task := SWETask{
		ID:       "typed-post-readiness-recovery",
		Family:   "direct_question",
		Scenario: Scenario{Name: "typed-post-readiness-recovery"},
		Oracle: SWEOracle{
			RequireNoRunnerRejections: true,
			MaxErrors:                 &maxErrors,
			ExpectedRecoveries: []SWEExpectedRecovery{{
				Kind:                       "post_readiness_named_gap_recovery",
				EventType:                  "tool_result",
				Tool:                       "code_navigate",
				SummaryContains:            []string{"Post-readiness follow-up missing named gap"},
				ErrorContains:              []string{"post-readiness exact source probes require a named_gap"},
				AccountsForRunnerRejection: true,
				MaxCount:                   intPtr(1),
			}},
		},
	}
	records := []Record{
		{Type: "tool_result", Data: map[string]any{
			"tool":    "code_navigate",
			"summary": "Post-readiness follow-up missing named gap",
			"error":   "post-readiness exact source probes require a named_gap",
		}},
		{Type: "turn_summary", Data: map[string]any{"rejected_actions": 1, "completion_gate_rejections": 0}},
	}

	report := EvaluateSWETask("typed-recovery.jsonl", records, task)
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected typed recovery to account for runner rejection, got %#v", report.Findings))

	unexpected := append(records, Record{Type: "tool_result", Data: map[string]any{
		"tool":    "shell",
		"summary": "shell rejected",
		"error":   "shell not in the current allowed tool surface",
	}})

	report = EvaluateSWETask("typed-recovery.jsonl", unexpected, task)

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected unexpected runner rejection to fail"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("unexpected error event did not match expected_recoveries"), fmt.Sprintf("expected unmatched recovery finding, got %q", joined))
})
var _ = ginkgo.It("evaluate swe task expected recovery does not account for non rejection event", func() {
	maxErrors := 0
	task := SWETask{
		ID:       "typed-non-rejection-event",
		Family:   "direct_question",
		Scenario: Scenario{Name: "typed-non-rejection-event"},
		Oracle: SWEOracle{
			RequireNoRunnerRejections: true,
			MaxErrors:                 &maxErrors,
			ExpectedRecoveries: []SWEExpectedRecovery{{
				Kind:                       "post_readiness_named_gap_recovery",
				EventType:                  "tool_result",
				Tool:                       "code_navigate",
				SummaryContains:            []string{"Post-readiness follow-up missing named gap"},
				ErrorContains:              []string{"post-readiness exact source probes require a named_gap"},
				AccountsForRunnerRejection: true,
				MaxCount:                   intPtr(1),
			}},
		},
	}
	records := []Record{
		{Type: "tool_result", Data: map[string]any{
			"tool":            "code_navigate",
			"summary":         "Post-readiness follow-up missing named gap",
			"error":           "post-readiness exact source probes require a named_gap",
			"runner_rejected": false,
		}},
		{Type: "turn_summary", Data: map[string]any{"rejected_actions": 1, "completion_gate_rejections": 0}},
	}

	report := EvaluateSWETask("typed-non-rejection-event.jsonl", records, task)
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("matched expected_recoveries event without runner rejection flag must not account for aggregate rejected_actions"))

	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("runner rejections were forbidden"), fmt.Sprintf("expected unaccounted runner rejection finding, got %#v", report.Findings))
})
var _ = ginkgo.It("evaluate swe task expected recovery does not double account overlapping matcher", func() {
	maxErrors := 0
	task := SWETask{
		ID:       "typed-overlapping-recovery",
		Family:   "direct_question",
		Scenario: Scenario{Name: "typed-overlapping-recovery"},
		Oracle: SWEOracle{
			RequireNoRunnerRejections: true,
			MaxErrors:                 &maxErrors,
			ExpectedRecoveries: []SWEExpectedRecovery{
				{
					Kind:                       "post_readiness_named_gap_recovery",
					EventType:                  "tool_result",
					Tool:                       "code_navigate",
					SummaryContains:            []string{"Post-readiness follow-up missing named gap"},
					ErrorContains:              []string{"post-readiness exact source probes require a named_gap"},
					AccountsForRunnerRejection: true,
					MaxCount:                   intPtr(1),
				},
				{
					Kind:                       "broad_named_gap_recovery",
					EventType:                  "tool_result",
					Tool:                       "code_navigate",
					ErrorContains:              []string{"post-readiness exact source probes require a named_gap"},
					AccountsForRunnerRejection: true,
					MaxCount:                   intPtr(1),
				},
			},
		},
	}
	records := []Record{
		{Type: "tool_result", Data: map[string]any{
			"tool":    "code_navigate",
			"summary": "Post-readiness follow-up missing named gap",
			"error":   "post-readiness exact source probes require a named_gap",
		}},
		{Type: "turn_summary", Data: map[string]any{"rejected_actions": 2, "completion_gate_rejections": 0}},
	}

	report := EvaluateSWETask("typed-overlapping-recovery.jsonl", records, task)
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected one actual recovery event not to account for two aggregate runner rejections"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("runner rejections were forbidden but observed unexpected rejected_actions=1"), fmt.Sprintf("expected overlapping matcher not to double-account, got %q", joined))
})
var _ = ginkgo.It("evaluate swe task expected recovery can bound guard family across tools", func() {
	maxErrors := 0
	task := SWETask{
		ID:       "typed-cross-tool-recovery",
		Family:   "direct_question",
		Scenario: Scenario{Name: "typed-cross-tool-recovery"},
		Oracle: SWEOracle{
			RequireNoRunnerRejections: true,
			MaxErrors:                 &maxErrors,
			ExpectedRecoveries: []SWEExpectedRecovery{{
				Kind:                       "post_readiness_named_gap_recovery",
				EventType:                  "tool_result",
				SummaryContains:            []string{"Post-readiness follow-up missing named gap"},
				ErrorContains:              []string{"post-readiness exact source probes require a named_gap"},
				AccountsForRunnerRejection: true,
				MaxCount:                   intPtr(3),
			}},
		},
	}
	records := []Record{
		{Type: "tool_result", Data: map[string]any{
			"tool":    "read_file",
			"summary": "Post-readiness follow-up missing named gap",
			"error":   "post-readiness exact source probes require a named_gap",
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "code_navigate",
			"summary": "Post-readiness follow-up missing named gap",
			"error":   "post-readiness exact source probes require a named_gap",
		}},
		{Type: "tool_result", Data: map[string]any{
			"tool":    "search_content",
			"summary": "Post-readiness follow-up missing named gap",
			"error":   "post-readiness exact source probes require a named_gap",
		}},
		{Type: "turn_summary", Data: map[string]any{"rejected_actions": 3, "completion_gate_rejections": 0}},
	}

	report := EvaluateSWETask("typed-cross-tool-recovery.jsonl", records, task)
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected one typed recovery family to account across tools, got %#v", report.Findings))

	tooMany := append([]Record{}, records...)

	tooMany = append(tooMany[:len(tooMany)-1], Record{Type: "tool_result", Data: map[string]any{
		"tool":    "search_content",
		"summary": "Post-readiness follow-up missing named gap",
		"error":   "post-readiness exact source probes require a named_gap",
	}}, tooMany[len(tooMany)-1])
	tooMany[len(tooMany)-1] = Record{Type: "turn_summary", Data: map[string]any{"rejected_actions": 4, "completion_gate_rejections": 0}}

	report = EvaluateSWETask("typed-cross-tool-recovery.jsonl", tooMany, task)
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected typed recovery family maximum to reject excess guard friction"))

	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("exceeded maximum"), fmt.Sprintf("expected max-count finding, got %#v", report.Findings))
})
var _ = ginkgo.It("evaluate swe task requires turn summary for no runner rejections", func() {
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

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected missing turn_summary to fail no-runner-rejections oracle"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("no turn_summary was present"), fmt.Sprintf("expected missing turn_summary finding, got %q", joined))
})
var _ = ginkgo.It("evaluate swe task requires no runner rejections from gate events", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected gate rejection event to fail no-runner-rejections oracle"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("completion_gate_rejections=1"), fmt.Sprintf("expected completion gate rejection finding, got %q", joined))
})
var _ = ginkgo.It("evaluate swe task contract replay uses autonomy events", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected contract replay task to pass, got %#v", report.Findings))

	gomega.Expect(report.Metrics.ContractReplay).NotTo(gomega.BeNil(), fmt.Sprint("expected contract replay metrics"))

	gomega.Expect(report.Metrics.ContractReplay.TotalEvents).To(gomega.BeEquivalentTo(2), fmt.Sprintf("unexpected contract replay report: %#v", report.Metrics.ContractReplay))
	gomega.Expect(report.Metrics.ContractReplay.AllClear).To(gomega.BeTrue(), fmt.Sprintf("unexpected contract replay report: %#v", report.Metrics.ContractReplay))

	gomega.Expect(report.Metrics.ContractReplay.Packs["completion/readiness"]).To(gomega.BeEquivalentTo(1), fmt.Sprintf("expected replay pack count, got %#v", report.Metrics.ContractReplay.Packs))
})
var _ = ginkgo.It("evaluate swe task contract replay fails expectations", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected contract replay violation to fail"))

	joined := strings.Join(report.Findings, "\n")
	for _, want := range []string{"contract replay violations exceeded limit", "contract replay violation \"contract_event_unmatched_contract_reference\" was forbidden"} {
		gomega.Expect(joined).To(gomega.ContainSubstring(want), fmt.Sprintf("expected finding %q, got %q", want, joined))
	}
})
var _ = ginkgo.It("evaluate swe task contract replay max violations ignores audit diagnostics", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected audit-only diagnostics not to count against replay max violations, got %#v", report.Findings))

	gomega.Expect(report.Metrics.ContractReplay).NotTo(gomega.BeNil(), fmt.Sprintf("expected audit diagnostic to remain visible, got %#v", report.Metrics.ContractReplay))
	gomega.Expect(report.Metrics.ContractReplay.Violations).NotTo(gomega.HaveLen(0), fmt.Sprintf("expected audit diagnostic to remain visible, got %#v", report.Metrics.ContractReplay))

	{
		got := len(report.Metrics.ContractReplay.Replay.Violations)
		gomega.Expect(got).To(gomega.BeEquivalentTo(0), fmt.Sprintf("expected no deterministic replay violations, got %d", got))
	}
})
var _ = ginkgo.It("evaluate swe task without autonomy events keeps replay optional", func() {
	task := SWETask{
		ID:       "no-contract-replay",
		Family:   "direct_question",
		Scenario: Scenario{Name: "no-contract-replay"},
	}

	report := EvaluateSWETask("no-contract-replay.jsonl", []Record{
		{Type: "message", Data: map[string]any{"role": "user", "content": "Explain."}},
		{Type: "message", Data: map[string]any{"role": "assistant", "content": "Done."}},
	}, task)
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected task without contract replay expectation to pass, got %#v", report.Findings))

	gomega.Expect(report.Metrics.ContractReplay).To(gomega.BeNil(), fmt.Sprintf("expected no contract replay metrics without autonomy events, got %#v", report.Metrics.ContractReplay))

	task.Scenario.RequireContractReplay = &ContractReplayExpectation{}
	report = EvaluateSWETask("no-contract-replay.jsonl", nil, task)

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprintf("expected missing contract replay finding, got passed=%t findings=%#v", report.Passed, report.Findings))
	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("required contract replay was not observed"), fmt.Sprintf("expected missing contract replay finding, got passed=%t findings=%#v", report.Passed, report.Findings))
})
var _ = ginkgo.It("evaluate swe task distinguishes low value actions from runner rejections", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected low-value action limit to fail"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("low-value actions exceeded"), fmt.Sprintf("expected low-value finding, got %q", joined))

	gomega.Expect(joined).NotTo(gomega.ContainSubstring("runner rejections were forbidden"), fmt.Sprintf("low-value action must not be treated as hard runner rejection: %q", joined))

	gomega.Expect(report.Metrics.LowValueActions).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected metrics: %#v", report.Metrics))
	gomega.Expect(report.Metrics.RejectedActions).To(gomega.BeEquivalentTo(0), fmt.Sprintf("unexpected metrics: %#v", report.Metrics))
	gomega.Expect(report.Metrics.CompletionGateRejections).To(gomega.BeEquivalentTo(0), fmt.Sprintf("unexpected metrics: %#v", report.Metrics))
})
var _ = ginkgo.It("evaluate swe task max errors uses error events not unique messages", func() {
	maxErrors := 1
	task := SWETask{
		ID:       "repeat-error",
		Family:   "implementation",
		Scenario: Scenario{Name: "repeat-error"},
		Oracle:   SWEOracle{MaxErrors: &maxErrors},
	}
	records := []Record{
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "read_file", "summary": "Implementation target write-ready tool rejected", "error": "same rejection"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "read_file", "summary": "Implementation target write-ready tool rejected", "error": "same rejection"}},
	}

	report := EvaluateSWETask("repeat-error.jsonl", records, task)
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected max_errors to fail on two identical error events"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("unexpected errors exceeded oracle limit: got 2 want <= 1"), fmt.Sprintf("expected error event count finding, got %q", joined))

	gomega.Expect(report.Metrics.ErrorCount).To(gomega.BeEquivalentTo(2), fmt.Sprintf("expected event count plus deduplicated messages, got count=%d errors=%#v", report.Metrics.ErrorCount, report.Metrics.Errors))
	gomega.Expect(report.Metrics.Errors).To(gomega.HaveLen(1), fmt.Sprintf("expected event count plus deduplicated messages, got count=%d errors=%#v", report.Metrics.ErrorCount, report.Metrics.Errors))
})
var _ = ginkgo.It("evaluate swe task expected recoveries do not hide unrelated errors", func() {
	maxErrors := 2
	task := SWETask{
		ID:       "typed-recoveries",
		Family:   "implementation",
		Scenario: Scenario{Name: "typed-recoveries"},
		Oracle: SWEOracle{
			MaxErrors: &maxErrors,
			ExpectedRecoveries: []SWEExpectedRecovery{{
				Kind:            "write_surface_rejection",
				EventType:       "tool_result",
				Tool:            "read_file",
				SummaryContains: []string{"write-ready tool rejected"},
				ErrorContains:   []string{"outside the target-write-ready surface"},
				MaxCount:        intPtr(1),
			}},
		},
	}
	records := []Record{
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "read_file", "summary": "Implementation target write-ready tool rejected", "error": "read_file is outside the target-write-ready surface"}},
		{Type: "tool_result", Data: map[string]any{"step": 2, "tool": "shell", "summary": "command failed", "error": "unexpected unrelated failure"}},
	}

	report := EvaluateSWETask("typed-recoveries.jsonl", records, task)
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected unrelated error to fail despite max_errors allowance"))

	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("unexpected error event did not match expected_recoveries"), fmt.Sprintf("expected unrelated error finding, got %#v", report.Findings))

	matched := EvaluateSWETask("typed-recoveries.jsonl", records[:1], task)
	gomega.Expect(matched.Passed).To(gomega.BeTrue(), fmt.Sprintf("expected matching recovery error to pass, got %#v", matched.Findings))

	gomega.Expect(matched.Metrics.ErrorEvents).To(gomega.HaveLen(1), fmt.Sprintf("expected typed error event metrics, got %#v", matched.Metrics.ErrorEvents))
	gomega.Expect(matched.Metrics.ErrorEvents[0].Tool).To(gomega.BeEquivalentTo("read_file"), fmt.Sprintf("expected typed error event metrics, got %#v", matched.Metrics.ErrorEvents))
})
var _ = ginkgo.It("evaluate swe task expected recoveries do not consume max errors", func() {
	maxErrors := 0
	task := SWETask{
		ID:       "typed-recovery-strict",
		Family:   "implementation",
		Scenario: Scenario{Name: "typed-recovery-strict"},
		Oracle: SWEOracle{
			MaxErrors: &maxErrors,
			ExpectedRecoveries: []SWEExpectedRecovery{{
				Kind:          "completion_gate_recovery",
				EventType:     "tool_result",
				Tool:          "completion_gate",
				ErrorContains: []string{"verification required"},
				MaxCount:      intPtr(1),
			}},
		},
	}
	records := []Record{
		{Type: "tool_result", Data: map[string]any{"step": 1, "tool": "completion_gate", "summary": "completion rejected", "error": "verification required before final answer"}},
	}

	report := EvaluateSWETask("typed-recovery-strict.jsonl", records, task)
	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("typed expected recovery should not consume max_errors, got %#v", report.Findings))
})
var _ = ginkgo.It("evaluate swe task expected recoveries default to required", func() {
	task := SWETask{
		ID:       "typed-recovery-required",
		Family:   "implementation",
		Scenario: Scenario{Name: "typed-recovery-required"},
		Oracle: SWEOracle{
			ExpectedRecoveries: []SWEExpectedRecovery{{
				Kind:          "completion_gate_recovery",
				EventType:     "tool_result",
				Tool:          "completion_gate",
				ErrorContains: []string{"verification required"},
				MaxCount:      intPtr(1),
			}},
		},
	}
	report := EvaluateSWETask("typed-recovery-required.jsonl", nil, task)

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected missing recovery to fail by default"))

	gomega.Expect(strings.Join(report.Findings, "\n")).To(gomega.ContainSubstring("below minimum: got 0 want >= 1"), fmt.Sprintf("expected missing recovery finding, got %#v", report.Findings))

	optional := task

	optional.Oracle.ExpectedRecoveries[0].MinCount = intPtr(0)
	report = EvaluateSWETask("typed-recovery-optional.jsonl", nil, optional)

	gomega.Expect(report.Passed).To(gomega.BeTrue(), fmt.Sprintf("explicit min_count=0 recovery should be optional, got %#v", report.Findings))
})
var _ = ginkgo.It("evaluate swe task requires no timeout", func() {
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
	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected timeout oracle to fail"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("timeouts were forbidden"), fmt.Sprintf("expected timeout finding, got %q", joined))

	gomega.Expect(joined).NotTo(gomega.ContainSubstring("runner rejections were forbidden"), fmt.Sprintf("timeout must not be treated as runner rejection: %q", joined))
})
var _ = ginkgo.It("lint swe suite flags groundedness config errors", func() {
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

	gomega.Expect(joined).To(gomega.ContainSubstring("no-context"), fmt.Sprintf("expected no-context lint finding, got %v", findings))
	gomega.Expect(joined).To(gomega.ContainSubstring("context_files or context_fragments"), fmt.Sprintf("expected no-context lint finding, got %v", findings))

	gomega.Expect(joined).To(gomega.ContainSubstring("out-of-range"), fmt.Sprintf("expected out-of-range lint finding, got %v", findings))
	gomega.Expect(joined).To(gomega.ContainSubstring("must be in [0, 1]"), fmt.Sprintf("expected out-of-range lint finding, got %v", findings))

	gomega.Expect(joined).To(gomega.ContainSubstring("absolute-path"), fmt.Sprintf("expected absolute-path lint finding, got %v", findings))

	gomega.Expect(joined).To(gomega.ContainSubstring("backslash-traversal"), fmt.Sprintf("expected backslash-traversal lint finding, got %v", findings))
})
var _ = ginkgo.It("task oracle assertions covers new oracles", func() {
	task := SWETask{
		Oracle: SWEOracle{
			Groundedness:         &SWEGroundednessOracle{MinScore: 0.5, ContextFragments: []string{"x"}},
			ForbidGenericPhrases: true,
		},
	}
	got := taskOracleAssertions(task)

	gomega.Expect(containsStr(got, "groundedness")).To(gomega.BeTrue(), fmt.Sprintf("expected groundedness in assertions, got %v", got))

	gomega.Expect(containsStr(got, "forbid_generic_phrases")).To(gomega.BeTrue(), fmt.Sprintf("expected forbid_generic_phrases in assertions, got %v", got))
})

func containsStr(set []string, want string) bool {
	for _, v := range set {
		if v == want {
			return true
		}
	}

	return false
}

var _ = ginkgo.It("evaluate swe suite reports missing logs", func() {
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
	report := EvaluateSWESuiteWithLoadErrors(suite, nil, nil, nil)

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected missing task log to fail suite"))

	gomega.Expect(report.Coverage.Passed).To(gomega.BeTrue(), fmt.Sprintf("coverage should pass when no contract is set: %#v", report.Coverage))

	gomega.Expect(report.Summary.FailedTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected missing-log summary: %#v", report.Summary))
	gomega.Expect(report.Summary.MissingLogs).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected missing-log summary: %#v", report.Summary))

	{
		bucket := report.Summary.ByFamily["direct_question"]
		gomega.Expect(bucket.Total).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected family bucket: %#v", bucket))
		gomega.Expect(bucket.Failed).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected family bucket: %#v", bucket))
	}

	{
		bucket := report.Summary.ByTaskShape["read_only"]
		gomega.Expect(bucket.Total).To(gomega.BeEquivalentTo(0), fmt.Sprintf("unexpected task-shape bucket for untagged task: %#v", bucket))
		gomega.Expect(bucket.Failed).To(gomega.BeEquivalentTo(0), fmt.Sprintf("unexpected task-shape bucket for untagged task: %#v", bucket))
	}

	gomega.Expect(report.Tasks).To(gomega.HaveLen(1), fmt.Sprintf("unexpected suite report: %#v", report))
	gomega.Expect(report.Tasks[0].Passed).To(gomega.BeFalse(), fmt.Sprintf("unexpected suite report: %#v", report))
})
var _ = ginkgo.It("evaluate swe suite reports load errors", func() {
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

	gomega.Expect(report.Passed).To(gomega.BeFalse(), fmt.Sprint("expected load error to fail suite"))

	joined := strings.Join(report.Findings, "\n")
	gomega.Expect(joined).To(gomega.ContainSubstring("task log could not be loaded"), fmt.Sprintf("expected load error finding, got %q", joined))

	gomega.Expect(report.Summary.LoadErrors).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected load-error summary: %#v", report.Summary))
	gomega.Expect(report.Summary.FailedTasks).To(gomega.BeEquivalentTo(1), fmt.Sprintf("unexpected load-error summary: %#v", report.Summary))
})

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
