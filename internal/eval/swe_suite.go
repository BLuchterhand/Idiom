package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type SWESuite struct {
	Name     string              `json:"name"`
	Version  int                 `json:"version,omitempty"`
	Coverage SWECoverageContract `json:"coverage,omitempty"`
	Tasks    []SWETask           `json:"tasks"`
}

type SWETask struct {
	ID       string    `json:"id"`
	Family   string    `json:"family"`
	Prompt   string    `json:"prompt"`
	Turns    []string  `json:"turns,omitempty"`
	Tags     []string  `json:"tags,omitempty"`
	Fixture  string    `json:"fixture,omitempty"`
	LogPath  string    `json:"log_path,omitempty"`
	Scenario Scenario  `json:"scenario"`
	Oracle   SWEOracle `json:"oracle,omitempty"`
}

type SWEOracle struct {
	RequireNoChangedFiles       bool                        `json:"require_no_changed_files,omitempty"`
	RequireChangedFiles         []string                    `json:"require_changed_files,omitempty"`
	ForbidChangedFiles          []string                    `json:"forbid_changed_files,omitempty"`
	RequireFileContent          []SWEFileContentExpectation `json:"require_file_content,omitempty"`
	MaxToolRequests             *int                        `json:"max_tool_requests,omitempty"`
	MaxDurationMillis           *int64                      `json:"max_duration_millis,omitempty"`
	MaxErrors                   *int                        `json:"max_errors,omitempty"`
	MinUserTurns                *int                        `json:"min_user_turns,omitempty"`
	MinAssistantTurns           *int                        `json:"min_assistant_turns,omitempty"`
	RequireNoApproval           bool                        `json:"require_no_approval,omitempty"`
	RequireNoShell              bool                        `json:"require_no_shell,omitempty"`
	RequireNoSummaryRejections  bool                        `json:"require_no_summary_rejections,omitempty"`
	RequireSummaryRejectionLane []string                    `json:"require_summary_rejection_lane,omitempty"`
}

type SWEFileContentExpectation struct {
	Path           string   `json:"path"`
	Contains       []string `json:"contains,omitempty"`
	ForbidContains []string `json:"forbid_contains,omitempty"`
}

type SWECoverageContract struct {
	MinTasks                   *int           `json:"min_tasks,omitempty"`
	RequiredFamilies           []string       `json:"required_families,omitempty"`
	RequiredTags               []string       `json:"required_tags,omitempty"`
	RequiredFixtures           []string       `json:"required_fixtures,omitempty"`
	RequiredTaskShapes         []string       `json:"required_task_shapes,omitempty"`
	RequiredInitialLanes       []string       `json:"required_initial_lanes,omitempty"`
	RequiredFollowupLanes      []string       `json:"required_followup_lanes,omitempty"`
	RequiredRouteReasons       []string       `json:"required_route_reasons,omitempty"`
	RequiredContracts          []string       `json:"required_contracts,omitempty"`
	RequiredThoroughness       []string       `json:"required_thoroughness,omitempty"`
	RequiredVerification       []string       `json:"required_verification,omitempty"`
	RequiredScenarioAssertions []string       `json:"required_scenario_assertions,omitempty"`
	RequiredOracleAssertions   []string       `json:"required_oracle_assertions,omitempty"`
	RequiredReadinessActions   []string       `json:"required_readiness_actions,omitempty"`
	RequiredDeepeningActions   []string       `json:"required_deepening_actions,omitempty"`
	RequiredFinalCriticActions []string       `json:"required_final_critic_actions,omitempty"`
	RequiredEffortLevels       []string       `json:"required_effort_levels,omitempty"`
	RequiredLongHorizonActions []string       `json:"required_long_horizon_actions,omitempty"`
	RequiredLongHorizonKinds   []string       `json:"required_long_horizon_kinds,omitempty"`
	RequiredNegativeEvidence   []string       `json:"required_negative_evidence,omitempty"`
	RequiredTools              []string       `json:"required_tools,omitempty"`
	RequiredForbiddenTools     []string       `json:"required_forbidden_tools,omitempty"`
	RequiredFileSurfaces       []string       `json:"required_file_surfaces,omitempty"`
	RequiredEvidenceSurfaces   []string       `json:"required_evidence_surfaces,omitempty"`
	MinTasksPerFamily          map[string]int `json:"min_tasks_per_family,omitempty"`
}

type SWECoverageReport struct {
	Passed                           bool           `json:"passed"`
	TotalTasks                       int            `json:"total_tasks"`
	Families                         map[string]int `json:"families,omitempty"`
	Tags                             map[string]int `json:"tags,omitempty"`
	Fixtures                         map[string]int `json:"fixtures,omitempty"`
	TaskShapes                       map[string]int `json:"task_shapes,omitempty"`
	InitialLanes                     map[string]int `json:"initial_lanes,omitempty"`
	FollowupLanes                    map[string]int `json:"followup_lanes,omitempty"`
	RouteReasons                     map[string]int `json:"route_reasons,omitempty"`
	Contracts                        map[string]int `json:"contracts,omitempty"`
	Thoroughness                     map[string]int `json:"thoroughness,omitempty"`
	Verification                     map[string]int `json:"verification,omitempty"`
	ScenarioAssertions               map[string]int `json:"scenario_assertions,omitempty"`
	OracleAssertions                 map[string]int `json:"oracle_assertions,omitempty"`
	ReadinessActions                 map[string]int `json:"readiness_actions,omitempty"`
	DeepeningActions                 map[string]int `json:"deepening_actions,omitempty"`
	FinalCriticActions               map[string]int `json:"final_critic_actions,omitempty"`
	EffortLevels                     map[string]int `json:"effort_levels,omitempty"`
	LongHorizonActions               map[string]int `json:"long_horizon_actions,omitempty"`
	LongHorizonKinds                 map[string]int `json:"long_horizon_kinds,omitempty"`
	NegativeEvidence                 map[string]int `json:"negative_evidence,omitempty"`
	Tools                            map[string]int `json:"tools,omitempty"`
	ForbiddenTools                   map[string]int `json:"forbidden_tools,omitempty"`
	FileSurfaces                     map[string]int `json:"file_surfaces,omitempty"`
	EvidenceSurfaces                 map[string]int `json:"evidence_surfaces,omitempty"`
	MissingRequiredFamilies          []string       `json:"missing_required_families,omitempty"`
	MissingRequiredTags              []string       `json:"missing_required_tags,omitempty"`
	MissingRequiredFixtures          []string       `json:"missing_required_fixtures,omitempty"`
	MissingRequiredTaskShapes        []string       `json:"missing_required_task_shapes,omitempty"`
	MissingRequiredInitialLanes      []string       `json:"missing_required_initial_lanes,omitempty"`
	MissingRequiredFollowupLanes     []string       `json:"missing_required_followup_lanes,omitempty"`
	MissingRequiredRouteReasons      []string       `json:"missing_required_route_reasons,omitempty"`
	MissingRequiredContracts         []string       `json:"missing_required_contracts,omitempty"`
	MissingRequiredThoroughness      []string       `json:"missing_required_thoroughness,omitempty"`
	MissingRequiredVerification      []string       `json:"missing_required_verification,omitempty"`
	MissingRequiredScenarioAssertion []string       `json:"missing_required_scenario_assertions,omitempty"`
	MissingRequiredOracleAssertion   []string       `json:"missing_required_oracle_assertions,omitempty"`
	MissingRequiredReadinessActions  []string       `json:"missing_required_readiness_actions,omitempty"`
	MissingRequiredDeepeningActions  []string       `json:"missing_required_deepening_actions,omitempty"`
	MissingRequiredFinalCriticAction []string       `json:"missing_required_final_critic_actions,omitempty"`
	MissingRequiredEffortLevels      []string       `json:"missing_required_effort_levels,omitempty"`
	MissingRequiredLongHorizonAction []string       `json:"missing_required_long_horizon_actions,omitempty"`
	MissingRequiredLongHorizonKinds  []string       `json:"missing_required_long_horizon_kinds,omitempty"`
	MissingRequiredNegativeEvidence  []string       `json:"missing_required_negative_evidence,omitempty"`
	MissingRequiredTools             []string       `json:"missing_required_tools,omitempty"`
	MissingRequiredForbiddenTools    []string       `json:"missing_required_forbidden_tools,omitempty"`
	MissingRequiredFileSurfaces      []string       `json:"missing_required_file_surfaces,omitempty"`
	MissingRequiredEvidenceSurfaces  []string       `json:"missing_required_evidence_surfaces,omitempty"`
	UnderrepresentedFamily           []string       `json:"underrepresented_family,omitempty"`
	Findings                         []string       `json:"findings,omitempty"`
}

