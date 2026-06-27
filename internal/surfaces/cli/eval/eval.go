package eval

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"idiom/internal/evidence/eval"
	"idiom/internal/platform/textutil"
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

	var (
		logPath                string
		scenarioPath           string
		writeScenarioPath      string
		localizationCorpusPath string
		localizationTopK       string
		localizationReport     bool
		jsonOut                bool
	)

	fs := flag.NewFlagSet("idiom-eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&logPath, "log", "", "path to an idiom JSONL conversation log")
	fs.StringVar(&scenarioPath, "scenario", "", "path to a scenario JSON file")
	fs.StringVar(&writeScenarioPath, "write-scenario", "", "write a suggested scenario JSON file derived from the log")
	fs.StringVar(&localizationCorpusPath, "localization-corpus", "", "path to a localization corpus JSON file")
	fs.StringVar(&localizationTopK, "localization-top-k", "1,3,5", "comma-separated k values for localization top-k edit-path coverage")
	fs.BoolVar(&localizationReport, "localization-report", false, "emit the unified localization evidence report for -localization-corpus")
	fs.BoolVar(&jsonOut, "json", false, "emit JSON instead of human-readable text")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if strings.TrimSpace(localizationCorpusPath) != "" {
		topK, err := parseLocalizationTopK(localizationTopK)
		if err != nil {
			fmt.Fprintln(stderr, err)

			return 2
		}

		root := workspaceRootForCorpus(localizationCorpusPath)
		if localizationReport {
			report, err := eval.EvaluateLocalizationUnifiedReportFile(localizationCorpusPath, root, eval.LocalizationUnifiedReportOptions{
				Benchmark:           eval.LocalizationBenchmarkOptions{TopK: topK},
				VerificationLogPath: logPath,
			})
			if err != nil {
				fmt.Fprintln(stderr, err)

				return 1
			}

			if jsonOut {
				enc := json.NewEncoder(stdout)
				enc.SetIndent("", "  ")

				_ = enc.Encode(report)
			} else {
				printLocalizationUnifiedReport(report)
			}

			if !report.Passed {
				return 1
			}

			return 0
		}

		report, err := eval.EvaluateLocalizationCorpusFileWithOptions(localizationCorpusPath, root, eval.LocalizationBenchmarkOptions{TopK: topK})
		if err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}

		if jsonOut {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")

			_ = enc.Encode(report)
		} else {
			printLocalizationCorpusReport(report)
		}

		if !report.Passed {
			return 1
		}

		return 0
	}

	if strings.TrimSpace(logPath) == "" {
		fmt.Fprintln(stderr, "missing required -log path")

		return 2
	}

	records, err := eval.LoadRecords(logPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	if strings.TrimSpace(writeScenarioPath) != "" {
		name := strings.TrimSuffix(filepath.Base(writeScenarioPath), filepath.Ext(writeScenarioPath))
		scenario := eval.BuildSuggestedScenario(name, records)

		if err := eval.WriteScenario(writeScenarioPath, scenario); err != nil {
			fmt.Fprintln(stderr, err)

			return 1
		}
	}

	if strings.TrimSpace(scenarioPath) == "" {
		metrics := eval.Analyze(records)
		if jsonOut {
			_ = json.NewEncoder(stdout).Encode(metrics)
			return 0
		}

		printMetrics(metrics)

		return 0
	}

	scenario, err := eval.LoadScenario(scenarioPath)
	if err != nil {
		fmt.Fprintln(stderr, err)

		return 1
	}

	report := eval.Evaluate(logPath, records, scenario)

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		_ = enc.Encode(report)
	} else {
		printReport(report)
	}

	if !report.Passed {
		return 1
	}

	return 0
}

func parseLocalizationTopK(value string) ([]int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return eval.DefaultLocalizationTopK(), nil
	}

	parts := strings.Split(value, ",")
	values := make([]int, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		parsed, err := strconv.Atoi(part)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("invalid -localization-top-k value %q: k values must be positive integers", value)
		}

		values = append(values, parsed)
	}

	return eval.NormalizeLocalizationTopK(values), nil
}

