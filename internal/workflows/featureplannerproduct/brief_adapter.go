package featureplannerproduct

import (
	"idiom/internal/workflows/featureplanner"
	"idiom/internal/workflows/productworkflow"
)

func BriefFromImplementationHandoff(handoff productworkflow.ImplementationHandoff) *featureplanner.ProductWorkflowBrief {
	brief := &featureplanner.ProductWorkflowBrief{
		EffortID:                  handoff.EffortID,
		TaskID:                    handoff.TaskID,
		EffortRoot:                handoff.EffortRoot,
		ArtifactRootPath:          handoff.ArtifactRootPath,
		StatusBucket:              handoff.StatusBucket,
		CurrentPhase:              handoff.CurrentPhase,
		CurrentState:              handoff.CurrentState,
		NextValidActions:          append([]string(nil), handoff.NextValidActions...),
		CoverageTier:              handoff.CoverageTier,
		CoverageGatePassed:        handoff.CoverageGatePassed,
		CoverageBlockers:          append([]string(nil), handoff.CoverageBlockers...),
		CoverageBlockerKinds:      append([]string(nil), handoff.CoverageBlockerKinds...),
		AcceptedADRIDs:            append([]string(nil), handoff.AcceptedADRIDs...),
		AcceptedADRPaths:          append([]string(nil), handoff.AcceptedADRPaths...),
		FilesToModify:             append([]string(nil), handoff.FilesToModify...),
		ContextEvidencePaths:      append([]string(nil), handoff.ContextEvidencePaths...),
		RequiredBehavior:          append([]string(nil), handoff.RequiredBehavior...),
		Tests:                     append([]string(nil), handoff.Tests...),
		Acceptance:                append([]string(nil), handoff.Acceptance...),
		Constraints:               append([]string(nil), handoff.Constraints...),
		Decisions:                 append([]string(nil), handoff.Decisions...),
		Blockers:                  append([]string(nil), handoff.Blockers...),
		AuthorizedNewPackageRoots: nil,
	}
	for _, req := range handoff.PRDRequirements {
		brief.PRDRequirements = append(brief.PRDRequirements, featureplanner.PRDRequirementBrief{
			ID:          req.ID,
			Priority:    req.Priority,
			Statement:   req.Statement,
			Testability: req.Testability,
			Metric:      req.Metric,
			Obligations: append([]string(nil), req.Obligations...),
		})
	}
	for _, obligation := range handoff.TDDObligations {
		brief.TDDObligations = append(brief.TDDObligations, featureplanner.TDDObligationBrief{
			ID:             obligation.ID,
			Behavior:       obligation.Behavior,
			Command:        obligation.Command,
			RequiredBefore: obligation.RequiredBefore,
			Status:         obligation.Status,
		})
	}
	return brief
}