type SWEResultSummary struct {
	TotalTasks     int                        `json:"total_tasks"`
	PassedTasks    int                        `json:"passed_tasks"`
	FailedTasks    int                        `json:"failed_tasks"`
	MissingLogs    int                        `json:"missing_logs"`
	LoadErrors     int                        `json:"load_errors"`
	CoveragePassed bool                       `json:"coverage_passed"`
	LintPassed     bool                       `json:"lint_passed"`
	ByFamily       map[string]SWEResultBucket `json:"by_family,omitempty"`
	ByTaskShape    map[string]SWEResultBucket `json:"by_task_shape,omitempty"`
	ByFixture      map[string]SWEResultBucket `json:"by_fixture,omitempty"`
	ByInitialLane  map[string]SWEResultBucket `json:"by_initial_lane,omitempty"`
	ByRouteReason  map[string]SWEResultBucket `json:"by_route_reason,omitempty"`
	ByContract     map[string]SWEResultBucket `json:"by_contract,omitempty"`
	ByVerification map[string]SWEResultBucket `json:"by_verification,omitempty"`
	ByScenario     map[string]SWEResultBucket `json:"by_scenario,omitempty"`
	ByOracle       map[string]SWEResultBucket `json:"by_oracle,omitempty"`
	ByReadiness    map[string]SWEResultBucket `json:"by_readiness,omitempty"`
	ByDeepening    map[string]SWEResultBucket `json:"by_deepening,omitempty"`
	ByFinalCritic  map[string]SWEResultBucket `json:"by_final_critic,omitempty"`
	ByEffortLevel  map[string]SWEResultBucket `json:"by_effort_level,omitempty"`
	ByLongHorizon  map[string]SWEResultBucket `json:"by_long_horizon,omitempty"`
	ByNegative     map[string]SWEResultBucket `json:"by_negative_evidence,omitempty"`
	ByTool         map[string]SWEResultBucket `json:"by_tool,omitempty"`
	ByFileSurface  map[string]SWEResultBucket `json:"by_file_surface,omitempty"`
}

type SWEResultBucket struct {
	Total  int `json:"total"`
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type SWETaskReport struct {
	TaskID   string   `json:"task_id"`
	Family   string   `json:"family,omitempty"`
	LogPath  string   `json:"log_path,omitempty"`
	Passed   bool     `json:"passed"`
	Findings []string `json:"findings,omitempty"`
	Metrics  Metrics  `json:"metrics"`
}

type SWESuiteReport struct {
	Suite        string            `json:"suite"`
	Passed       bool              `json:"passed"`
	Coverage     SWECoverageReport `json:"coverage"`
	LintFindings []string          `json:"lint_findings,omitempty"`
	Summary      SWEResultSummary  `json:"summary"`
	Tasks        []SWETaskReport   `json:"tasks"`
	Findings     []string          `json:"findings,omitempty"`
}

func LoadSWESuite(path string) (SWESuite, error) {
	suite, err := ReadSWESuite(path)
	if err != nil {
		return suite, err
	}
	if err := ValidateSWESuite(suite); err != nil {
		return suite, err
	}
	if err := ValidateSWESuiteFixtures(suite, path); err != nil {
		return suite, err
	}
	return suite, nil
}

func ReadSWESuite(path string) (SWESuite, error) {
	var suite SWESuite
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return suite, fmt.Errorf("read SWE suite: %w", err)
	}
	if err := json.Unmarshal(data, &suite); err != nil {
		return suite, fmt.Errorf("decode SWE suite: %w", err)
	}
	return suite, nil
}

func ValidateSWESuite(suite SWESuite) error {
	if strings.TrimSpace(suite.Name) == "" {
		return fmt.Errorf("SWE suite missing name")
	}
	if len(suite.Tasks) == 0 {
		return fmt.Errorf("SWE suite %q has no tasks", suite.Name)
	}
	seen := make(map[string]struct{}, len(suite.Tasks))
	for i, task := range suite.Tasks {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			return fmt.Errorf("SWE suite task %d missing id", i)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("SWE suite task id %q is duplicated", id)
		}
		seen[id] = struct{}{}
		if strings.TrimSpace(task.Family) == "" {
			return fmt.Errorf("SWE suite task %q missing family", id)
		}
		if strings.TrimSpace(task.Prompt) == "" && len(task.Turns) == 0 {
			return fmt.Errorf("SWE suite task %q missing prompt or turns", id)
		}
		if strings.TrimSpace(task.Scenario.Name) == "" {
			return fmt.Errorf("SWE suite task %q missing scenario name", id)
		}
		if len(task.Scenario.PromptContains) == 0 && strings.TrimSpace(task.Prompt) != "" {
			return fmt.Errorf("SWE suite task %q scenario should assert prompt text", id)
		}
	}
	coverage := AnalyzeSWESuiteCoverage(suite)
	if !coverage.Passed {
		return fmt.Errorf("SWE suite %q coverage failed: %s", suite.Name, strings.Join(coverage.Findings, "; "))
	}
	if findings := LintSWESuite(suite); len(findings) > 0 {
		return fmt.Errorf("SWE suite %q lint failed: %s", suite.Name, strings.Join(findings, "; "))
	}
	return nil
}

