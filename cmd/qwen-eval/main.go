package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"qwen-bubbles/internal/eval"
)

func main() {
	var logPath string
	var scenarioPath string
	var writeScenarioPath string
	var jsonOut bool

	flag.StringVar(&logPath, "log", "", "path to a qwen-bubbles JSONL conversation log")
	flag.StringVar(&scenarioPath, "scenario", "", "path to a scenario JSON file")
	flag.StringVar(&writeScenarioPath, "write-scenario", "", "write a suggested scenario JSON file derived from the log")
	flag.BoolVar(&jsonOut, "json", false, "emit JSON instead of human-readable text")
	flag.Parse()

	if strings.TrimSpace(logPath) == "" {
		fmt.Fprintln(os.Stderr, "missing required -log path")
		os.Exit(2)
	}

	records, err := eval.LoadRecords(logPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if strings.TrimSpace(writeScenarioPath) != "" {
		name := strings.TrimSuffix(filepath.Base(writeScenarioPath), filepath.Ext(writeScenarioPath))
		scenario := eval.BuildSuggestedScenario(name, records)
		if err := eval.WriteScenario(writeScenarioPath, scenario); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}

	if strings.TrimSpace(scenarioPath) == "" {
		metrics := eval.Analyze(records)
		if jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(metrics)
			return
		}
		printMetrics(metrics)
		return
	}

	scenario, err := eval.LoadScenario(scenarioPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report := eval.Evaluate(logPath, records, scenario)
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	} else {
		printReport(report)
	}
	if !report.Passed {
		os.Exit(1)
	}
}

