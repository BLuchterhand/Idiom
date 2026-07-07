package implementationdispatch

import (
	"context"
	"strings"
)

type PlanDocumentPreflightRunner func(context.Context, PlanDocument) PlanDocumentPreflightResult

func PreflightPlanDocuments(ctx context.Context, planDocuments []PlanDocument, runner PlanDocumentPreflightRunner) []PlanDocumentPreflightResult {
	if runner == nil {
		return nil
	}

	var blocked []PlanDocumentPreflightResult

	for _, doc := range planDocuments {
		if strings.TrimSpace(doc.Path) == "" {
			continue
		}

		if doc.ReferenceOnly {
			continue
		}

		result := runner(ctx, doc)
		if !result.OK || len(result.Blockers) > 0 {
			if strings.TrimSpace(result.Path) == "" {
				result.Path = doc.Path
			}

			blocked = append(blocked, result)
		}
	}

	return blocked
}
