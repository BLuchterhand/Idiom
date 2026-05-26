package sweevalcmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"idiom/internal/eval"
	"idiom/internal/util"
)

var output io.Writer = os.Stdout

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	return run(args, stdout, stderr)
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
	var runLiveContinueOnError bool
	var runnerCommand string
	var generatedLogsDir string
	var writeFailingScenarios string

	fs := flag.NewFlagSet("idiom-swe-eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&suitePath, "suite", "testdata/swe/representative-suite.json", "path to a SWE task suite JSON file")
	fs.StringVar(&logsDir, "logs", "", "directory containing one <task-id>.jsonl log per suite task")
	fs.BoolVar(&jsonOut, "json", false, "emit JSON instead of human-readable text")
	fs.BoolVar(&suiteOnly, "suite-only", false, "validate suite coverage and lint without loading task logs")
	fs.BoolVar(&runLive, "run-live", false, "run each suite task through a live command before scoring")
	fs.BoolVar(&runLiveContinueOnError, "run-live-continue-on-error", false, "continue running live suite tasks after a task command fails or times out")
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
		liveLogPaths, err = runLiveSuiteTasks(suite, suitePath, liveLogsDir, runnerCommand, stderr, runLiveContinueOnError)
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

func firstNonEmpty(values ...string) string {
	return util.FirstNonEmpty(values...)
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
	fmt.Fprintf(output, "%s %s (%d tasks)\n", status, util.Fallback(report.Suite, "<unnamed suite>"), reportTaskCount(report))
	printCoverage(report.Coverage)
	printLint(report.LintFindings)
	printSummary(report.Summary)
	printControlRailsDashboard(report.Tasks)
	for _, task := range report.Tasks {
		taskStatus := "PASS"
		if !task.Passed {
			taskStatus = "FAIL"
		}
		fmt.Fprintf(output, "- %s %s", taskStatus, util.Fallback(task.TaskID, "<unnamed task>"))
		if strings.TrimSpace(task.Family) != "" {
			fmt.Fprintf(output, " [%s]", task.Family)
		}
		fmt.Fprintf(output, ": tools=%d broad=%d verification=%s", task.Metrics.ToolRequests, task.Metrics.BroadToolRequests, eval.VerificationStatus(task.Metrics))
		if len(task.Metrics.DiscoveryClasses) > 0 {
			fmt.Fprintf(output, " discovery=%s", formatCountMap(task.Metrics.DiscoveryClasses))
		}
		if task.Metrics.HeavyDiscoveryRequests > 0 {
			fmt.Fprintf(output, " heavy_discovery=%d", task.Metrics.HeavyDiscoveryRequests)
		}
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

func formatCountMap(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%d", key, counts[key]))
	}
	return strings.Join(parts, ",")
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