func LintSWESuite(suite SWESuite) []string {
	findings := make([]string, 0)
	addFinding := func(task SWETask, format string, args ...any) {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			id = "<unnamed>"
		}
		findings = append(findings, fmt.Sprintf("%s: %s", id, fmt.Sprintf(format, args...)))
	}
	for _, task := range suite.Tasks {
		if err := validateFixturePathSyntax(task.Fixture); err != nil {
			addFinding(task, "fixture path is invalid: %v", err)
		}
		if taskHasTag(task, "read-only") && !task.Oracle.RequireNoChangedFiles {
			addFinding(task, "read-only task must require no changed files")
		}
		if taskHasTag(task, "negative-evidence") && !task.Oracle.RequireNoChangedFiles {
			addFinding(task, "negative-evidence task must require no changed files")
		}
		if taskHasAnyTag(task, "security", "adversarial") && !task.Oracle.RequireNoChangedFiles && len(task.Scenario.ForbidTools) == 0 {
			addFinding(task, "security/adversarial task must forbid writes or require no changed files")
		}
		if taskHasTag(task, "implement") && taskStartsInLane(task, "implement") && !taskRequiresChangedFilesOrApproval(task) {
			addFinding(task, "direct implementation task must require changed files or approval")
		}
		if len(task.Turns) > 0 && (task.Oracle.MinUserTurns == nil || *task.Oracle.MinUserTurns < len(task.Turns)) {
			addFinding(task, "multi-turn task must require at least %d user turns", len(task.Turns))
		}
		if len(task.Turns) > 0 && !task.Scenario.RequireAssistantForEachUser {
			addFinding(task, "multi-turn task must require an assistant response for each user turn")
		}
		if taskHasTag(task, "failure-repair") && task.Scenario.RequireVerification == "" {
			addFinding(task, "failure-repair task must assert verification outcome")
		}
		if taskHasTag(task, "tool-error") && task.Oracle.MaxErrors == nil {
			addFinding(task, "tool-error task must bound errors")
		}
		for _, expected := range task.Oracle.RequireFileContent {
			if err := validateFixturePathSyntax(expected.Path); err != nil {
				addFinding(task, "file content oracle path is invalid: %v", err)
			}
			if len(expected.Contains) == 0 && len(expected.ForbidContains) == 0 {
				addFinding(task, "file content oracle for %q must assert required or forbidden text", expected.Path)
			}
		}
	}
	return findings
}

func ValidateSWESuiteFixtures(suite SWESuite, suitePath string) error {
	findings := LintSWESuiteFixtures(suite, suitePath)
	if len(findings) > 0 {
		return fmt.Errorf("%s", strings.Join(findings, "; "))
	}
	return nil
}

func LintSWESuiteFixtures(suite SWESuite, suitePath string) []string {
	findings := make([]string, 0)
	baseDir := filepath.Dir(filepath.Clean(suitePath))
	for _, task := range suite.Tasks {
		fixture := strings.TrimSpace(task.Fixture)
		if fixture == "" {
			continue
		}
		if err := validateFixturePathSyntax(fixture); err != nil {
			findings = append(findings, fmt.Sprintf("%s: fixture invalid: %v", task.ID, err))
			continue
		}
		fixturePath := filepath.Join(baseDir, filepath.Clean(fixture))
		stat, err := os.Stat(fixturePath)
		if err != nil {
			findings = append(findings, fmt.Sprintf("%s: fixture missing: %v", task.ID, err))
			continue
		}
		if !stat.IsDir() {
			findings = append(findings, fmt.Sprintf("%s: fixture is not a directory: %s", task.ID, fixturePath))
		}
	}
	return findings
}

func EvaluateSWESuiteDefinitionWithFixtures(suite SWESuite, suitePath string) SWESuiteReport {
	report := EvaluateSWESuiteDefinition(suite)
	fixtureFindings := LintSWESuiteFixtures(suite, suitePath)
	if len(fixtureFindings) == 0 {
		return report
	}
	report.Passed = false
	report.LintFindings = append(report.LintFindings, fixtureFindings...)
	report.Findings = append(report.Findings, prefixedFindings("fixture", fixtureFindings)...)
	report.Summary.LintPassed = false
	return report
}