func workspaceRootForCorpus(path string) string {
	start := filepath.Dir(filepath.Clean(path))
	if abs, err := filepath.Abs(start); err == nil {
		start = abs
	}

	for {
		if _, err := os.Stat(filepath.Join(start, "go.mod")); err == nil {
			return start
		}

		parent := filepath.Dir(start)
		if parent == start {
			break
		}

		start = parent
	}

	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}

	return "."
}

func printMetrics(metrics eval.Metrics) {
	fmt.Fprintf(output, "Last user prompt: %s\n", textutil.Fallback(metrics.LastUser, "<none>"))

	if metrics.DurationMillis > 0 {
		fmt.Fprintf(output, "Duration: %dms\n", metrics.DurationMillis)
	}

	fmt.Fprintf(output, "Tool requests: %d (broad: %d)\n", metrics.ToolRequests, metrics.BroadToolRequests)
	fmt.Fprintf(output, "Repeated dead ends: %d\n", metrics.RepeatedDeadEndRejections+metrics.RepeatedToolRejections)

	if metrics.SummaryRejections > 0 {
		fmt.Fprintf(output, "Summary rejections: %d", metrics.SummaryRejections)

		if len(metrics.SummaryRejectionLanes) > 0 {
			fmt.Fprintf(output, " (%s)", strings.Join(metrics.SummaryRejectionLanes, ", "))
		}

		fmt.Fprintln(output)
	}

	fmt.Fprintf(output, "Verification: %s (%d checks)\n", eval.VerificationStatus(metrics), metrics.VerificationChecks)

	if len(metrics.VerificationPlans) > 0 {
		plan := metrics.VerificationPlans[len(metrics.VerificationPlans)-1]
		fmt.Fprintf(output, "Verification plan: confidence=%s", textutil.Fallback(plan.Confidence, "unknown"))

		if strings.TrimSpace(plan.Coverage) != "" {
			fmt.Fprintf(output, " coverage=%s", plan.Coverage)
		}

		if strings.TrimSpace(plan.RiskLevel) != "" {
			fmt.Fprintf(output, " risk=%s", plan.RiskLevel)
		}

		if plan.UsesFullSuite {
			fmt.Fprint(output, " full-suite=true")
		}

		if len(plan.ExpectedFailures) > 0 {
			fmt.Fprintf(output, " expected=%s", strings.Join(plan.ExpectedFailures, ","))
		}

		fmt.Fprintln(output)
	}

	if len(metrics.ReadinessChecks) > 0 {
		readiness := metrics.ReadinessChecks[len(metrics.ReadinessChecks)-1]
		fmt.Fprintf(output, "Readiness: ready=%t", readiness.Ready)

		if strings.TrimSpace(readiness.Lane) != "" {
			fmt.Fprintf(output, " lane=%s", readiness.Lane)
		}

		if strings.TrimSpace(readiness.Confidence) != "" {
			fmt.Fprintf(output, " confidence=%s", readiness.Confidence)
		}

		if strings.TrimSpace(readiness.Action) != "" {
			fmt.Fprintf(output, " action=%s", readiness.Action)
		}

		if len(readiness.EvidencePaths) > 0 {
			fmt.Fprintf(output, " evidence_paths=%d", len(readiness.EvidencePaths))
		}

		if strings.TrimSpace(readiness.Reason) != "" {
			fmt.Fprintf(output, " reason=%s", readiness.Reason)
		}

		fmt.Fprintln(output)
	}

	if len(metrics.DeepeningAttempts) > 0 {
		deepening := metrics.DeepeningAttempts[len(metrics.DeepeningAttempts)-1]
		fmt.Fprintf(output, "Deepening: attempted=%t", deepening.Attempted)

		if deepening.Attempt > 0 {
			fmt.Fprintf(output, " attempt=%d", deepening.Attempt)
		}

		if deepening.Exhausted {
			fmt.Fprint(output, " exhausted=true")
		}

		if strings.TrimSpace(deepening.Lane) != "" {
			fmt.Fprintf(output, " lane=%s", deepening.Lane)
		}

		if strings.TrimSpace(deepening.Action) != "" {
			fmt.Fprintf(output, " action=%s", deepening.Action)
		}

		if len(deepening.EvidencePaths) > 0 {
			fmt.Fprintf(output, " evidence_paths=%d", len(deepening.EvidencePaths))
		}

		if strings.TrimSpace(deepening.Reason) != "" {
			fmt.Fprintf(output, " reason=%s", deepening.Reason)
		}

		fmt.Fprintln(output)
	}

	if len(metrics.FinalAnswerCritics) > 0 {
		critic := metrics.FinalAnswerCritics[len(metrics.FinalAnswerCritics)-1]
		fmt.Fprintf(output, "Final critic: passed=%t", critic.Passed)

		if strings.TrimSpace(critic.Contract) != "" {
			fmt.Fprintf(output, " contract=%s", critic.Contract)
		}

		if strings.TrimSpace(critic.Action) != "" {
			fmt.Fprintf(output, " action=%s", critic.Action)
		}

		if critic.Repaired {
			fmt.Fprint(output, " repaired=true")
		}

		if len(critic.Findings) > 0 {
			fmt.Fprintf(output, " findings=%d", len(critic.Findings))
		}

		fmt.Fprintln(output)
	}

	if len(metrics.EffortPolicies) > 0 {
		policy := metrics.EffortPolicies[len(metrics.EffortPolicies)-1]
		fmt.Fprintf(output, "Effort policy: lane=%s level=%s", textutil.Fallback(policy.Lane, "unknown"), textutil.Fallback(policy.Level, "unknown"))

		if policy.AnalysisToolBudget > 0 {
			fmt.Fprintf(output, " analysis_budget=%d", policy.AnalysisToolBudget)
		}

		if policy.ImplementToolBudget > 0 {
			fmt.Fprintf(output, " implement_budget=%d", policy.ImplementToolBudget)
		}

		if policy.DeepeningPasses > 0 {
			fmt.Fprintf(output, " deepening_passes=%d", policy.DeepeningPasses)
		}

		if policy.MaxOutputTokens > 0 {
			fmt.Fprintf(output, " max_output_tokens=%d", policy.MaxOutputTokens)
		}

		if policy.ReasoningBudget != 0 {
			fmt.Fprintf(output, " reasoning_budget=%d", policy.ReasoningBudget)
		}

		if strings.TrimSpace(policy.ConfidenceFloor) != "" {
			fmt.Fprintf(output, " confidence_floor=%s", policy.ConfidenceFloor)
		}

		fmt.Fprintln(output)
	}

	if len(metrics.BudgetLedgers) > 0 {
		ledger := metrics.BudgetLedgers[len(metrics.BudgetLedgers)-1]
		fmt.Fprintf(output, "Budget ledger: lane=%s stop=%s model_calls=%d deepening=%d/%d evidence=%d",
			textutil.Fallback(ledger.Lane, "unknown"),
			textutil.Fallback(ledger.StopReason, "unknown"),
			ledger.UsedModelCalls,
			ledger.UsedDeepeningPasses,
			ledger.MaxDeepeningPasses,
			ledger.TotalEvidencePaths,
		)

		if ledger.BudgetExhausted {
			fmt.Fprint(output, " exhausted=true")
		}

		fmt.Fprintln(output)
	}

	if len(metrics.ControlRails) > 0 {
		rails := metrics.ControlRails[len(metrics.ControlRails)-1]
		fmt.Fprintf(output, "Control rails: lane=%s family=%s permission=%s",
			textutil.Fallback(rails.Lane, "unknown"),
			textutil.Fallback(rails.TaskFamily, "unknown"),
			textutil.Fallback(rails.PermissionProfile, "unknown"),
		)

		if strings.TrimSpace(rails.Contract) != "" {
			fmt.Fprintf(output, " contract=%s", rails.Contract)
		}

		if len(rails.RequiredEvidence) > 0 {
			fmt.Fprintf(output, " required_evidence=%d", len(rails.RequiredEvidence))
		}

		if len(rails.RequiredTools) > 0 {
			fmt.Fprintf(output, " required_tools=%d", len(rails.RequiredTools))
		}

		if rails.FailureToEval {
			fmt.Fprint(output, " failure-to-eval=true")
		}

		fmt.Fprintln(output)
	}

	if len(metrics.ClaimConfidences) > 0 {
		confidence := metrics.ClaimConfidences[len(metrics.ClaimConfidences)-1]
		fmt.Fprintf(output, "Claim confidence: lane=%s confidence=%s evidence_paths=%d unresolved=%d\n",
			textutil.Fallback(confidence.Lane, "unknown"),
			textutil.Fallback(confidence.Confidence, "unknown"),
			len(confidence.EvidencePaths),
			len(confidence.UnresolvedUncertainty),
		)
	}

	if len(metrics.ContextCompactions) > 0 {
		compaction := metrics.ContextCompactions[len(metrics.ContextCompactions)-1]
		fmt.Fprintf(output, "Context compaction: dropped=%d retained=%d max_history=%d pinned_paths=%d",
			compaction.DroppedMessages,
			compaction.RetainedMessages,
			compaction.MaxHistory,
			len(compaction.PinnedPaths),
		)

		if strings.TrimSpace(compaction.PinnedMemoryKind) != "" {
			fmt.Fprintf(output, " memory=%s", compaction.PinnedMemoryKind)
		}

		fmt.Fprintln(output)
	}

	if len(metrics.RouteAmbiguities) > 0 {
		ambiguity := metrics.RouteAmbiguities[len(metrics.RouteAmbiguities)-1]
		fmt.Fprintf(output, "Route ambiguity: selected=%s ambiguous=%t signals=%s\n",
			textutil.Fallback(ambiguity.SelectedLane, "unknown"),
			ambiguity.Ambiguous,
			strings.Join(ambiguity.Signals, ","),
		)
	}

	if len(metrics.LongHorizonStates) > 0 {
		state := metrics.LongHorizonStates[len(metrics.LongHorizonStates)-1]
		fmt.Fprintf(output, "Long-horizon state: action=%s", textutil.Fallback(state.Action, "unknown"))

		if strings.TrimSpace(state.MemoryKind) != "" {
			fmt.Fprintf(output, " kind=%s", state.MemoryKind)
		}

		if strings.TrimSpace(state.SourceLane) != "" {
			fmt.Fprintf(output, " source_lane=%s", state.SourceLane)
		}

		if state.Stage > 0 {
			fmt.Fprintf(output, " stage=%d", state.Stage)
		}

		if strings.TrimSpace(state.FocusPath) != "" {
			fmt.Fprintf(output, " focus=%s", state.FocusPath)
		}

		if len(state.Remaining) > 0 {
			fmt.Fprintf(output, " remaining=%d", len(state.Remaining))
		}

		if len(state.ChosenTargets) > 0 {
			fmt.Fprintf(output, " targets=%d", len(state.ChosenTargets))
		}

		if len(state.OpenGaps) > 0 {
			fmt.Fprintf(output, " open_gaps=%d", len(state.OpenGaps))
		}

		if state.Resumable {
			fmt.Fprint(output, " resumable=true")
		}

		fmt.Fprintln(output)
	}

	if len(metrics.ChangedFiles) > 0 {
		fmt.Fprintf(output, "Changed files: %s\n", strings.Join(metrics.ChangedFiles, ", "))
	}

	if len(metrics.EvidencePaths) > 0 {
		fmt.Fprintf(output, "Evidence paths: %s\n", strings.Join(metrics.EvidencePaths, ", "))
	}

	if len(metrics.LanguageCapabilities) > 0 {
		fmt.Fprintln(output, "Language capabilities:")

		for _, capability := range metrics.LanguageCapabilities {
			line := fmt.Sprintf("- %s", textutil.Fallback(capability.Language, "unknown"))
			if len(capability.Tools) > 0 {
				line += " tools=" + strings.Join(capability.Tools, ",")
			}

			if len(capability.Signals) > 0 {
				line += " signals=" + strings.Join(capability.Signals, ",")
			}

			fmt.Fprintln(output, line)
		}
	}

	if len(metrics.Phases) > 0 {
		fmt.Fprintln(output, "Phases:")

		for _, phase := range metrics.Phases {
			fmt.Fprintf(output, "- %s / %s [%s]\n", textutil.Fallback(phase.Lane, "unknown"), textutil.Fallback(phase.Phase, "unknown"), textutil.Fallback(phase.State, "unknown"))
		}
	}

	if len(metrics.RoutePlans) > 0 {
		plan := metrics.RoutePlans[len(metrics.RoutePlans)-1]
		fmt.Fprintf(output, "Route plan: initial=%s", textutil.Fallback(plan.InitialLane, "unknown"))

		if strings.TrimSpace(plan.FollowupLane) != "" {
			fmt.Fprintf(output, " followup=%s", plan.FollowupLane)
		}

		if strings.TrimSpace(plan.Reason) != "" {
			fmt.Fprintf(output, " reason=%s", plan.Reason)
		}

		if strings.TrimSpace(plan.Contract) != "" {
			fmt.Fprintf(output, " contract=%s", plan.Contract)
		}

		if strings.TrimSpace(plan.Thoroughness) != "" {
			fmt.Fprintf(output, " thoroughness=%s", plan.Thoroughness)
		}

		fmt.Fprintln(output)
	}

	if len(metrics.Replans) > 0 {
		replan := metrics.Replans[len(metrics.Replans)-1]
		fmt.Fprintf(output, "Replan recommendation: %s -> %s", textutil.Fallback(replan.FromLane, "unknown"), textutil.Fallback(replan.ToLane, "unknown"))

		if strings.TrimSpace(replan.Reason) != "" {
			fmt.Fprintf(output, " reason=%s", replan.Reason)
		}

		fmt.Fprintln(output)
	}
}

