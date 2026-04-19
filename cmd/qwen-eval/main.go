package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

	var logPath string
	var scenarioPath string
	var writeScenarioPath string
	var jsonOut bool

	fs := flag.NewFlagSet("qwen-eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&logPath, "log", "", "path to a qwen-bubbles JSONL conversation log")
	fs.StringVar(&scenarioPath, "scenario", "", "path to a scenario JSON file")
	fs.StringVar(&writeScenarioPath, "write-scenario", "", "write a suggested scenario JSON file derived from the log")
	fs.BoolVar(&jsonOut, "json", false, "emit JSON instead of human-readable text")
	if err := fs.Parse(args); err != nil {
		return 2
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

func printMetrics(metrics eval.Metrics) {
	fmt.Fprintf(output, "Last user prompt: %s\n", fallback(metrics.LastUser, "<none>"))
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
	fmt.Fprintf(output, "Verification: %s (%d checks)\n", verificationStatus(metrics), metrics.VerificationChecks)
	if len(metrics.VerificationPlans) > 0 {
		plan := metrics.VerificationPlans[len(metrics.VerificationPlans)-1]
		fmt.Fprintf(output, "Verification plan: confidence=%s", fallback(plan.Confidence, "unknown"))
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
		fmt.Fprintf(output, "Effort policy: lane=%s level=%s", fallback(policy.Lane, "unknown"), fallback(policy.Level, "unknown"))
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
			fallback(ledger.Lane, "unknown"),
			fallback(ledger.StopReason, "unknown"),
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
			fallback(rails.Lane, "unknown"),
			fallback(rails.TaskFamily, "unknown"),
			fallback(rails.PermissionProfile, "unknown"),
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
			fallback(confidence.Lane, "unknown"),
			fallback(confidence.Confidence, "unknown"),
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
			fallback(ambiguity.SelectedLane, "unknown"),
			ambiguity.Ambiguous,
			strings.Join(ambiguity.Signals, ","),
		)
	}
	if len(metrics.LongHorizonStates) > 0 {
		state := metrics.LongHorizonStates[len(metrics.LongHorizonStates)-1]
		fmt.Fprintf(output, "Long-horizon state: action=%s", fallback(state.Action, "unknown"))
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
			line := fmt.Sprintf("- %s", fallback(capability.Language, "unknown"))
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
			fmt.Fprintf(output, "- %s / %s [%s]\n", fallback(phase.Lane, "unknown"), fallback(phase.Phase, "unknown"), fallback(phase.State, "unknown"))
		}
	}
	if len(metrics.RoutePlans) > 0 {
		plan := metrics.RoutePlans[len(metrics.RoutePlans)-1]
		fmt.Fprintf(output, "Route plan: initial=%s", fallback(plan.InitialLane, "unknown"))
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
		fmt.Fprintf(output, "Replan recommendation: %s -> %s", fallback(replan.FromLane, "unknown"), fallback(replan.ToLane, "unknown"))
		if strings.TrimSpace(replan.Reason) != "" {
			fmt.Fprintf(output, " reason=%s", replan.Reason)
		}
		fmt.Fprintln(output)
	}
}

func printReport(report eval.Report) {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Fprintf(output, "%s %s\n", status, fallback(report.Scenario, "<unnamed scenario>"))
	fmt.Fprintf(output, "Log: %s\n", report.LogPath)
	printMetrics(report.Metrics)
	if len(report.Findings) > 0 {
		fmt.Fprintln(output, "Findings:")
		for _, finding := range report.Findings {
			fmt.Fprintf(output, "- %s\n", finding)
		}
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