func AnalyzeSWESuiteCoverage(suite SWESuite) SWECoverageReport {
	report := SWECoverageReport{
		Passed:             true,
		TotalTasks:         len(suite.Tasks),
		Families:           make(map[string]int),
		Tags:               make(map[string]int),
		Fixtures:           make(map[string]int),
		TaskShapes:         make(map[string]int),
		InitialLanes:       make(map[string]int),
		FollowupLanes:      make(map[string]int),
		RouteReasons:       make(map[string]int),
		Contracts:          make(map[string]int),
		Thoroughness:       make(map[string]int),
		Verification:       make(map[string]int),
		ScenarioAssertions: make(map[string]int),
		OracleAssertions:   make(map[string]int),
		ReadinessActions:   make(map[string]int),
		DeepeningActions:   make(map[string]int),
		FinalCriticActions: make(map[string]int),
		EffortLevels:       make(map[string]int),
		LongHorizonActions: make(map[string]int),
		LongHorizonKinds:   make(map[string]int),
		NegativeEvidence:   make(map[string]int),
		Tools:              make(map[string]int),
		ForbiddenTools:     make(map[string]int),
		FileSurfaces:       make(map[string]int),
		EvidenceSurfaces:   make(map[string]int),
	}
	for _, task := range suite.Tasks {
		family := normalizedCoverageKey(task.Family)
		if family != "" {
			report.Families[family]++
		}
		for _, tag := range task.Tags {
			tag = normalizedCoverageKey(tag)
			if tag != "" {
				report.Tags[tag]++
			}
		}
		if fixture := normalizedFixturePath(task.Fixture); fixture != "" {
			report.Fixtures[fixture]++
		}
		for _, shape := range taskShapes(task) {
			report.TaskShapes[shape]++
		}
		if task.Scenario.RequireRoutePlan != nil {
			if lane := normalizedCoverageKey(task.Scenario.RequireRoutePlan.InitialLane); lane != "" {
				report.InitialLanes[lane]++
			}
			if lane := normalizedCoverageKey(task.Scenario.RequireRoutePlan.FollowupLane); lane != "" {
				report.FollowupLanes[lane]++
			}
			if reason := normalizedCoverageKey(task.Scenario.RequireRoutePlan.Reason); reason != "" {
				report.RouteReasons[reason]++
			}
			if contract := normalizedCoverageKey(task.Scenario.RequireRoutePlan.Contract); contract != "" {
				report.Contracts[contract]++
			}
			if thoroughness := normalizedCoverageKey(task.Scenario.RequireRoutePlan.Thoroughness); thoroughness != "" {
				report.Thoroughness[thoroughness]++
			}
		}
		if verification := normalizedVerification(task.Scenario.RequireVerification); verification != "" {
			report.Verification[verification]++
		}
		for _, assertion := range taskScenarioAssertions(task) {
			report.ScenarioAssertions[assertion]++
		}
		for _, assertion := range taskOracleAssertions(task) {
			report.OracleAssertions[assertion]++
		}
		if action := taskReadinessAction(task); action != "" {
			report.ReadinessActions[action]++
		}
		if action := taskDeepeningAction(task); action != "" {
			report.DeepeningActions[action]++
		}
		if action := taskFinalCriticAction(task); action != "" {
			report.FinalCriticActions[action]++
		}
		if level := taskEffortLevel(task); level != "" {
			report.EffortLevels[level]++
		}
		if action := taskLongHorizonAction(task); action != "" {
			report.LongHorizonActions[action]++
		}
		if kind := taskLongHorizonKind(task); kind != "" {
			report.LongHorizonKinds[kind]++
		}
		if evidence := taskNegativeEvidence(task); evidence != "" {
			report.NegativeEvidence[evidence]++
		}
		for _, tool := range task.Scenario.RequireTools {
			if tool = normalizedCoverageKey(tool); tool != "" {
				report.Tools[tool]++
			}
		}
		for _, tool := range task.Scenario.ForbidTools {
			if tool = normalizedCoverageKey(tool); tool != "" {
				report.ForbiddenTools[tool]++
			}
		}
		for _, surface := range taskFileSurfaces(task) {
			report.FileSurfaces[surface]++
		}
		for _, surface := range evidenceSurfaces(task.Scenario.RequireEvidencePaths) {
			report.EvidenceSurfaces[surface]++
		}
	}

	contract := suite.Coverage
	addFinding := func(format string, args ...any) {
		report.Passed = false
		report.Findings = append(report.Findings, fmt.Sprintf(format, args...))
	}
	if contract.MinTasks != nil && report.TotalTasks < *contract.MinTasks {
		addFinding("task count below coverage minimum: got %d want >= %d", report.TotalTasks, *contract.MinTasks)
	}
	for _, family := range contract.RequiredFamilies {
		family = normalizedCoverageKey(family)
		if family == "" {
			continue
		}
		if report.Families[family] == 0 {
			report.MissingRequiredFamilies = append(report.MissingRequiredFamilies, family)
			addFinding("required family %q is missing", family)
		}
	}
	for _, tag := range contract.RequiredTags {
		tag = normalizedCoverageKey(tag)
		if tag == "" {
			continue
		}
		if report.Tags[tag] == 0 {
			report.MissingRequiredTags = append(report.MissingRequiredTags, tag)
			addFinding("required tag %q is missing", tag)
		}
	}
	for _, fixture := range contract.RequiredFixtures {
		fixture = normalizedFixturePath(fixture)
		if fixture == "" {
			continue
		}
		if report.Fixtures[fixture] == 0 {
			report.MissingRequiredFixtures = append(report.MissingRequiredFixtures, fixture)
			addFinding("required fixture %q is missing", fixture)
		}
	}
	for _, shape := range contract.RequiredTaskShapes {
		shape = normalizedCoverageKey(shape)
		if shape == "" {
			continue
		}
		if report.TaskShapes[shape] == 0 {
			report.MissingRequiredTaskShapes = append(report.MissingRequiredTaskShapes, shape)
			addFinding("required task shape %q is missing", shape)
		}
	}
	for _, lane := range contract.RequiredInitialLanes {
		lane = normalizedCoverageKey(lane)
		if lane == "" {
			continue
		}
		if report.InitialLanes[lane] == 0 {
			report.MissingRequiredInitialLanes = append(report.MissingRequiredInitialLanes, lane)
			addFinding("required initial lane %q is missing", lane)
		}
	}
	for _, lane := range contract.RequiredFollowupLanes {
		lane = normalizedCoverageKey(lane)
		if lane == "" {
			continue
		}
		if report.FollowupLanes[lane] == 0 {
			report.MissingRequiredFollowupLanes = append(report.MissingRequiredFollowupLanes, lane)
			addFinding("required follow-up lane %q is missing", lane)
		}
	}
	for _, reason := range contract.RequiredRouteReasons {
		reason = normalizedCoverageKey(reason)
		if reason == "" {
			continue
		}
		if report.RouteReasons[reason] == 0 {
			report.MissingRequiredRouteReasons = append(report.MissingRequiredRouteReasons, reason)
			addFinding("required route reason %q is missing", reason)
		}
	}
	for _, answerContract := range contract.RequiredContracts {
		answerContract = normalizedCoverageKey(answerContract)
		if answerContract == "" {
			continue
		}
		if report.Contracts[answerContract] == 0 {
			report.MissingRequiredContracts = append(report.MissingRequiredContracts, answerContract)
			addFinding("required answer contract %q is missing", answerContract)
		}
	}
	for _, thoroughness := range contract.RequiredThoroughness {
		thoroughness = normalizedCoverageKey(thoroughness)
		if thoroughness == "" {
			continue
		}
		if report.Thoroughness[thoroughness] == 0 {
			report.MissingRequiredThoroughness = append(report.MissingRequiredThoroughness, thoroughness)
			addFinding("required thoroughness %q is missing", thoroughness)
		}
	}
	for _, verification := range contract.RequiredVerification {
		verification = normalizedVerification(verification)
		if verification == "" {
			continue
		}
		if report.Verification[verification] == 0 {
			report.MissingRequiredVerification = append(report.MissingRequiredVerification, verification)
			addFinding("required verification state %q is missing", verification)
		}
	}
	for _, assertion := range contract.RequiredScenarioAssertions {
		assertion = normalizedCoverageKey(assertion)
		if assertion == "" {
			continue
		}
		if report.ScenarioAssertions[assertion] == 0 {
			report.MissingRequiredScenarioAssertion = append(report.MissingRequiredScenarioAssertion, assertion)
			addFinding("required scenario assertion %q is missing", assertion)
		}
	}
	for _, assertion := range contract.RequiredOracleAssertions {
		assertion = normalizedCoverageKey(assertion)
		if assertion == "" {
			continue
		}
		if report.OracleAssertions[assertion] == 0 {
			report.MissingRequiredOracleAssertion = append(report.MissingRequiredOracleAssertion, assertion)
			addFinding("required oracle assertion %q is missing", assertion)
		}
	}
	for _, action := range contract.RequiredReadinessActions {
		action = normalizedCoverageKey(action)
		if action == "" {
			continue
		}
		if report.ReadinessActions[action] == 0 {
			report.MissingRequiredReadinessActions = append(report.MissingRequiredReadinessActions, action)
			addFinding("required readiness action %q is missing", action)
		}
	}
	for _, action := range contract.RequiredDeepeningActions {
		action = normalizedCoverageKey(action)
		if action == "" {
			continue
		}
		if report.DeepeningActions[action] == 0 {
			report.MissingRequiredDeepeningActions = append(report.MissingRequiredDeepeningActions, action)
			addFinding("required deepening action %q is missing", action)
		}
	}
	for _, action := range contract.RequiredFinalCriticActions {
		action = normalizedCoverageKey(action)
		if action == "" {
			continue
		}
		if report.FinalCriticActions[action] == 0 {
			report.MissingRequiredFinalCriticAction = append(report.MissingRequiredFinalCriticAction, action)
			addFinding("required final critic action %q is missing", action)
		}
	}
	for _, level := range contract.RequiredEffortLevels {
		level = normalizedCoverageKey(level)
		if level == "" {
			continue
		}
		if report.EffortLevels[level] == 0 {
			report.MissingRequiredEffortLevels = append(report.MissingRequiredEffortLevels, level)
			addFinding("required effort level %q is missing", level)
		}
	}
	for _, action := range contract.RequiredLongHorizonActions {
		action = normalizedCoverageKey(action)
		if action == "" {
			continue
		}
		if report.LongHorizonActions[action] == 0 {
			report.MissingRequiredLongHorizonAction = append(report.MissingRequiredLongHorizonAction, action)
			addFinding("required long-horizon action %q is missing", action)
		}
	}
	for _, kind := range contract.RequiredLongHorizonKinds {
		kind = normalizedCoverageKey(kind)
		if kind == "" {
			continue
		}
		if report.LongHorizonKinds[kind] == 0 {
			report.MissingRequiredLongHorizonKinds = append(report.MissingRequiredLongHorizonKinds, kind)
			addFinding("required long-horizon kind %q is missing", kind)
		}
	}
	for _, evidence := range contract.RequiredNegativeEvidence {
		evidence = normalizedCoverageKey(evidence)
		if evidence == "" {
			continue
		}
		if report.NegativeEvidence[evidence] == 0 {
			report.MissingRequiredNegativeEvidence = append(report.MissingRequiredNegativeEvidence, evidence)
			addFinding("required negative-evidence outcome %q is missing", evidence)
		}
	}
	for _, tool := range contract.RequiredTools {
		tool = normalizedCoverageKey(tool)
		if tool == "" {
			continue
		}
		if report.Tools[tool] == 0 {
			report.MissingRequiredTools = append(report.MissingRequiredTools, tool)
			addFinding("required tool %q is missing", tool)
		}
	}
	for _, tool := range contract.RequiredForbiddenTools {
		tool = normalizedCoverageKey(tool)
		if tool == "" {
			continue
		}
		if report.ForbiddenTools[tool] == 0 {
			report.MissingRequiredForbiddenTools = append(report.MissingRequiredForbiddenTools, tool)
			addFinding("required forbidden tool %q is missing", tool)
		}
	}
	for _, surface := range contract.RequiredFileSurfaces {
		surface = normalizedCoverageKey(surface)
		if surface == "" {
			continue
		}
		if report.FileSurfaces[surface] == 0 {
			report.MissingRequiredFileSurfaces = append(report.MissingRequiredFileSurfaces, surface)
			addFinding("required file surface %q is missing", surface)
		}
	}
	for _, surface := range contract.RequiredEvidenceSurfaces {
		surface = normalizedCoverageKey(surface)
		if surface == "" {
			continue
		}
		if report.EvidenceSurfaces[surface] == 0 {
			report.MissingRequiredEvidenceSurfaces = append(report.MissingRequiredEvidenceSurfaces, surface)
			addFinding("required evidence surface %q is missing", surface)
		}
	}
	for family, minCount := range contract.MinTasksPerFamily {
		family = normalizedCoverageKey(family)
		if family == "" || minCount <= 0 {
			continue
		}
		got := report.Families[family]
		if got < minCount {
			report.UnderrepresentedFamily = append(report.UnderrepresentedFamily, fmt.Sprintf("%s:%d/%d", family, got, minCount))
			addFinding("family %q below coverage minimum: got %d want >= %d", family, got, minCount)
		}
	}
	return report
}

