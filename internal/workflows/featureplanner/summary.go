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

			if strings.TrimSpace(question.Answer) != "" {
				fmt.Fprintf(&b, "answer: %s\n", question.Answer)
			}
		}
	}

	if len(state.Blockers) > 0 {
		fmt.Fprintf(&b, "blockers: %s\n", strings.Join(state.Blockers, "; "))
	}

	if len(state.DerivedNextValidActions) > 0 {
		fmt.Fprintf(&b, "next: %s\n", strings.Join(state.DerivedNextValidActions, ", "))
	}

	return strings.TrimSpace(b.String())
}

func CountOpenPlannerQuestions(state FeatureState) int {
	open := 0

	for _, question := range state.OpenQuestions {
		if strings.TrimSpace(question.Status) == "" || strings.EqualFold(question.Status, "open") {
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