func printLocalizationCorpusReport(report eval.LocalizationCorpusReport) {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}

	fmt.Fprintf(output, "Localization corpus: %s %s tasks=%d experimental=%t\n", status, textutil.Fallback(report.Name, "<unnamed corpus>"), report.TotalTasks, report.Experimental)
	fmt.Fprintf(output, "Sources: %s\n", formatEvalCountMap(report.SourceCoverage))
	fmt.Fprintf(output, "Themes: %s\n", formatEvalCountMap(report.ThemeCoverage))
	fmt.Fprintf(output, "Redaction: %s\n", formatEvalCountMap(report.RedactionStatus))
	fmt.Fprintf(output, "Client/product: %s\n", formatEvalCountMap(report.ClientProductAreas))
	fmt.Fprintf(output, "Expected anchors: files=%d symbols=%d tests=%d\n", report.ExpectedFiles, report.ExpectedSymbols, report.ExpectedTests)
	printLocalizationBenchmarkReport(report.Benchmark)

	for _, finding := range report.Findings {
		fmt.Fprintf(output, "  - %s\n", finding)
	}
}

func printLocalizationBenchmarkReport(report eval.LocalizationBenchmarkReport) {
	fmt.Fprintf(output, "Benchmark metrics: experimental=%t manual_corpus_review_required=%t", report.Experimental, report.ManualCorpusReviewRequired)

	if len(report.MissingInputs) > 0 {
		fmt.Fprintf(output, " missing_inputs=%s", strings.Join(report.MissingInputs, ","))
	}

	fmt.Fprintln(output)
	fmt.Fprint(output, "Top-k edit-path coverage:")

	if len(report.TopKEditPathCoverage) == 0 {
		fmt.Fprint(output, " <none>")
	}

	for _, metric := range report.TopKEditPathCoverage {
		fmt.Fprintf(output, " k=%d %d/%d rate=%.3f available=%t",
			metric.K,
			metric.Numerator,
			metric.Denominator,
			metric.Rate,
			metric.Available,
		)

		if len(metric.FailureReasons) > 0 {
			fmt.Fprintf(output, " failure_reasons=%s", strings.Join(metric.FailureReasons, ","))
		}
	}

	fmt.Fprintln(output)
	printLocalizationCountMetric("Broad-read count", report.BroadReadCount)
	printLocalizationCountMetric("Zero-hit search count", report.ZeroHitSearchCount)
	fmt.Fprintf(output, "Candidate/path waste: candidates=%d/%d rate=%.3f paths=%d/%d rate=%.3f available=%t",
		report.CandidatePathWaste.CandidateWasteNumerator,
		report.CandidatePathWaste.CandidateWasteDenominator,
		report.CandidatePathWaste.CandidateWasteRate,
		report.CandidatePathWaste.PathWasteNumerator,
		report.CandidatePathWaste.PathWasteDenominator,
		report.CandidatePathWaste.PathWasteRate,
		report.CandidatePathWaste.Available,
	)

	if len(report.CandidatePathWaste.FailureReasons) > 0 {
		fmt.Fprintf(output, " failure_reasons=%s", strings.Join(report.CandidatePathWaste.FailureReasons, ","))
	}

	fmt.Fprintln(output)
	printLocalizationCompetitiveConditions(report.CompetitiveConditions)

	if len(report.Definitions) > 0 {
		fmt.Fprintln(output, "Metric definitions:")

		for _, definition := range report.Definitions {
			fmt.Fprintf(output, "- %s numerator=%s denominator=%s failure_reasons=%s\n",
				definition.Name,
				definition.Numerator,
				definition.Denominator,
				strings.Join(definition.FailureReasons, ","),
			)
		}
	}
}

