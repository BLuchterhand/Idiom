package productworkflow

import (
	"fmt"
	"os"
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
		gateStatusCounts[gate.Status]++
	}

	tddStatusCounts := map[string]int{}
	tddTypeCounts := map[string]int{}
	for _, obligation := range tdd.Obligations {
		tddStatusCounts[obligation.Status]++
		tddTypeCounts[obligation.Type]++
	}

	ledgerTypeCounts := map[string]int{}
	ledgerAtomCount := 0
	for _, record := range ledger {
		ledgerTypeCounts[record.RecordType]++
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
	data, err := os.ReadFile(filepath.Join(absRoot, relPath))
	if err != nil {
		return nil, metricsArtifactError("read", relPath, err)
	}
	return data, nil
}

func metricsArtifactError(op, relPath string, err error) ProductWorkflowMetricsError {
	return ProductWorkflowMetricsError{
		Op:   op,
		Path: filepath.ToSlash(relPath),
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
			reportStatusCounts["review_"+strings.TrimSpace(report.Status)]++
		case "investigation":
			report, parseErr := ParseInvestigationReportJSON(data)
			if parseErr != nil {
				return nil, nil, metricsArtifactError("parse", relPath, parseErr)
			}
			reportStatusCounts["investigation_"+strings.TrimSpace(report.Status)]++
		}
	}

	return reportTypeCounts, reportStatusCounts, nil
}