func EvaluateSWETask(logPath string, records []Record, task SWETask) SWETaskReport {
	scenarioReport := Evaluate(logPath, records, task.Scenario)
	report := SWETaskReport{
		TaskID:   strings.TrimSpace(task.ID),
		Family:   strings.TrimSpace(task.Family),
		LogPath:  logPath,
		Passed:   scenarioReport.Passed,
		Findings: append([]string(nil), scenarioReport.Findings...),
		Metrics:  scenarioReport.Metrics,
	}
	addFinding := func(format string, args ...any) {
		report.Passed = false
		report.Findings = append(report.Findings, fmt.Sprintf(format, args...))
	}
	oracle := task.Oracle
	if oracle.RequireNoChangedFiles && len(report.Metrics.ChangedFiles) > 0 {
		addFinding("changed files were forbidden, got %v", report.Metrics.ChangedFiles)
	}
	for _, required := range oracle.RequireChangedFiles {
		if !containsStringFold(report.Metrics.ChangedFiles, required) {
			addFinding("oracle changed files do not include %q", required)
		}
	}
	for _, forbidden := range oracle.ForbidChangedFiles {
		if containsStringFold(report.Metrics.ChangedFiles, forbidden) {
			addFinding("oracle changed files unexpectedly include %q", forbidden)
		}
	}
	for _, expected := range oracle.RequireFileContent {
		for _, finding := range evaluateFileContentOracle(report.Metrics.WorkspaceRoot, expected) {
			addFinding("%s", finding)
		}
	}
	if oracle.MaxToolRequests != nil && report.Metrics.ToolRequests > *oracle.MaxToolRequests {
		addFinding("tool requests exceeded oracle limit: got %d want <= %d", report.Metrics.ToolRequests, *oracle.MaxToolRequests)
	}
	if oracle.MaxDurationMillis != nil && report.Metrics.DurationMillis > *oracle.MaxDurationMillis {
		addFinding("duration exceeded oracle limit: got %dms want <= %dms", report.Metrics.DurationMillis, *oracle.MaxDurationMillis)
	}
	if oracle.MaxErrors != nil && len(report.Metrics.Errors) > *oracle.MaxErrors {
		addFinding("errors exceeded oracle limit: got %d want <= %d", len(report.Metrics.Errors), *oracle.MaxErrors)
	}
	if oracle.MinUserTurns != nil && report.Metrics.UserTurns < *oracle.MinUserTurns {
		addFinding("user turns below oracle minimum: got %d want >= %d", report.Metrics.UserTurns, *oracle.MinUserTurns)
	}
	if oracle.MinAssistantTurns != nil && report.Metrics.AssistantTurns < *oracle.MinAssistantTurns {
		addFinding("assistant turns below oracle minimum: got %d want >= %d", report.Metrics.AssistantTurns, *oracle.MinAssistantTurns)
	}
	if oracle.RequireNoApproval && report.Metrics.ApprovalRequired {
		addFinding("approval was forbidden but required by run")
	}
	if oracle.RequireNoShell && report.Metrics.Tools["shell"] > 0 {
		addFinding("shell use was forbidden but observed %d shell request(s)", report.Metrics.Tools["shell"])
	}
	if oracle.RequireNoSummaryRejections && report.Metrics.SummaryRejections > 0 {
		addFinding("summary rejections were forbidden but observed %d", report.Metrics.SummaryRejections)
	}
	for _, lane := range oracle.RequireSummaryRejectionLane {
		if !containsStringFold(report.Metrics.SummaryRejectionLanes, lane) {
			addFinding("summary rejection lanes do not include %q", lane)
		}
	}
	return report
}

func evaluateFileContentOracle(workspaceRoot string, expected SWEFileContentExpectation) []string {
	findings := make([]string, 0)
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return []string{fmt.Sprintf("file content oracle for %q has no workspace root in log", expected.Path)}
	}
	path := filepath.Clean(strings.TrimSpace(expected.Path))
	if path == "" || path == "." {
		return []string{"file content oracle has empty path"}
	}
	if filepath.IsAbs(path) {
		return []string{fmt.Sprintf("file content oracle path %q must be relative", expected.Path)}
	}
	fullPath := filepath.Clean(filepath.Join(root, path))
	rel, err := filepath.Rel(root, fullPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{fmt.Sprintf("file content oracle path %q escapes workspace root", expected.Path)}
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return []string{fmt.Sprintf("file content oracle could not read %q: %v", path, err)}
	}
	content := string(data)
	for _, required := range expected.Contains {
		required = strings.TrimSpace(required)
		if required != "" && !strings.Contains(content, required) {
			findings = append(findings, fmt.Sprintf("file %q does not contain required text %q", path, required))
		}
	}
	for _, forbidden := range expected.ForbidContains {
		forbidden = strings.TrimSpace(forbidden)
		if forbidden != "" && strings.Contains(content, forbidden) {
			findings = append(findings, fmt.Sprintf("file %q unexpectedly contains %q", path, forbidden))
		}
	}
	return findings
}