func printLocalizationCompetitiveConditions(conditions []eval.LocalizationCompetitiveConditionMetric) {
	fmt.Fprintln(output, "Competitive conditions:")

	if len(conditions) == 0 {
		fmt.Fprintln(output, "- <none>")

		return
	}

	for _, condition := range conditions {
		fmt.Fprintf(output, "- %s status=%s external=%t measured=%d infeasible=%d missing=%d budgeted=%d reproducible=%d top_k=%s",
			condition.Baseline,
			condition.Status,
			condition.External,
			condition.MeasuredTasks,
			condition.InfeasibleTasks,
			condition.MissingTasks,
			condition.BudgetedTasks,
			condition.ReproducibleTasks,
			formatLocalizationTopK(condition.TopKEditPathCoverage),
		)

		if len(condition.InfeasibilityReasons) > 0 {
			fmt.Fprintf(output, " infeasibility_reasons=%s", formatEvalCountMap(condition.InfeasibilityReasons))
		}

		if len(condition.ClaimImpacts) > 0 {
			fmt.Fprintf(output, " claim_impacts=%s", formatEvalCountMap(condition.ClaimImpacts))
		}

		if len(condition.ClientCoverage) > 0 {
			fmt.Fprintf(output, " clients=%s", formatEvalCountMap(condition.ClientCoverage))
		}

		if condition.InvalidClientClasses > 0 {
			fmt.Fprintf(output, " invalid_client_classifications=%d", condition.InvalidClientClasses)
		}

		if len(condition.FailureReasons) > 0 {
			fmt.Fprintf(output, " failure_reasons=%s", strings.Join(condition.FailureReasons, ","))
		}

		fmt.Fprintln(output)
	}
}