func printMetrics(metrics eval.Metrics) {
	fmt.Printf("Last user prompt: %s\n", fallback(metrics.LastUser, "<none>"))
	if metrics.DurationMillis > 0 {
		fmt.Printf("Duration: %dms\n", metrics.DurationMillis)
	}
	fmt.Printf("Tool requests: %d (broad: %d)\n", metrics.ToolRequests, metrics.BroadToolRequests)
	fmt.Printf("Repeated dead ends: %d\n", metrics.RepeatedDeadEndRejections+metrics.RepeatedToolRejections)
	if metrics.SummaryRejections > 0 {
		fmt.Printf("Summary rejections: %d", metrics.SummaryRejections)
		if len(metrics.SummaryRejectionLanes) > 0 {
			fmt.Printf(" (%s)", strings.Join(metrics.SummaryRejectionLanes, ", "))
		}
		fmt.Println()
	}
	fmt.Printf("Verification: %s (%d checks)\n", verificationStatus(metrics), metrics.VerificationChecks)
	if len(metrics.VerificationPlans) > 0 {
		plan := metrics.VerificationPlans[len(metrics.VerificationPlans)-1]
		fmt.Printf("Verification plan: confidence=%s", fallback(plan.Confidence, "unknown"))
		if strings.TrimSpace(plan.Coverage) != "" {
			fmt.Printf(" coverage=%s", plan.Coverage)
		}
		if strings.TrimSpace(plan.RiskLevel) != "" {
			fmt.Printf(" risk=%s", plan.RiskLevel)
		}
		if plan.UsesFullSuite {
			fmt.Print(" full-suite=true")
		}
		if len(plan.ExpectedFailures) > 0 {
			fmt.Printf(" expected=%s", strings.Join(plan.ExpectedFailures, ","))
		}
		fmt.Println()
	}
	if len(metrics.ReadinessChecks) > 0 {
		readiness := metrics.ReadinessChecks[len(metrics.ReadinessChecks)-1]
		fmt.Printf("Readiness: ready=%t", readiness.Ready)
		if strings.TrimSpace(readiness.Lane) != "" {
			fmt.Printf(" lane=%s", readiness.Lane)
		}
		if strings.TrimSpace(readiness.Confidence) != "" {
			fmt.Printf(" confidence=%s", readiness.Confidence)
		}
		if strings.TrimSpace(readiness.Action) != "" {
			fmt.Printf(" action=%s", readiness.Action)
		}
		if len(readiness.EvidencePaths) > 0 {
			fmt.Printf(" evidence_paths=%d", len(readiness.EvidencePaths))
		}
		if strings.TrimSpace(readiness.Reason) != "" {
			fmt.Printf(" reason=%s", readiness.Reason)
		}
		fmt.Println()
	}
	if len(metrics.DeepeningAttempts) > 0 {
		deepening := metrics.DeepeningAttempts[len(metrics.DeepeningAttempts)-1]
		fmt.Printf("Deepening: attempted=%t", deepening.Attempted)
		if deepening.Attempt > 0 {
			fmt.Printf(" attempt=%d", deepening.Attempt)
		}
		if deepening.Exhausted {
			fmt.Print(" exhausted=true")
		}
		if strings.TrimSpace(deepening.Lane) != "" {
			fmt.Printf(" lane=%s", deepening.Lane)
		}
		if strings.TrimSpace(deepening.Action) != "" {
			fmt.Printf(" action=%s", deepening.Action)
		}
		if len(deepening.EvidencePaths) > 0 {
			fmt.Printf(" evidence_paths=%d", len(deepening.EvidencePaths))
		}
		if strings.TrimSpace(deepening.Reason) != "" {
			fmt.Printf(" reason=%s", deepening.Reason)
		}
		fmt.Println()
	}
	if len(metrics.FinalAnswerCritics) > 0 {
		critic := metrics.FinalAnswerCritics[len(metrics.FinalAnswerCritics)-1]
		fmt.Printf("Final critic: passed=%t", critic.Passed)
		if strings.TrimSpace(critic.Contract) != "" {
			fmt.Printf(" contract=%s", critic.Contract)
		}
		if strings.TrimSpace(critic.Action) != "" {
			fmt.Printf(" action=%s", critic.Action)
		}
		if critic.Repaired {
			fmt.Print(" repaired=true")
		}
		if len(critic.Findings) > 0 {
			fmt.Printf(" findings=%d", len(critic.Findings))
		}
		fmt.Println()
	}
	if len(metrics.EffortPolicies) > 0 {
		policy := metrics.EffortPolicies[len(metrics.EffortPolicies)-1]
		fmt.Printf("Effort policy: lane=%s level=%s", fallback(policy.Lane, "unknown"), fallback(policy.Level, "unknown"))
		if policy.AnalysisToolBudget > 0 {
			fmt.Printf(" analysis_budget=%d", policy.AnalysisToolBudget)
		}
		if policy.ImplementToolBudget > 0 {
			fmt.Printf(" implement_budget=%d", policy.ImplementToolBudget)
		}
		if policy.DeepeningPasses > 0 {
			fmt.Printf(" deepening_passes=%d", policy.DeepeningPasses)
		}
		if policy.MaxOutputTokens > 0 {
			fmt.Printf(" max_output_tokens=%d", policy.MaxOutputTokens)
		}
		if policy.ReasoningBudget != 0 {
			fmt.Printf(" reasoning_budget=%d", policy.ReasoningBudget)
		}
		if strings.TrimSpace(policy.ConfidenceFloor) != "" {
			fmt.Printf(" confidence_floor=%s", policy.ConfidenceFloor)
		}
		fmt.Println()
	}
	if len(metrics.BudgetLedgers) > 0 {
		ledger := metrics.BudgetLedgers[len(metrics.BudgetLedgers)-1]
		fmt.Printf("Budget ledger: lane=%s stop=%s model_calls=%d deepening=%d/%d evidence=%d",
			fallback(ledger.Lane, "unknown"),
			fallback(ledger.StopReason, "unknown"),
			ledger.UsedModelCalls,
			ledger.UsedDeepeningPasses,
			ledger.MaxDeepeningPasses,
			ledger.TotalEvidencePaths,
		)
		if ledger.BudgetExhausted {
			fmt.Print(" exhausted=true")
		}
		fmt.Println()
	}
	if len(metrics.ClaimConfidences) > 0 {
		confidence := metrics.ClaimConfidences[len(metrics.ClaimConfidences)-1]
		fmt.Printf("Claim confidence: lane=%s confidence=%s evidence_paths=%d unresolved=%d\n",
			fallback(confidence.Lane, "unknown"),
			fallback(confidence.Confidence, "unknown"),
			len(confidence.EvidencePaths),
			len(confidence.UnresolvedUncertainty),
		)
	}
	if len(metrics.RouteAmbiguities) > 0 {
		ambiguity := metrics.RouteAmbiguities[len(metrics.RouteAmbiguities)-1]
		fmt.Printf("Route ambiguity: selected=%s ambiguous=%t signals=%s\n",
			fallback(ambiguity.SelectedLane, "unknown"),
			ambiguity.Ambiguous,
			strings.Join(ambiguity.Signals, ","),
		)
	}
	if len(metrics.LongHorizonStates) > 0 {
		state := metrics.LongHorizonStates[len(metrics.LongHorizonStates)-1]
		fmt.Printf("Long-horizon state: action=%s", fallback(state.Action, "unknown"))
		if strings.TrimSpace(state.MemoryKind) != "" {
			fmt.Printf(" kind=%s", state.MemoryKind)
		}
		if strings.TrimSpace(state.SourceLane) != "" {
			fmt.Printf(" source_lane=%s", state.SourceLane)
		}
		if state.Stage > 0 {
			fmt.Printf(" stage=%d", state.Stage)
		}
		if strings.TrimSpace(state.FocusPath) != "" {
			fmt.Printf(" focus=%s", state.FocusPath)
		}
		if len(state.Remaining) > 0 {
			fmt.Printf(" remaining=%d", len(state.Remaining))
		}
		if len(state.ChosenTargets) > 0 {
			fmt.Printf(" targets=%d", len(state.ChosenTargets))
		}
		if len(state.OpenGaps) > 0 {
			fmt.Printf(" open_gaps=%d", len(state.OpenGaps))
		}
		if state.Resumable {
			fmt.Print(" resumable=true")
		}
		fmt.Println()
	}
	if len(metrics.ChangedFiles) > 0 {
		fmt.Printf("Changed files: %s\n", strings.Join(metrics.ChangedFiles, ", "))
	}
	if len(metrics.EvidencePaths) > 0 {
		fmt.Printf("Evidence paths: %s\n", strings.Join(metrics.EvidencePaths, ", "))
	}
	if len(metrics.LanguageCapabilities) > 0 {
		fmt.Println("Language capabilities:")
		for _, capability := range metrics.LanguageCapabilities {
			line := fmt.Sprintf("- %s", fallback(capability.Language, "unknown"))
			if len(capability.Tools) > 0 {
				line += " tools=" + strings.Join(capability.Tools, ",")
			}
			if len(capability.Signals) > 0 {
				line += " signals=" + strings.Join(capability.Signals, ",")
			}
			fmt.Println(line)
		}
	}
	if len(metrics.Phases) > 0 {
		fmt.Println("Phases:")
		for _, phase := range metrics.Phases {
			fmt.Printf("- %s / %s [%s]\n", fallback(phase.Lane, "unknown"), fallback(phase.Phase, "unknown"), fallback(phase.State, "unknown"))
		}
	}
	if len(metrics.RoutePlans) > 0 {
		plan := metrics.RoutePlans[len(metrics.RoutePlans)-1]
		fmt.Printf("Route plan: initial=%s", fallback(plan.InitialLane, "unknown"))
		if strings.TrimSpace(plan.FollowupLane) != "" {
			fmt.Printf(" followup=%s", plan.FollowupLane)
		}
		if strings.TrimSpace(plan.Reason) != "" {
			fmt.Printf(" reason=%s", plan.Reason)
		}
		if strings.TrimSpace(plan.Contract) != "" {
			fmt.Printf(" contract=%s", plan.Contract)
		}
		if strings.TrimSpace(plan.Thoroughness) != "" {
			fmt.Printf(" thoroughness=%s", plan.Thoroughness)
		}
		fmt.Println()
	}
	if len(metrics.Replans) > 0 {
		replan := metrics.Replans[len(metrics.Replans)-1]
		fmt.Printf("Replan recommendation: %s -> %s", fallback(replan.FromLane, "unknown"), fallback(replan.ToLane, "unknown"))
		if strings.TrimSpace(replan.Reason) != "" {
			fmt.Printf(" reason=%s", replan.Reason)
		}
		fmt.Println()
	}
}

func printReport(report eval.Report) {
	status := "PASS"
	if !report.Passed {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, fallback(report.Scenario, "<unnamed scenario>"))
	fmt.Printf("Log: %s\n", report.LogPath)
	printMetrics(report.Metrics)
	if len(report.Findings) > 0 {
		fmt.Println("Findings:")
		for _, finding := range report.Findings {
			fmt.Printf("- %s\n", finding)
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
