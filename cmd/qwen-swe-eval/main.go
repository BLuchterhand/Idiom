package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"qwen-bubbles/internal/eval"
)

var output io.Writer = os.Stdout

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	oldOutput := output
	output = stdout
	defer func() {
		output = oldOutput
	}()

	var suitePath string
	var logsDir string
	var jsonOut bool
	var suiteOnly bool
	var runLive bool
	var runnerCommand string
	var generatedLogsDir string
	var writeFailingScenarios string

	fs := flag.NewFlagSet("qwen-swe-eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&suitePath, "suite", "testdata/swe/representative-suite.json", "path to a SWE task suite JSON file")
	fs.StringVar(&logsDir, "logs", "", "directory containing one <task-id>.jsonl log per suite task")
	fs.BoolVar(&jsonOut, "json", false, "emit JSON instead of human-readable text")
	fs.BoolVar(&suiteOnly, "suite-only", false, "validate suite coverage and lint without loading task logs")
	fs.BoolVar(&runLive, "run-live", false, "run each suite task through a live command before scoring")
	fs.StringVar(&runnerCommand, "runner-command", "", "shell command used by -run-live; supports {prompt}, {task_id}, {log_path}, and {fixture}")
	fs.StringVar(&generatedLogsDir, "generated-logs", "", "directory where -run-live should write generated task logs")
	fs.StringVar(&writeFailingScenarios, "write-failing-scenarios", "", "write a SWE suite skeleton for failed task logs")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	suite, err := eval.ReadSWESuite(suitePath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if suiteOnly {
		report := eval.EvaluateSWESuiteDefinitionWithFixtures(suite, suitePath)
		printOrEncodeReport(report, jsonOut, stdout)
		if !report.Passed {
			return 1
		}
		return 0
	}
	if err := eval.ValidateSWESuite(suite); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := eval.ValidateSWESuiteFixtures(suite, suitePath); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	liveLogPaths := map[string]string(nil)
	if runLive {
		liveLogsDir := firstNonEmpty(generatedLogsDir, logsDir, filepath.Join(filepath.Dir(suitePath), "generated-logs"))
		var err error
		liveLogPaths, err = runLiveSuiteTasks(suite, suitePath, liveLogsDir, runnerCommand, stderr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		logsDir = liveLogsDir
	}
	recordsByTask := make(map[string][]eval.Record, len(suite.Tasks))
	logPaths := make(map[string]string, len(suite.Tasks))
	loadErrors := make(map[string]error)
	for _, task := range suite.Tasks {
		logPath := taskLogPath(suitePath, logsDir, task)
		if liveLogPaths != nil {
			if generated := strings.TrimSpace(liveLogPaths[task.ID]); generated != "" {
				logPath = generated
			}
		}
		logPaths[task.ID] = logPath
		if strings.TrimSpace(logPath) == "" {
			continue
		}
		records, err := eval.LoadRecords(logPath)
		if err != nil {
			loadErrors[task.ID] = err
			continue
		}
		recordsByTask[task.ID] = records
	}

	report := eval.EvaluateSWESuiteWithLoadErrors(suite, recordsByTask, logPaths, loadErrors)
	if strings.TrimSpace(writeFailingScenarios) != "" {
		generated := eval.BuildFailingScenarioSuite("generated regressions from "+suite.Name, suite, report, recordsByTask)
		if err := writeSWESuite(writeFailingScenarios, generated); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}
	printOrEncodeReport(report, jsonOut, stdout)
	if !report.Passed {
		return 1
	}
	return 0
}

func runLiveSuiteTasks(suite eval.SWESuite, suitePath string, outputDir string, commandTemplate string, stderr io.Writer) (map[string]string, error) {
	commandTemplate = strings.TrimSpace(commandTemplate)
	if commandTemplate == "" {
		return nil, fmt.Errorf("-run-live requires -runner-command")
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("create generated logs dir: %w", err)
	}
	logPaths := make(map[string]string, len(suite.Tasks))
	for _, task := range suite.Tasks {
		logPath := taskLogPath(suitePath, outputDir, task)
		if strings.TrimSpace(task.LogPath) != "" {
			logPath = filepath.Join(outputDir, strings.TrimSpace(task.ID)+".jsonl")
		}
		logPaths[task.ID] = logPath
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return nil, fmt.Errorf("create log dir for %s: %w", task.ID, err)
		}
		fixture := taskFixturePath(suitePath, task)
		prompt := taskPrompt(task)
		turns := taskTurns(task)
		turnsJSON, err := json.Marshal(turns)
		if err != nil {
			return nil, fmt.Errorf("encode turns for %s: %w", task.ID, err)
		}
		command := renderRunnerCommand(commandTemplate, map[string]string{
			"prompt":     prompt,
			"turns_json": string(turnsJSON),
			"turn_count": fmt.Sprintf("%d", len(turns)),
			"task_id":    strings.TrimSpace(task.ID),
			"log_path":   logPath,
			"fixture":    fixture,
		})
		cmd := exec.Command("/bin/sh", "-lc", command)
		cmd.Dir = firstNonEmpty(fixture, filepath.Dir(suitePath))
		cmd.Env = append(os.Environ(),
			"QWEN_SWE_PROMPT="+prompt,
			"QWEN_SWE_TURNS_JSON="+string(turnsJSON),
			"QWEN_SWE_TURN_COUNT="+fmt.Sprintf("%d", len(turns)),
			"QWEN_SWE_TASK_ID="+strings.TrimSpace(task.ID),
			"QWEN_SWE_LOG="+logPath,
			"QWEN_SWE_FIXTURE="+fixture,
		)
		output, err := cmd.CombinedOutput()
		if len(output) > 0 && fileMissingOrEmpty(logPath) {
			if writeErr := os.WriteFile(logPath, output, 0o644); writeErr != nil {
				return nil, fmt.Errorf("write captured log for %s: %w", task.ID, writeErr)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("run live task %s: %w: %s", task.ID, err, strings.TrimSpace(string(output)))
		}
		fmt.Fprintf(stderr, "generated %s\n", logPath)
	}
	return logPaths, nil
}

func renderRunnerCommand(template string, values map[string]string) string {
	pairs := make([]string, 0, 8)
	for _, key := range []string{"prompt", "turns_json", "turn_count", "task_id", "log_path", "fixture"} {
		if value, ok := values[key]; ok {
			pairs = append(pairs, "{"+key+"}", shellQuote(value))
		}
	}
	if len(pairs) == 0 {
		return template
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func taskPrompt(task eval.SWETask) string {
	if strings.TrimSpace(task.Prompt) != "" {
		return strings.TrimSpace(task.Prompt)
	}
	if len(task.Turns) == 0 {
		return ""
	}
	return strings.TrimSpace(task.Turns[len(task.Turns)-1])
}

func taskTurns(task eval.SWETask) []string {
	if len(task.Turns) > 0 {
		turns := make([]string, 0, len(task.Turns))
		for _, turn := range task.Turns {
			if turn = strings.TrimSpace(turn); turn != "" {
				turns = append(turns, turn)
			}
		}
		if len(turns) > 0 {
			return turns
		}
	}
	if prompt := taskPrompt(task); prompt != "" {
		return []string{prompt}
	}
	return nil
}

func taskFixturePath(suitePath string, task eval.SWETask) string {
	if strings.TrimSpace(task.Fixture) == "" {
		return ""
	}
	if filepath.IsAbs(task.Fixture) {
		return filepath.Clean(task.Fixture)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(suitePath), task.Fixture))
}

func writeSWESuite(path string, suite eval.SWESuite) error {
	data, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return fmt.Errorf("encode generated suite: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Clean(path)), 0o755); err != nil {
		return fmt.Errorf("create generated suite dir: %w", err)
	}
	if err := os.WriteFile(filepath.Clean(path), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write generated suite: %w", err)
	}
	return nil
}

func fileMissingOrEmpty(path string) bool {
	info, err := os.Stat(path)
	return err != nil || info.Size() == 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func printOrEncodeReport(report eval.SWESuiteReport, jsonOut bool, stdout io.Writer) {
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	} else {
		printSuiteReport(report)
	}
}

func taskLogPath(suitePath string, logsDir string, task eval.SWETask) string {
	if strings.TrimSpace(task.LogPath) != "" {
		if filepath.IsAbs(task.LogPath) {
			return filepath.Clean(task.LogPath)
		}
		return filepath.Clean(filepath.Join(filepath.Dir(suitePath), task.LogPath))
	}
	if strings.TrimSpace(logsDir) == "" {
		return ""
	}
	return filepath.Join(logsDir, strings.TrimSpace(task.ID)+".jsonl")
}

func printSuiteReport(report eval.SWESuiteReport) {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Fprintf(output, "%s %s (%d tasks)\n", status, fallback(report.Suite, "<unnamed suite>"), reportTaskCount(report))
	printCoverage(report.Coverage)
	printLint(report.LintFindings)
	printSummary(report.Summary)
	printControlRailsDashboard(report.Tasks)
	for _, task := range report.Tasks {
		taskStatus := "PASS"
		if !task.Passed {
			taskStatus = "FAIL"
		}
		fmt.Fprintf(output, "- %s %s", taskStatus, fallback(task.TaskID, "<unnamed task>"))
		if strings.TrimSpace(task.Family) != "" {
			fmt.Fprintf(output, " [%s]", task.Family)
		}
		fmt.Fprintf(output, ": tools=%d broad=%d verification=%s", task.Metrics.ToolRequests, task.Metrics.BroadToolRequests, verificationStatus(task.Metrics))
		if task.Metrics.DurationMillis > 0 {
			fmt.Fprintf(output, " duration=%dms", task.Metrics.DurationMillis)
		}
		if len(task.Metrics.ChangedFiles) > 0 {
			fmt.Fprintf(output, " changed=%s", strings.Join(task.Metrics.ChangedFiles, ","))
		}
		if task.Metrics.SummaryRejections > 0 {
			fmt.Fprintf(output, " summary_rejections=%d", task.Metrics.SummaryRejections)
		}
		fmt.Fprintln(output)
		for _, finding := range task.Findings {
			fmt.Fprintf(output, "  - %s\n", finding)
		}
	}
}

func printSummary(summary eval.SWEResultSummary) {
	if summary.PassedTasks == 0 && summary.FailedTasks == 0 && summary.MissingLogs == 0 && summary.LoadErrors == 0 {
		return
	}
	fmt.Fprintf(output,
		"Results: passed=%d failed=%d missing_logs=%d load_errors=%d\n",
		summary.PassedTasks,
		summary.FailedTasks,
		summary.MissingLogs,
		summary.LoadErrors,
	)
	printFailedBuckets("Failed families", summary.ByFamily)
	printFailedBuckets("Failed task shapes", summary.ByTaskShape)
	printFailedBuckets("Failed fixtures", summary.ByFixture)
	printFailedBuckets("Failed initial lanes", summary.ByInitialLane)
	printFailedBuckets("Failed route reasons", summary.ByRouteReason)
	printFailedBuckets("Failed scenario assertions", summary.ByScenario)
	printFailedBuckets("Failed readiness actions", summary.ByReadiness)
	printFailedBuckets("Failed deepening actions", summary.ByDeepening)
	printFailedBuckets("Failed final critic actions", summary.ByFinalCritic)
	printFailedBuckets("Failed effort levels", summary.ByEffortLevel)
	printFailedBuckets("Failed long-horizon actions", summary.ByLongHorizon)
	printFailedBuckets("Failed negative-evidence outcomes", summary.ByNegative)
	printFailedBuckets("Failed required tools", summary.ByTool)
	printFailedBuckets("Failed file surfaces", summary.ByFileSurface)
}

type dashboardBucket struct {
	total  int
	failed int
}

func printControlRailsDashboard(tasks []eval.SWETaskReport) {
	byFamily := make(map[string]dashboardBucket)
	byPermission := make(map[string]dashboardBucket)
	for _, task := range tasks {
		if len(task.Metrics.ControlRails) == 0 {
			continue
		}
		rails := task.Metrics.ControlRails[len(task.Metrics.ControlRails)-1]
		failed := !task.Passed
		addDashboardBucket(byFamily, firstNonEmpty(rails.TaskFamily, "<unknown>"), failed)
		addDashboardBucket(byPermission, firstNonEmpty(rails.PermissionProfile, "<unknown>"), failed)
	}
	if len(byFamily) == 0 && len(byPermission) == 0 {
		return
	}
	fmt.Fprintln(output, "Control rails dashboard:")
	printDashboardBuckets("  task_family", byFamily)
	printDashboardBuckets("  permission_profile", byPermission)
}

func addDashboardBucket(buckets map[string]dashboardBucket, key string, failed bool) {
	bucket := buckets[key]
	bucket.total++
	if failed {
		bucket.failed++
	}
	buckets[key] = bucket
}

func printDashboardBuckets(label string, buckets map[string]dashboardBucket) {
	if len(buckets) == 0 {
		return
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		parts = append(parts, fmt.Sprintf("%s=%d/%d failed", key, bucket.failed, bucket.total))
	}
	fmt.Fprintf(output, "%s: %s\n", label, strings.Join(parts, ", "))
}

func printFailedBuckets(label string, buckets map[string]eval.SWEResultBucket) {
	keys := make([]string, 0, len(buckets))
	for key, bucket := range buckets {
		if bucket.Failed > 0 {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		bucket := buckets[key]
		parts = append(parts, fmt.Sprintf("%s=%d/%d", key, bucket.Failed, bucket.Total))
	}
	fmt.Fprintf(output, "%s: %s\n", label, strings.Join(parts, ", "))
}

func printLint(findings []string) {
	if len(findings) == 0 {
		fmt.Fprintln(output, "Lint: PASS")
		return
	}
	fmt.Fprintf(output, "Lint: FAIL findings=%d\n", len(findings))
	for _, finding := range findings {
		fmt.Fprintf(output, "  - %s\n", finding)
	}
}

func reportTaskCount(report eval.SWESuiteReport) int {
	if len(report.Tasks) > 0 {
		return len(report.Tasks)
	}
	return report.Coverage.TotalTasks
}

func printCoverage(coverage eval.SWECoverageReport) {
	status := "PASS"
	if !coverage.Passed {
		status = "FAIL"
	}
	fmt.Fprintf(output,
		"Coverage: %s families=%d tags=%d fixtures=%d task_shapes=%d initial_lanes=%d followup_lanes=%d route_reasons=%d contracts=%d thoroughness=%d verification=%d scenario_assertions=%d oracle_assertions=%d readiness_actions=%d deepening_actions=%d final_critic_actions=%d effort_levels=%d long_horizon_actions=%d long_horizon_kinds=%d negative_evidence=%d tools=%d forbidden_tools=%d file_surfaces=%d evidence_surfaces=%d tasks=%d\n",
		status,
		len(coverage.Families),
		len(coverage.Tags),
		len(coverage.Fixtures),
		len(coverage.TaskShapes),
		len(coverage.InitialLanes),
		len(coverage.FollowupLanes),
		len(coverage.RouteReasons),
		len(coverage.Contracts),
		len(coverage.Thoroughness),
		len(coverage.Verification),
		len(coverage.ScenarioAssertions),
		len(coverage.OracleAssertions),
		len(coverage.ReadinessActions),
		len(coverage.DeepeningActions),
		len(coverage.FinalCriticActions),
		len(coverage.EffortLevels),
		len(coverage.LongHorizonActions),
		len(coverage.LongHorizonKinds),
		len(coverage.NegativeEvidence),
		len(coverage.Tools),
		len(coverage.ForbiddenTools),
		len(coverage.FileSurfaces),
		len(coverage.EvidenceSurfaces),
		coverage.TotalTasks,
	)
	for _, finding := range coverage.Findings {
		fmt.Fprintf(output, "  - %s\n", finding)
	}
}

func verificationStatus(metrics eval.Metrics) string {
	switch {
	case metrics.VerificationPassed:
		return "passed"
	case metrics.VerificationFailed:
		return "failed"
	default:
		return "not-run"
	}
}

func fallback(value string, alt string) string {
	if strings.TrimSpace(value) == "" {
		return alt
	}
	return value
}
