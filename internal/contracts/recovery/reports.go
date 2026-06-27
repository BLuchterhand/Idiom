package recovery

type ReportType string
type ReportStatus string

const (
	ReportTypeInspect   ReportType = "idiom.recover.inspect.v1"
	ReportTypeDecompose ReportType = "idiom.recover.decompose.v1"
	ReportTypeStatus    ReportType = "idiom.recover.status.v1"
	ReportTypePromote   ReportType = "idiom.recover.promote.v1"
	ReportTypeReconcile ReportType = "idiom.recover.reconcile.v1"
)

const (
	ReportStatusBlocked  ReportStatus = "blocked"
	ReportStatusError    ReportStatus = "error"
	ReportStatusFailed   ReportStatus = "failed"
	ReportStatusPromoted ReportStatus = "promoted"
)

func ReportTypes() []ReportType {
	return []ReportType{
		ReportTypeInspect,
		ReportTypeDecompose,
		ReportTypeStatus,
		ReportTypePromote,
		ReportTypeReconcile,
	}
}

func KnownReportType(value ReportType) bool {
	for _, known := range ReportTypes() {
		if value == known {
			return true
		}
	}

	return false
}

func ReportStatuses() []ReportStatus {
	return []ReportStatus{
		ReportStatusBlocked,
		ReportStatusError,
		ReportStatusFailed,
		ReportStatusPromoted,
	}
}

func KnownReportStatus(value ReportStatus) bool {
	for _, known := range ReportStatuses() {
		if value == known {
			return true
		}
	}

	return false
}
