package productworkflow

import "strings"

type ProductWorkflowStatus string

const (
	ProductWorkflowStatusApplied         ProductWorkflowStatus = "applied"
	ProductWorkflowStatusAccepted        ProductWorkflowStatus = "accepted"
	ProductWorkflowStatusBlocked         ProductWorkflowStatus = "blocked"
	ProductWorkflowStatusClosed          ProductWorkflowStatus = "closed"
	ProductWorkflowStatusComplete        ProductWorkflowStatus = "complete"
	ProductWorkflowStatusError           ProductWorkflowStatus = "error"
	ProductWorkflowStatusFail            ProductWorkflowStatus = "fail"
	ProductWorkflowStatusFailed          ProductWorkflowStatus = "failed"
	ProductWorkflowStatusNeedsInspection ProductWorkflowStatus = "needs_inspection"
	ProductWorkflowStatusNotApplicable   ProductWorkflowStatus = "not_applicable"
	ProductWorkflowStatusOK              ProductWorkflowStatus = "ok"
	ProductWorkflowStatusPartial         ProductWorkflowStatus = "partial"
	ProductWorkflowStatusPass            ProductWorkflowStatus = "pass"
	ProductWorkflowStatusPassAfterRepair ProductWorkflowStatus = "pass_after_repair"
	ProductWorkflowStatusPassed          ProductWorkflowStatus = "passed"
	ProductWorkflowStatusPending         ProductWorkflowStatus = "pending"
	ProductWorkflowStatusPlanned         ProductWorkflowStatus = "planned"
	ProductWorkflowStatusReady           ProductWorkflowStatus = "ready"
	ProductWorkflowStatusResumed         ProductWorkflowStatus = "resumed"

	ProductWorkflowSeverityError = "error"

	ProductStatusAuditRemediationAction = "fix product workflow findings, then rerun `idiom product status --effort-root <path>`"

	productStatusMigrationTempSuffix = ".status-migration.tmp"
)

func ProductWorkflowStatusIsPass(status string) bool {
	return ProductWorkflowStatus(strings.TrimSpace(status)) == ProductWorkflowStatusPass
}

func ProductWorkflowStatusIsAccepted(status string) bool {
	switch ProductWorkflowStatus(strings.TrimSpace(status)) {
	case ProductWorkflowStatusAccepted, ProductWorkflowStatusPass:
		return true
	default:
		return false
	}
}