func printLocalizationCountMetric(label string, metric eval.LocalizationCountMetric) {
	fmt.Fprintf(output, "%s: %d/%d available=%t",
		label,
		metric.Numerator,
		metric.Denominator,
		metric.Available,
	)

	if len(metric.FailureReasons) > 0 {
		fmt.Fprintf(output, " failure_reasons=%s", strings.Join(metric.FailureReasons, ","))
	}

	fmt.Fprintln(output)
}

func printLocalizationUnifiedReport(report eval.LocalizationUnifiedReport) {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}

	fmt.Fprintf(output, "Localization report: %s %s schema=%s experimental=%t\n", status, textutil.Fallback(report.Name, "<unnamed corpus>"), report.SchemaVersion, report.Experimental)
	fmt.Fprintf(output, "Outcome: samples=%d expected_files=%d expected_symbols=%d expected_tests=%d top_k=%s broad_reads=%d/%d available=%t zero_hit_searches=%d/%d available=%t candidate_waste=%d/%d rate=%.3f path_waste=%d/%d rate=%.3f available=%t\n",
		report.Outcome.SampleCount,
		report.Outcome.ExpectedFiles,
		report.Outcome.ExpectedSymbols,
		report.Outcome.ExpectedTests,
		formatLocalizationTopK(report.Outcome.TopKEditPathCoverage),
		report.Outcome.BroadReadCount.Numerator,
		report.Outcome.BroadReadCount.Denominator,
		report.Outcome.BroadReadCount.Available,
		report.Outcome.ZeroHitSearchCount.Numerator,
		report.Outcome.ZeroHitSearchCount.Denominator,
		report.Outcome.ZeroHitSearchCount.Available,
		report.Outcome.CandidatePathWaste.CandidateWasteNumerator,
		report.Outcome.CandidatePathWaste.CandidateWasteDenominator,
		report.Outcome.CandidatePathWaste.CandidateWasteRate,
		report.Outcome.CandidatePathWaste.PathWasteNumerator,
		report.Outcome.CandidatePathWaste.PathWasteDenominator,
		report.Outcome.CandidatePathWaste.PathWasteRate,
		report.Outcome.CandidatePathWaste.Available,
	)
	fmt.Fprintf(output, "Trajectory: baseline=%s current=%s delta=%s current_broad_reads_available=%t current_zero_hit_available=%t current_waste_available=%t\n",
		report.Trajectory.Baseline.State,
		report.Trajectory.Current.State,
		report.Trajectory.Delta.State,
		report.Trajectory.Current.BroadReadCount.Available,
		report.Trajectory.Current.ZeroHitSearchCount.Available,
		report.Trajectory.Current.CandidatePathWaste.Available,
	)
	printLocalizationCompetitiveConditions(report.Outcome.CompetitiveConditions)
	fmt.Fprintf(output, "Cache/freshness: state=%s available=%t indexed=%t fresh=%t index_id_present=%t store=%s stale_paths=%d missing_files=%d rows=%d fresh_rows=%d stale_rows=%d non_current_rows=%d contract_rows=%d unresolved_gap_rows=%d provenance=%s\n",
		report.CacheFreshness.State,
		report.CacheFreshness.Available,
		report.CacheFreshness.Indexed,
		report.CacheFreshness.Fresh,
		report.CacheFreshness.IndexIDPresent,
		textutil.Fallback(report.CacheFreshness.StoreKind, "not_available"),
		report.CacheFreshness.StalePathCount,
		report.CacheFreshness.MissingFileCount,
		report.CacheFreshness.Rows,
		report.CacheFreshness.FreshRows,
		report.CacheFreshness.StaleRows,
		report.CacheFreshness.NonCurrentRows,
		report.CacheFreshness.ContractRows,
		report.CacheFreshness.UnresolvedGapRows,
		textutil.Fallback(report.CacheFreshness.FreshnessProvenanceSource, "not_available"),
	)

	reasonCountKeys := 0
	totalEvents := 0
	accepted := 0
	rejected := 0

	if report.Ranker.LearningSummary != nil {
		totalEvents = report.Ranker.LearningSummary.TotalEvents
		accepted = report.Ranker.LearningSummary.AcceptedCount
		rejected = report.Ranker.LearningSummary.RejectedCount
		reasonCountKeys = len(report.Ranker.LearningSummary.ReasonCounts)
	}

	fmt.Fprintf(output, "Ranker: state=%s available=%t weights_present=%t learning_log_present=%t weight_count=%d events=%d accepted=%d rejected=%d reasons=%d\n",
		report.Ranker.State,
		report.Ranker.Available,
		report.Ranker.WeightsPresent,
		report.Ranker.LearningLogPresent,
		report.Ranker.WeightCount,
		totalEvents,
		accepted,
		rejected,
		reasonCountKeys,
	)
	fmt.Fprintf(output, "Verification: state=%s available=%t status=%s checks=%d plans=%d feedback=%d full_suite_plan=%t\n",
		report.Verification.State,
		report.Verification.Available,
		report.Verification.Status,
		report.Verification.CheckCount,
		report.Verification.VerificationPlanCount,
		report.Verification.VerificationFeedbackCount,
		report.Verification.FullSuitePlanObserved,
	)
	fmt.Fprintf(output, "Audit metrics: joins=%s behavior_findings=%d cache_rows=%d cache_fresh=%d cache_stale=%d cache_non_current=%d ranker_events=%d ranker_accepted=%d ranker_rejected=%d freshness_blockers=%d stale_preventions=%d\n",
		formatLocalizationJoinCoverage(report.AuditMetrics.JoinCoverage),
		report.AuditMetrics.BehaviorFindingCount,
		report.AuditMetrics.ContextCache.Rows,
		report.AuditMetrics.ContextCache.FreshRows,
		report.AuditMetrics.ContextCache.StaleRows,
		report.AuditMetrics.ContextCache.NonCurrentRows,
		report.AuditMetrics.RankerLearning.TotalEvents,
		report.AuditMetrics.RankerLearning.AcceptedCount,
		report.AuditMetrics.RankerLearning.RejectedCount,
		report.AuditMetrics.FreshnessHealth.Blockers,
		report.AuditMetrics.FreshnessHealth.StaleCachedViewPreventions,
	)
	fmt.Fprintln(output, "Product-success gates:")

	for _, gate := range report.ProductSuccess {
		fmt.Fprintf(output, "- %s status=%s evidence=%s reason=%s\n",
			gate.Gate,
			gate.Status,
			textutil.Fallback(gate.Evidence, "not_available"),
			textutil.Fallback(gate.Reason, "not_available"),
		)
	}

	fmt.Fprintf(output, "Client/product: generic=%t client_specific_branches=%t areas=%s covered=%s missing=%s classification_rows=%d\n",
		report.ClientProduct.GenericSupport,
		report.ClientProduct.ClientSpecificBranches,
		formatEvalCountMap(report.ClientProduct.Areas),
		formatLocalizationStringSlice(report.ClientProduct.CoveredClassifications),
		formatLocalizationStringSlice(report.ClientProduct.MissingClassifications),
		len(report.ClientProduct.ClassificationRows),
	)
	fmt.Fprintln(output, "Client classifications:")

	for _, row := range report.ClientProduct.ClassificationRows {
		fmt.Fprintf(output, "- %s label=%q status=%s samples=%d reason=%s evidence=%s\n",
			row.ID,
			row.Label,
			row.Status,
			row.SampleCount,
			textutil.Fallback(row.Reason, "not_available"),
			textutil.Fallback(row.Evidence, "not_available"),
		)
	}

	fmt.Fprintf(output, "Privacy: policy=%s prompt_text=%t file_bodies=%t log_excerpts=%t secrets=%t\n",
		report.Privacy.EvidencePolicy,
		report.Privacy.PromptTextIncluded,
		report.Privacy.FileBodiesIncluded,
		report.Privacy.LogExcerptsIncluded,
		report.Privacy.SecretsIncluded,
	)

	for _, finding := range report.Findings {
		fmt.Fprintf(output, "  - %s\n", finding)
	}
}

