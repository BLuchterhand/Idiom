package implementationdispatch

import (
	"context"
	"strings"
)

type PlanDocumentPreflightRunner func(context.Context, PlanDocumentPreflightInput) PlanDocumentPreflightResult

func PreflightPlanDocuments(ctx context.Context, input PlanDocumentsPreflightInput) []PlanDocumentPreflightResult {
	runner := input.Runner
	if runner == nil {
		runner = PreflightPlanDocument
	}

	var blocked []PlanDocumentPreflightResult

	for _, doc := range input.PlanDocuments {
		if strings.TrimSpace(doc.Path) == "" {
			continue
		}

		if doc.ReferenceOnly {
			continue
		}

		result := runner(ctx, PlanDocumentPreflightInput{
			WorkspaceRoot: input.WorkspaceRoot,
			WorkspaceCWD:  input.WorkspaceCWD,
			Document:      doc,
			Timeout:       input.Timeout,
		})
		if !result.OK || len(result.Blockers) > 0 {
			if strings.TrimSpace(result.Path) == "" {
				result.Path = doc.Path
			}

			blocked = append(blocked, result)
		}
	}

	return blocked
}