func EvaluateSWESuite(suite SWESuite, recordsByTask map[string][]Record, logPaths map[string]string) SWESuiteReport {
	return EvaluateSWESuiteWithLoadErrors(suite, recordsByTask, logPaths, nil)
}

func EvaluateSWESuiteDefinition(suite SWESuite) SWESuiteReport {
	coverage := AnalyzeSWESuiteCoverage(suite)
	lintFindings := LintSWESuite(suite)
	summary := newSWEResultSummary(suite, coverage, lintFindings)
	return SWESuiteReport{
		Suite:        suite.Name,
		Passed:       coverage.Passed && len(lintFindings) == 0,
		Coverage:     coverage,
		LintFindings: lintFindings,
		Summary:      summary,
		Findings:     append(prefixedFindings("coverage", coverage.Findings), prefixedFindings("lint", lintFindings)...),
	}
}

func EvaluateSWESuiteWithLoadErrors(suite SWESuite, recordsByTask map[string][]Record, logPaths map[string]string, loadErrors map[string]error) SWESuiteReport {
	coverage := AnalyzeSWESuiteCoverage(suite)
	lintFindings := LintSWESuite(suite)
	summary := newSWEResultSummary(suite, coverage, lintFindings)
	report := SWESuiteReport{
		Suite:        suite.Name,
		Passed:       coverage.Passed && len(lintFindings) == 0,
		Coverage:     coverage,
		LintFindings: lintFindings,
		Summary:      summary,
		Tasks:        make([]SWETaskReport, 0, len(suite.Tasks)),
		Findings:     append(prefixedFindings("coverage", coverage.Findings), prefixedFindings("lint", lintFindings)...),
	}
	for _, task := range suite.Tasks {
		id := strings.TrimSpace(task.ID)
		if err := loadErrors[id]; err != nil {
			taskReport := SWETaskReport{
				TaskID:   id,
				Family:   task.Family,
				LogPath:  logPaths[id],
				Passed:   false,
				Findings: []string{fmt.Sprintf("task log could not be loaded: %v", err)},
				Metrics:  Metrics{Tools: map[string]int{}},
			}
			report.Passed = false
			addTaskOutcome(&report.Summary, task, false)
			report.Summary.LoadErrors++
			report.Tasks = append(report.Tasks, taskReport)
			report.Findings = append(report.Findings, fmt.Sprintf("%s: task log could not be loaded: %v", id, err))
			continue
		}
		records, ok := recordsByTask[id]
		if !ok {
			taskReport := SWETaskReport{
				TaskID:   id,
				Family:   task.Family,
				LogPath:  logPaths[id],
				Passed:   false,
				Findings: []string{"task log was not provided"},
				Metrics:  Metrics{Tools: map[string]int{}},
			}
			report.Passed = false
			addTaskOutcome(&report.Summary, task, false)
			report.Summary.MissingLogs++
			report.Tasks = append(report.Tasks, taskReport)
			report.Findings = append(report.Findings, fmt.Sprintf("%s: task log was not provided", id))
			continue
		}
		taskReport := EvaluateSWETask(logPaths[id], records, task)
		addTaskOutcome(&report.Summary, task, taskReport.Passed)
		if !taskReport.Passed {
			report.Passed = false
			for _, finding := range taskReport.Findings {
				report.Findings = append(report.Findings, fmt.Sprintf("%s: %s", id, finding))
			}
		}
		report.Tasks = append(report.Tasks, taskReport)
	}
	return report
}

func newSWEResultSummary(suite SWESuite, coverage SWECoverageReport, lintFindings []string) SWEResultSummary {
	return SWEResultSummary{
		TotalTasks:     len(suite.Tasks),
		CoveragePassed: coverage.Passed,
		LintPassed:     len(lintFindings) == 0,
		ByFamily:       make(map[string]SWEResultBucket),
		ByTaskShape:    make(map[string]SWEResultBucket),
		ByFixture:      make(map[string]SWEResultBucket),
		ByInitialLane:  make(map[string]SWEResultBucket),
		ByRouteReason:  make(map[string]SWEResultBucket),
		ByContract:     make(map[string]SWEResultBucket),
		ByVerification: make(map[string]SWEResultBucket),
		ByScenario:     make(map[string]SWEResultBucket),
		ByOracle:       make(map[string]SWEResultBucket),
		ByReadiness:    make(map[string]SWEResultBucket),
		ByDeepening:    make(map[string]SWEResultBucket),
		ByFinalCritic:  make(map[string]SWEResultBucket),
		ByEffortLevel:  make(map[string]SWEResultBucket),
		ByLongHorizon:  make(map[string]SWEResultBucket),
		ByNegative:     make(map[string]SWEResultBucket),
		ByTool:         make(map[string]SWEResultBucket),
		ByFileSurface:  make(map[string]SWEResultBucket),
	}
}

func addTaskOutcome(summary *SWEResultSummary, task SWETask, passed bool) {
	if passed {
		summary.PassedTasks++
	} else {
		summary.FailedTasks++
	}
	addOutcomeBucket(summary.ByFamily, normalizedCoverageKey(task.Family), passed)
	for _, shape := range taskShapes(task) {
		addOutcomeBucket(summary.ByTaskShape, shape, passed)
	}
	addOutcomeBucket(summary.ByFixture, normalizedFixturePath(task.Fixture), passed)
	if task.Scenario.RequireRoutePlan != nil {
		addOutcomeBucket(summary.ByInitialLane, normalizedCoverageKey(task.Scenario.RequireRoutePlan.InitialLane), passed)
		addOutcomeBucket(summary.ByRouteReason, normalizedCoverageKey(task.Scenario.RequireRoutePlan.Reason), passed)
		addOutcomeBucket(summary.ByContract, normalizedCoverageKey(task.Scenario.RequireRoutePlan.Contract), passed)
	}
	addOutcomeBucket(summary.ByVerification, normalizedVerification(task.Scenario.RequireVerification), passed)
	for _, assertion := range taskScenarioAssertions(task) {
		addOutcomeBucket(summary.ByScenario, assertion, passed)
	}
	for _, assertion := range taskOracleAssertions(task) {
		addOutcomeBucket(summary.ByOracle, assertion, passed)
	}
	addOutcomeBucket(summary.ByReadiness, taskReadinessAction(task), passed)
	addOutcomeBucket(summary.ByDeepening, taskDeepeningAction(task), passed)
	addOutcomeBucket(summary.ByFinalCritic, taskFinalCriticAction(task), passed)
	addOutcomeBucket(summary.ByEffortLevel, taskEffortLevel(task), passed)
	addOutcomeBucket(summary.ByLongHorizon, taskLongHorizonAction(task), passed)
	addOutcomeBucket(summary.ByNegative, taskNegativeEvidence(task), passed)
	for _, tool := range task.Scenario.RequireTools {
		addOutcomeBucket(summary.ByTool, normalizedCoverageKey(tool), passed)
	}
	for _, surface := range taskFileSurfaces(task) {
		addOutcomeBucket(summary.ByFileSurface, surface, passed)
	}
}

