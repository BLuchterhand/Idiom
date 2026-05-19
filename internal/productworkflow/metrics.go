package productworkflow

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type ProductWorkflowMetricsError struct {
	Op   string
	Path string
	Err  error
}

func (e ProductWorkflowMetricsError) Error() string {
	if e.Err == nil {
		return strings.TrimSpace(e.Op + " " + e.Path)
	}
	return fmt.Sprintf("%s %s: %s", e.Op, e.Path, metricsErrorMessage(e.Err))
}

func (e ProductWorkflowMetricsError) Unwrap() error {
	return e.Err
}

func BuildProductWorkflowMetrics(absRoot string) (ProductWorkflowMetrics, error) {
	wsData, err := readMetricsArtifact(absRoot, "workflow_state.json")
	if err != nil {
		return ProductWorkflowMetrics{}, err
	}
	ws, err := ParseWorkflowStateJSON(wsData)
	if err != nil {
		return ProductWorkflowMetrics{}, metricsArtifactError("parse", "workflow_state.json", err)
	}

	tddData, err := readMetricsArtifact(absRoot, "tdd.json")
	if err != nil {
		return ProductWorkflowMetrics{}, err
	}
	tdd, err := ParseTDDLedgerJSON(tddData)
	if err != nil {
		return ProductWorkflowMetrics{}, metricsArtifactError("parse", "tdd.json", err)
	}

	evidenceData, err := readMetricsArtifact(absRoot, filepath.Join("evidence", "evidence.jsonl"))
	if err != nil {
		return ProductWorkflowMetrics{}, err
	}
	cards, err := ParseEvidenceCardsJSONL(evidenceData)
	if err != nil {
		return ProductWorkflowMetrics{}, metricsArtifactError("parse", filepath.Join("evidence", "evidence.jsonl"), err)
	}

	traceData, err := readMetricsArtifact(absRoot, filepath.Join("evidence", "traceability_matrix.json"))
	if err != nil {
		return ProductWorkflowMetrics{}, err
	}
	matrix, err := ParseTraceabilityMatrixJSON(traceData)
	if err != nil {
		return ProductWorkflowMetrics{}, metricsArtifactError("parse", filepath.Join("evidence", "traceability_matrix.json"), err)
	}

	ledgerData, err := readMetricsArtifact(absRoot, "implementation_ledger.jsonl")
	if err != nil {
		return ProductWorkflowMetrics{}, err
	}
	ledger, err := ParseImplementationLedgerJSONL(ledgerData)
	if err != nil {
		return ProductWorkflowMetrics{}, metricsArtifactError("parse", "implementation_ledger.jsonl", err)
	}

	prdData, err := readMetricsArtifact(absRoot, "prd.md")
	if err != nil {
		return ProductWorkflowMetrics{}, err
	}
	prd, err := ParsePRDMarkdown(prdData)
	if err != nil {
		return ProductWorkflowMetrics{}, metricsArtifactError("parse", "prd.md", err)
	}

	adrIndexData, err := readMetricsArtifact(absRoot, filepath.Join("adrs", "index.json"))
	if err != nil {
		return ProductWorkflowMetrics{}, err
	}
	adrIndex, err := ParseADRIndexJSON(adrIndexData)
	if err != nil {
		return ProductWorkflowMetrics{}, metricsArtifactError("parse", filepath.Join("adrs", "index.json"), err)
	}

	gateStatusCounts := map[string]int{}
	for _, gate := range ws.Coverage.GateResults {
		if err := countBoundedMetricValue(gateStatusCounts, "coverage.gate_results.status", gate.Status, allowedMetricGateStatuses); err != nil {
			return ProductWorkflowMetrics{}, metricsArtifactError("validate", "workflow_state.json", err)
		}
	}

	tddStatusCounts := map[string]int{}
	tddTypeCounts := map[string]int{}
	for _, obligation := range tdd.Obligations {
		if err := countBoundedMetricValue(tddStatusCounts, "tdd.obligations.status", obligation.Status, allowedMetricTDDStatuses); err != nil {
			return ProductWorkflowMetrics{}, metricsArtifactError("validate", "tdd.json", err)
		}
		if err := countBoundedMetricValue(tddTypeCounts, "tdd.obligations.type", obligation.Type, allowedMetricTDDTypes); err != nil {
			return ProductWorkflowMetrics{}, metricsArtifactError("validate", "tdd.json", err)
		}
	}

	ledgerTypeCounts := map[string]int{}
	ledgerAtomCount := 0
	for _, record := range ledger {
		if err := countBoundedMetricValue(ledgerTypeCounts, "implementation_ledger.record_type", record.RecordType, allowedMetricLedgerRecordTypes); err != nil {
			return ProductWorkflowMetrics{}, metricsArtifactError("validate", "implementation_ledger.jsonl", err)
		}
		if strings.HasPrefix(record.RecordType, "atom_") {
			ledgerAtomCount++
		}
	}

	reportTypeCounts, reportStatusCounts, err := collectReportMetrics(absRoot)
	if err != nil {
		return ProductWorkflowMetrics{}, err
	}

	prdEvidenceRefCount := 0
	for _, requirement := range prd.Requirements {
		prdEvidenceRefCount += len(requirement.EvidenceRefs)
	}

	adrEvidenceRefCount := 0
	adrPRDRefCount := 0
	adrTDDObligationRefCount := 0
	for _, entry := range adrIndex.ADRs {
		adrRelPath := filepath.Join("adrs", entry.Path)
		adrData, readErr := readMetricsArtifact(absRoot, adrRelPath)
		if readErr != nil {
			return ProductWorkflowMetrics{}, readErr
		}
		adrDoc, parseErr := ParseADRMarkdown(adrData)
		if parseErr != nil {
			return ProductWorkflowMetrics{}, metricsArtifactError("parse", adrRelPath, parseErr)
		}
		adrEvidenceRefCount += len(adrDoc.FrontMatter.EvidenceRefs)
		adrPRDRefCount += len(adrDoc.FrontMatter.PRDRefs)
		adrTDDObligationRefCount += len(adrDoc.FrontMatter.TDDObligations)
	}

	return ProductWorkflowMetrics{
		CurrentPhase:          ws.CurrentPhase,
		CurrentState:          ws.CurrentState,
		StateRevision:         ws.StateRevision,
		NextValidActionCount:  len(ws.NextValidActions),
		BlockerCount:          len(ws.Blockers),
		OpenObligationCount:   len(ws.OpenObligations),
		GateStatusCounts:      gateStatusCounts,
		TDDObligationTotal:    len(tdd.Obligations),
		TDDStatusCounts:       tddStatusCounts,
		TDDTypeCounts:         tddTypeCounts,
		LedgerRecordCount:     len(ledger),
		LedgerAtomRecordCount: ledgerAtomCount,
		LedgerTypeCounts:      ledgerTypeCounts,
		ReportTypeCounts:      reportTypeCounts,
		ReportStatusCounts:    reportStatusCounts,
		ArtifactPathCount:     len(ws.ArtifactPaths),
		EvidenceCardCount:     len(cards),
		TraceabilityRowCount:  len(matrix.Matrix),
		PRDRequirementCount:   len(prd.Requirements),
		ADRCount:              len(adrIndex.ADRs),
		EvidenceLinkCounts: map[string]int{
			"prd_requirement_evidence_refs": prdEvidenceRefCount,
			"adr_evidence_refs":             adrEvidenceRefCount,
			"adr_prd_refs":                  adrPRDRefCount,
			"adr_tdd_obligation_refs":       adrTDDObligationRefCount,
			"traceability_evidence_links":   len(matrix.Matrix),
		},
	}, nil
}

