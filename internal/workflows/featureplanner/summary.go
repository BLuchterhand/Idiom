package featureplanner

import (
	"fmt"
	"strings"
)

func FormatStateSummary(state FeatureState, statePath string) string {
	var b strings.Builder

	fmt.Fprintf(&b, "feature-planner: %s\n", state.Status)

	if statePath != "" {
		fmt.Fprintf(&b, "state: %s\n", statePath)
	}

	if len(state.Atoms) > 0 {
		fmt.Fprintf(&b, "atoms: %d\n", len(state.Atoms))

		var atomIDs []string

		for _, atom := range state.Atoms {
			atomIDs = append(atomIDs, atom.ID)
		}

		fmt.Fprintf(&b, "atom_ids: %s\n", strings.Join(atomIDs, ", "))
	}

	if sddRefs := SDDRefsForSummary(state.Plan); len(sddRefs) > 0 {
		fmt.Fprintf(&b, "sdd_refs: %s\n", strings.Join(sddRefs, ", "))
	}

	if len(state.Plan.TDDContract.Obligations) > 0 {
		var tddIDs []string

		for _, obligation := range state.Plan.TDDContract.Obligations {
			tddIDs = append(tddIDs, obligation.ID)
		}

		fmt.Fprintf(&b, "tdd_obligations: %s\n", strings.Join(tddIDs, ", "))
	}

	if len(state.OpenQuestions) > 0 {
		openCount := CountOpenPlannerQuestions(state)
		if openCount > 0 {
			fmt.Fprintf(&b, "open questions: %d\n", openCount)
		} else {
			fmt.Fprintf(&b, "answered questions: %d\n", len(state.OpenQuestions))
		}

		for _, question := range state.OpenQuestions {
			if strings.TrimSpace(question.QuestionSummary) != "" {
				fmt.Fprintf(&b, "question: %s\n", question.QuestionSummary)
			}
			if questionOpen(question) && strings.TrimSpace(question.AuthorityRequired) != "" && strings.TrimSpace(question.AuthorityRequired) != "operator_approval" {
				fmt.Fprintf(&b, "answer_format: %s\n", plannerQuestionAnswerFormat(question.AuthorityRequired))
			}

			if strings.TrimSpace(question.Answer) != "" {
				fmt.Fprintf(&b, "answer: %s\n", question.Answer)
			}
		}
	}

	if len(state.Blockers) > 0 && CountOpenPlannerQuestions(state) > 0 {
		fmt.Fprintf(&b, "blockers: blocked by open planner question; full diagnostics are saved in the feature-planner state\n")
	} else if len(state.Blockers) > 0 {
		fmt.Fprintf(&b, "blockers: %s\n", strings.Join(state.Blockers, "; "))
	}

	nextActions := DeriveNextValidActions(state)
	if len(nextActions) == 0 {
		nextActions = state.DerivedNextValidActions
	}
	if len(nextActions) > 0 {
		fmt.Fprintf(&b, "next: %s\n", strings.Join(nextActions, ", "))
	}

	return strings.TrimSpace(b.String())
}

func plannerQuestionAnswerFormat(authorityRequired string) string {
	authorityRequired = strings.TrimSpace(authorityRequired)
	if authorityRequired == "accepted_source" {
		return `JSON object with answer, answer_source_kind="accepted_source", evidence_refs, and source_facts [{path, symbol, signature}]`
	}

	return fmt.Sprintf("JSON object with answer, answer_source_kind=%q, and evidence_refs", authorityRequired)
}

func questionOpen(question PlannerQuestion) bool {
	status := strings.TrimSpace(question.Status)
	return status == "" || strings.EqualFold(status, "open")
}

func CountOpenPlannerQuestions(state FeatureState) int {
	open := 0

	for _, question := range state.OpenQuestions {
		if questionOpen(question) {
			open++
		}
	}

	return open
}

// SDDRefsForSummary returns stable traceability references for operator-facing
// completion summaries.
func SDDRefsForSummary(plan FeaturePlan) []string {
	seen := map[string]bool{}

	var refs []string

	add := func(values []string) {
		for _, value := range values {
			if value == "" || seen[value] {
				continue
			}

			seen[value] = true
			refs = append(refs, value)
		}
	}
	add(plan.SDDContract.Refs)
	add(plan.SDDContract.PRDRequirementRefs)
	add(plan.SDDContract.ADRRefs)

	for _, atom := range plan.Atoms {
		add(atom.SDDRefs)
	}

	return refs
}
