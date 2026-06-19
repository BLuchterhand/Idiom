package app

import "idiom/internal/surfaces/tui/implementationdispatch"

type implementationDispatchDirtyAllowance = implementationdispatch.DirtyAllowance

func implementationDispatchPlanDocumentsWithDirtyAllowance(prompt string, docs []implementationDispatchPlanDocument) []implementationDispatchPlanDocument {
	return implementationdispatch.PlanDocumentsWithDirtyAllowance(prompt, docs)
}