func addOutcomeBucket(buckets map[string]SWEResultBucket, key string, passed bool) {
	if key == "" {
		return
	}
	bucket := buckets[key]
	bucket.Total++
	if passed {
		bucket.Passed++
	} else {
		bucket.Failed++
	}
	buckets[key] = bucket
}

func prefixedFindings(prefix string, findings []string) []string {
	if len(findings) == 0 {
		return nil
	}
	out := make([]string, 0, len(findings))
	for _, finding := range findings {
		out = append(out, fmt.Sprintf("%s: %s", prefix, finding))
	}
	return out
}

func normalizedCoverageKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizedFixturePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Clean(value))
}

func validateFixturePathSyntax(fixture string) error {
	fixture = strings.TrimSpace(fixture)
	if fixture == "" {
		return nil
	}
	if filepath.IsAbs(fixture) {
		return fmt.Errorf("must be relative")
	}
	clean := filepath.Clean(fixture)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("must stay under the suite directory")
	}
	return nil
}

func normalizedVerification(value string) string {
	value = normalizedCoverageKey(value)
	if value == "" {
		return ""
	}
	return value
}

func taskShapes(task SWETask) []string {
	shapes := make([]string, 0, 12)
	if taskHasTag(task, "read-only") || task.Oracle.RequireNoChangedFiles {
		shapes = append(shapes, "read_only")
	}
	if strings.TrimSpace(task.Fixture) != "" {
		shapes = append(shapes, "fixture_backed")
	}
	if len(task.Turns) > 0 {
		shapes = append(shapes, "multi_turn")
	}
	if task.Scenario.RequireApproval != nil && *task.Scenario.RequireApproval {
		shapes = append(shapes, "protected_approval")
	}
	if taskStartsInLane(task, "implement") {
		shapes = append(shapes, "direct_implementation")
	}
	if task.Scenario.RequireRoutePlan != nil && strings.TrimSpace(task.Scenario.RequireRoutePlan.FollowupLane) != "" {
		shapes = append(shapes, "analysis_then_implementation")
	}
	if strings.EqualFold(task.Family, "verification_only") {
		shapes = append(shapes, "verification_only")
	}
	if normalizedVerification(task.Scenario.RequireVerification) == "failed" {
		shapes = append(shapes, "expected_failure")
	}
	if taskHasTag(task, "failure-repair") {
		shapes = append(shapes, "failure_repair")
	}
	if taskHasTag(task, "tool-error") {
		shapes = append(shapes, "tool_error_recovery")
	}
	if taskHasAnyTag(task, "rollback", "undo") {
		shapes = append(shapes, "rollback")
	}
	if taskHasAnyTag(task, "security", "adversarial") {
		shapes = append(shapes, "security_adversarial")
	}
	if taskHasAnyTag(task, "negative-evidence", "no-match", "no-bug-found") {
		shapes = append(shapes, "negative_evidence")
	}
	if taskHasAnyTag(task, "docs", "config", "shell", "tests", "prompts", "go") {
		shapes = append(shapes, "surface_specific_edit")
	}
	return uniqueNonEmptyStrings(shapes)
}

func taskScenarioAssertions(task SWETask) []string {
	assertions := make([]string, 0, 16)
	scenario := task.Scenario
	if len(scenario.PromptContains) > 0 {
		assertions = append(assertions, "prompt_contains")
	}
	if len(scenario.FinalAssistantContains) > 0 {
		assertions = append(assertions, "final_assistant_contains")
	}
	if len(scenario.ForbidFinalAssistantContains) > 0 {
		assertions = append(assertions, "forbid_final_assistant_contains")
	}
	if len(scenario.RequireTools) > 0 {
		assertions = append(assertions, "tools")
	}
	if len(scenario.ForbidTools) > 0 {
		assertions = append(assertions, "forbid_tools")
	}
	if len(scenario.RequirePhases) > 0 {
		assertions = append(assertions, "phases")
	}
	if scenario.RequireAssistantForEachUser {
		assertions = append(assertions, "assistant_for_each_user")
	}
	if scenario.RequireRoutePlan != nil {
		assertions = append(assertions, "route_plan")
	}
	if scenario.RequireReplan != nil {
		assertions = append(assertions, "replan")
	}
	if scenario.MaxBroadToolRequests != nil {
		assertions = append(assertions, "broad_tool_budget")
	}
	if scenario.MaxRepeatedDeadEnds != nil {
		assertions = append(assertions, "repeated_dead_end_budget")
	}
	if scenario.MaxSummaryRejections != nil {
		assertions = append(assertions, "summary_rejection_budget")
	}
	if strings.TrimSpace(scenario.RequireVerification) != "" {
		assertions = append(assertions, "verification_expected")
	}
	if scenario.RequireVerificationPlan != nil {
		assertions = append(assertions, "verification_plan")
	}
	if scenario.RequireReadiness != nil {
		assertions = append(assertions, "readiness")
	}
	if scenario.RequireDeepening != nil {
		assertions = append(assertions, "deepening")
	}
	if scenario.RequireFinalAnswerCritic != nil {
		assertions = append(assertions, "final_answer_critic")
	}
	if scenario.RequireEffortPolicy != nil {
		assertions = append(assertions, "effort_policy")
	}
	if scenario.RequireLongHorizonState != nil {
		assertions = append(assertions, "long_horizon_state")
	}
	if scenario.RequireWorkflowState != nil {
		assertions = append(assertions, "workflow_state")
	}
	if scenario.RequireBudgetLedger != nil {
		assertions = append(assertions, "budget_ledger")
	}
	if scenario.RequireEvidenceDelta != nil {
		assertions = append(assertions, "evidence_delta")
	}
	if scenario.RequireClaimConfidence != nil {
		assertions = append(assertions, "claim_confidence")
	}
	if scenario.RequireContextCompaction != nil {
		assertions = append(assertions, "context_compaction")
	}
	if scenario.RequireRouteAmbiguity != nil {
		assertions = append(assertions, "route_ambiguity")
	}
	if scenario.RequireVerificationFeedback != nil {
		assertions = append(assertions, "verification_feedback")
	}
	if scenario.RequireToolFailureConfidence != nil {
		assertions = append(assertions, "tool_failure_confidence")
	}
	if scenario.RequireControlRails != nil {
		assertions = append(assertions, "control_rails")
	}
	if len(scenario.RequireChangedFiles) > 0 {
		assertions = append(assertions, "changed_files")
	}
	if len(scenario.RequireEvidencePaths) > 0 {
		assertions = append(assertions, "evidence_paths")
	}
	if len(scenario.RequireLanguageCapabilities) > 0 {
		assertions = append(assertions, "language_capabilities")
	}
	if scenario.RequireApproval != nil {
		assertions = append(assertions, "approval_expected")
	}
	return uniqueNonEmptyStrings(assertions)
}

func taskReadinessAction(task SWETask) string {
	if task.Scenario.RequireReadiness == nil {
		return ""
	}
	if action := normalizedCoverageKey(task.Scenario.RequireReadiness.Action); action != "" {
		return action
	}
	if task.Scenario.RequireReadiness.Ready != nil {
		if *task.Scenario.RequireReadiness.Ready {
			return "ready"
		}
		return "not_ready"
	}
	return ""
}

