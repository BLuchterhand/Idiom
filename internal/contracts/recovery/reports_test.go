package recovery

import "testing"

func TestRecoveryReportVocabularyIsKnown(t *testing.T) {
	for _, reportType := range ReportTypes() {
		if !KnownReportType(reportType) {
			t.Fatalf("KnownReportType(%q) = false", reportType)
		}
	}

	for _, status := range ReportStatuses() {
		if !KnownReportStatus(status) {
			t.Fatalf("KnownReportStatus(%q) = false", status)
		}
	}

	if KnownReportType(ReportType("idiom.recover.typo.v1")) {
		t.Fatal("KnownReportType accepted unknown report type")
	}
	if KnownReportStatus(ReportStatus("typo")) {
		t.Fatal("KnownReportStatus accepted unknown status")
	}
}