func readMetricsArtifact(absRoot, relPath string) ([]byte, error) {
	cleanRelPath, ok := cleanMetricsArtifactPath(relPath)
	if !ok {
		return nil, metricsArtifactError("read", relPath, fmt.Errorf("artifact path must be effort-relative and must not contain traversal"))
	}
	data, err := os.ReadFile(filepath.Join(absRoot, filepath.FromSlash(cleanRelPath)))
	if err != nil {
		return nil, metricsArtifactError("read", cleanRelPath, err)
	}
	return data, nil
}

func cleanMetricsArtifactPath(relPath string) (string, bool) {
	raw := strings.TrimSpace(relPath)
	normalized := strings.ReplaceAll(filepath.ToSlash(raw), "\\", "/")
	if normalized == "" || filepath.IsAbs(raw) || strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") {
		return "", false
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func metricsArtifactError(op, relPath string, err error) ProductWorkflowMetricsError {
	return ProductWorkflowMetricsError{
		Op:   op,
		Path: strings.ReplaceAll(filepath.ToSlash(relPath), "\\", "/"),
		Err:  err,
	}
}

func metricsErrorMessage(err error) string {
	switch {
	case os.IsNotExist(err):
		return "file not found"
	case os.IsPermission(err):
		return "permission denied"
	default:
		return strings.TrimSpace(err.Error())
	}
}

func collectReportMetrics(absRoot string) (map[string]int, map[string]int, error) {
	reportTypeCounts := map[string]int{}
	reportStatusCounts := map[string]int{}

	entries, err := os.ReadDir(filepath.Join(absRoot, "reports"))
	if err != nil {
		return nil, nil, metricsArtifactError("read", "reports/", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		relPath := filepath.Join("reports", name)
		data, readErr := readMetricsArtifact(absRoot, relPath)
		if readErr != nil {
			return nil, nil, readErr
		}
		reportType, known := ClassifyReportFileName(name)
		if !known {
			reportTypeCounts["other"]++
			continue
		}
		reportTypeCounts[reportType]++
		switch reportType {
		case "implementation":
			report, parseErr := ParseImplementationReportJSON(data)
			if parseErr != nil {
				return nil, nil, metricsArtifactError("parse", relPath, parseErr)
			}
			if strings.TrimSpace(report.CompletedAt) == "" {
				reportStatusCounts["implementation_pending"]++
			} else {
				reportStatusCounts["implementation_completed"]++
			}
		case "review":
			report, parseErr := ParseReviewReportJSON(data)
			if parseErr != nil {
				return nil, nil, metricsArtifactError("parse", relPath, parseErr)
			}
			status := strings.TrimSpace(report.Status)
			if !allowedMetricReviewStatuses[status] {
				return nil, nil, metricsArtifactError("validate", relPath, fmt.Errorf("review status has unsupported value %q", status))
			}
			reportStatusCounts["review_"+status]++
		case "investigation":
			report, parseErr := ParseInvestigationReportJSON(data)
			if parseErr != nil {
				return nil, nil, metricsArtifactError("parse", relPath, parseErr)
			}
			status := strings.TrimSpace(report.Status)
			if !allowedMetricInvestigationStatuses[status] {
				return nil, nil, metricsArtifactError("validate", relPath, fmt.Errorf("investigation status has unsupported value %q", status))
			}
			reportStatusCounts["investigation_"+status]++
		}
	}

	return reportTypeCounts, reportStatusCounts, nil
}

func countBoundedMetricValue(counts map[string]int, field, value string, allowed map[string]bool) error {
	trimmed := strings.TrimSpace(value)
	if !allowed[trimmed] {
		return fmt.Errorf("%s has unsupported value %q", field, trimmed)
	}
	counts[trimmed]++
	return nil
}

var allowedMetricGateStatuses = map[string]bool{
	"pass":    true,
	"passed":  true,
	"fail":    true,
	"failed":  true,
	"pending": true,
	"planned": true,
	"blocked": true,
	"error":   true,
}

var allowedMetricTDDStatuses = map[string]bool{
	"pending":  true,
	"planned":  true,
	"passed":   true,
	"pass":     true,
	"done":     true,
	"complete": true,
	"failed":   true,
	"blocked":  true,
}

var allowedMetricTDDTypes = map[string]bool{
	"acceptance":  true,
	"audit":       true,
	"contract":    true,
	"unit":        true,
	"integration": true,
	"property":    true,
	"mutation":    true,
	"eval":        true,
	"security":    true,
	"privacy":     true,
}

var allowedMetricLedgerRecordTypes = map[string]bool{
	"atom_closed":                                  true,
	"atom_planned":                                 true,
	"blocker_recorded":                             true,
	"fallback_completion":                          true,
	"fallback_exception":                           true,
	"implementation_completed":                     true,
	"implementation_guidelines_updated":            true,
	"implementor_error":                            true,
	"implementor_interrupted_for_guideline_update": true,
	"implementor_policy_updated":                   true,
	"implementor_timeout":                          true,
	"partial_implementation_recorded":              true,
	"post_effort_investigation_completed":          true,
	"review_completed":                             true,
	"verification_completed":                       true,
}

var allowedMetricReviewStatuses = map[string]bool{
	"pending":           true,
	"pass":              true,
	"passed":            true,
	"pass_after_repair": true,
	"fail":              true,
	"failed":            true,
	"blocked":           true,
	"complete":          true,
}

var allowedMetricInvestigationStatuses = map[string]bool{
	"planned":  true,
	"pending":  true,
	"complete": true,
	"blocked":  true,
	"pass":     true,
	"passed":   true,
	"fail":     true,
	"failed":   true,
}