func taskDeepeningAction(task SWETask) string {
	if task.Scenario.RequireDeepening == nil {
		return ""
	}
	if action := normalizedCoverageKey(task.Scenario.RequireDeepening.Action); action != "" {
		return action
	}
	if task.Scenario.RequireDeepening.Attempted != nil {
		if *task.Scenario.RequireDeepening.Attempted {
			return "attempted"
		}
		return "not_attempted"
	}
	return ""
}

func taskFinalCriticAction(task SWETask) string {
	if task.Scenario.RequireFinalAnswerCritic == nil {
		return ""
	}
	if action := normalizedCoverageKey(task.Scenario.RequireFinalAnswerCritic.Action); action != "" {
		return action
	}
	if task.Scenario.RequireFinalAnswerCritic.Passed != nil {
		if *task.Scenario.RequireFinalAnswerCritic.Passed {
			return "passed"
		}
		return "failed"
	}
	return ""
}

func taskEffortLevel(task SWETask) string {
	if task.Scenario.RequireEffortPolicy != nil {
		return normalizedCoverageKey(task.Scenario.RequireEffortPolicy.Level)
	}
	if task.Scenario.RequireRoutePlan != nil {
		return normalizedCoverageKey(task.Scenario.RequireRoutePlan.Thoroughness)
	}
	return ""
}

func taskLongHorizonAction(task SWETask) string {
	if task.Scenario.RequireLongHorizonState == nil {
		return ""
	}
	if action := normalizedCoverageKey(task.Scenario.RequireLongHorizonState.Action); action != "" {
		return action
	}
	if task.Scenario.RequireLongHorizonState.Resumable != nil {
		if *task.Scenario.RequireLongHorizonState.Resumable {
			return "resumable"
		}
		return "not_resumable"
	}
	return ""
}

func taskLongHorizonKind(task SWETask) string {
	if task.Scenario.RequireLongHorizonState == nil {
		return ""
	}
	return normalizedCoverageKey(task.Scenario.RequireLongHorizonState.MemoryKind)
}

func taskNegativeEvidence(task SWETask) string {
	switch {
	case taskHasTag(task, "no-match"):
		return "no_match"
	case taskHasTag(task, "no-bug-found"):
		return "no_bounded_finding"
	case taskHasTag(task, "false-positive"):
		return "false_positive"
	case taskHasTag(task, "negative-evidence"):
		return "negative_evidence"
	default:
		return ""
	}
}

func taskFileSurfaces(task SWETask) []string {
	paths := make([]string, 0)
	paths = append(paths, task.Scenario.RequireChangedFiles...)
	paths = append(paths, task.Oracle.RequireChangedFiles...)
	paths = append(paths, task.Oracle.ForbidChangedFiles...)
	for _, expected := range task.Oracle.RequireFileContent {
		paths = append(paths, expected.Path)
	}
	return fileSurfaces(paths)
}

func evidenceSurfaces(paths []string) []string {
	return fileSurfaces(paths)
}

func fileSurfaces(paths []string) []string {
	surfaces := make([]string, 0, len(paths))
	for _, path := range paths {
		if surface := fileSurface(path); surface != "" {
			surfaces = append(surfaces, surface)
		}
	}
	return uniqueNonEmptyStrings(surfaces)
}

func fileSurface(path string) string {
	path = strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	switch {
	case path == "":
		return ""
	case strings.Contains(path, "_test.") || strings.Contains(path, "/test") || strings.HasPrefix(path, "test"):
		return "test"
	case strings.HasPrefix(path, "prompts/"):
		return "prompt"
	case strings.HasPrefix(path, "docs/") || strings.HasSuffix(path, ".md") || path == "readme.md":
		return "docs"
	case strings.HasPrefix(path, "scripts/") || strings.HasSuffix(path, ".sh"):
		return "script"
	case strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml") || strings.HasSuffix(path, ".toml") || strings.HasPrefix(path, "config"):
		return "config"
	case strings.HasSuffix(path, ".go"):
		return "go"
	default:
		return "other"
	}
}

func taskOracleAssertions(task SWETask) []string {
	assertions := make([]string, 0, 12)
	oracle := task.Oracle
	if oracle.RequireNoChangedFiles {
		assertions = append(assertions, "no_changed_files")
	}
	if len(oracle.RequireChangedFiles) > 0 || len(task.Scenario.RequireChangedFiles) > 0 {
		assertions = append(assertions, "changed_files")
	}
	if len(oracle.ForbidChangedFiles) > 0 {
		assertions = append(assertions, "forbid_changed_files")
	}
	if len(oracle.RequireFileContent) > 0 {
		assertions = append(assertions, "file_content")
	}
	if oracle.MaxToolRequests != nil {
		assertions = append(assertions, "max_tool_requests")
	}
	if oracle.MaxDurationMillis != nil {
		assertions = append(assertions, "max_duration_millis")
	}
	if oracle.MaxErrors != nil {
		assertions = append(assertions, "max_errors")
	}
	if oracle.MinUserTurns != nil {
		assertions = append(assertions, "min_user_turns")
	}
	if oracle.MinAssistantTurns != nil {
		assertions = append(assertions, "min_assistant_turns")
	}
	if oracle.RequireNoApproval {
		assertions = append(assertions, "no_approval")
	}
	if oracle.RequireNoShell {
		assertions = append(assertions, "no_shell")
	}
	if oracle.RequireNoSummaryRejections {
		assertions = append(assertions, "no_summary_rejections")
	}
	if len(oracle.RequireSummaryRejectionLane) > 0 {
		assertions = append(assertions, "summary_rejection_lane")
	}
	if task.Scenario.RequireApproval != nil && *task.Scenario.RequireApproval {
		assertions = append(assertions, "approval_required")
	}
	if task.Scenario.RequireVerificationPlan != nil {
		assertions = append(assertions, "verification_plan")
	}
	return uniqueNonEmptyStrings(assertions)
}

func taskHasTag(task SWETask, tag string) bool {
	tag = normalizedCoverageKey(tag)
	for _, candidate := range task.Tags {
		if normalizedCoverageKey(candidate) == tag {
			return true
		}
	}
	return false
}

func taskHasAnyTag(task SWETask, tags ...string) bool {
	for _, tag := range tags {
		if taskHasTag(task, tag) {
			return true
		}
	}
	return false
}

func taskRequiresChangedFilesOrApproval(task SWETask) bool {
	if task.Scenario.RequireApproval != nil && *task.Scenario.RequireApproval {
		return true
	}
	return len(task.Scenario.RequireChangedFiles) > 0 || len(task.Oracle.RequireChangedFiles) > 0
}

func taskStartsInLane(task SWETask, lane string) bool {
	if task.Scenario.RequireRoutePlan == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(task.Scenario.RequireRoutePlan.InitialLane), strings.TrimSpace(lane))
}
// VerificationStatus returns a short string describing the verification outcome.
func VerificationStatus(m Metrics) string {
	switch {
	case m.VerificationPassed:
		return "passed"
	case m.VerificationFailed:
		return "failed"
	default:
		return "not-run"
	}
}