func formatLocalizationTopK(metrics []eval.LocalizationTopKCoverageMetric) string {
	if len(metrics) == 0 {
		return "<none>"
	}

	parts := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		parts = append(parts, fmt.Sprintf("k=%d:%d/%d:%.3f:available=%t", metric.K, metric.Numerator, metric.Denominator, metric.Rate, metric.Available))
	}

	return strings.Join(parts, ",")
}

func formatLocalizationJoinCoverage(coverage eval.LocalizationAuditJoinCoverage) string {
	return fmt.Sprintf("outcome=%t,trajectory=%t,cache_freshness=%t,ranker=%t,verification=%t,audit_metrics=%t",
		coverage.Outcome,
		coverage.Trajectory,
		coverage.CacheFreshness,
		coverage.Ranker,
		coverage.Verification,
		coverage.AuditMetrics,
	)
}

func formatLocalizationStringSlice(values []string) string {
	if len(values) == 0 {
		return "<none>"
	}

	clean := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			clean = append(clean, value)
		}
	}

	if len(clean) == 0 {
		return "<none>"
	}

	return strings.Join(clean, ",")
}

func formatEvalCountMap(counts map[string]int) string {
	if len(counts) == 0 {
		return "<none>"
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
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}

	return strings.Join(parts, ", ")
}

func printReport(report eval.Report) {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}

	fmt.Fprintf(output, "%s %s\n", status, textutil.Fallback(report.Scenario, "<unnamed scenario>"))
	fmt.Fprintf(output, "Log: %s\n", report.LogPath)
	printMetrics(report.Metrics)

	if len(report.Findings) > 0 {
		fmt.Fprintln(output, "Findings:")

		for _, finding := range report.Findings {
			fmt.Fprintf(output, "- %s\n", finding)
		}
	}
}
