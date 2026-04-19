package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"qwen-bubbles/internal/eval"
)

func main() {
	var suitePath string
	var logsDir string
	var jsonOut bool
	var suiteOnly bool

	flag.StringVar(&suitePath, "suite", "testdata/swe/representative-suite.json", "path to a SWE task suite JSON file")
	flag.StringVar(&logsDir, "logs", "", "directory containing one <task-id>.jsonl log per suite task")
	flag.BoolVar(&jsonOut, "json", false, "emit JSON instead of human-readable text")
	flag.BoolVar(&suiteOnly, "suite-only", false, "validate suite coverage and lint without loading task logs")
	flag.Parse()

	suite, err := eval.ReadSWESuite(suitePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if suiteOnly {
		report := eval.EvaluateSWESuiteDefinitionWithFixtures(suite, suitePath)
		printOrEncodeReport(report, jsonOut)
		if !report.Passed {
			os.Exit(1)
		}
		return
	}
	if err := eval.ValidateSWESuite(suite); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := eval.ValidateSWESuiteFixtures(suite, suitePath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	recordsByTask := make(map[string][]eval.Record, len(suite.Tasks))
	logPaths := make(map[string]string, len(suite.Tasks))
	loadErrors := make(map[string]error)
	for _, task := range suite.Tasks {
		logPath := taskLogPath(suitePath, logsDir, task)
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
	printOrEncodeReport(report, jsonOut)
	if !report.Passed {
		os.Exit(1)
	}
}

func printOrEncodeReport(report eval.SWESuiteReport, jsonOut bool) {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
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
	fmt.Printf("%s %s (%d tasks)\n", status, fallback(report.Suite, "<unnamed suite>"), reportTaskCount(report))
	printCoverage(report.Coverage)
	printLint(report.LintFindings)
	printSummary(report.Summary)
	for _, task := range report.Tasks {
		taskStatus := "PASS"
		if !task.Passed {
			taskStatus = "FAIL"
		}
		fmt.Printf("- %s %s", taskStatus, fallback(task.TaskID, "<unnamed task>"))
		if strings.TrimSpace(task.Family) != "" {
			fmt.Printf(" [%s]", task.Family)
		}
		fmt.Printf(": tools=%d broad=%d verification=%s", task.Metrics.ToolRequests, task.Metrics.BroadToolRequests, verificationStatus(task.Metrics))
		if task.Metrics.DurationMillis > 0 {
			fmt.Printf(" duration=%dms", task.Metrics.DurationMillis)
		}
		if len(task.Metrics.ChangedFiles) > 0 {
			fmt.Printf(" changed=%s", strings.Join(task.Metrics.ChangedFiles, ","))
		}
		if task.Metrics.SummaryRejections > 0 {
			fmt.Printf(" summary_rejections=%d", task.Metrics.SummaryRejections)
		}
		fmt.Println()
		for _, finding := range task.Findings {
			fmt.Printf("  - %s\n", finding)
		}
	}
}

func printSummary(summary eval.SWEResultSummary) {
	if summary.PassedTasks == 0 && summary.FailedTasks == 0 && summary.MissingLogs == 0 && summary.LoadErrors == 0 {
		return
	}
	fmt.Printf(
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
	fmt.Printf("%s: %s\n", label, strings.Join(parts, ", "))
}

func printLint(findings []string) {
	if len(findings) == 0 {
		fmt.Println("Lint: PASS")
		return
	}
	fmt.Printf("Lint: FAIL findings=%d\n", len(findings))
	for _, finding := range findings {
		fmt.Printf("  - %s\n", finding)
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
	fmt.Printf(
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
		fmt.Printf("  - %s\n", finding)
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
