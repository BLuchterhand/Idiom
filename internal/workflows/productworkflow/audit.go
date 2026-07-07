package productworkflow

import (
	"errors"
	"strings"
)

type ProductAuditSection struct {
	Name     string                `json:"name"`
	Status   ProductWorkflowStatus `json:"status"`
	Findings []LintSummaryFinding  `json:"findings"`
}

type ProductAuditResult struct {
	Status          ProductWorkflowStatus   `json:"status"`
	EffortRoot      string                  `json:"effort_root"`
	CheckedFiles    []string                `json:"checked_files"`
	Sections        []ProductAuditSection   `json:"sections"`
	Findings        []LintSummaryFinding    `json:"findings"`
	Metrics         *ProductWorkflowMetrics `json:"metrics,omitempty"`
	NextValidAction string                  `json:"next_valid_action"`
}

var productAuditSectionOrder = []string{"prd", "adr", "tdd", "traceability", "reports", "obsidian", "scope_gate", "metrics"}
var productStatusSectionOrder = []string{"prd", "adr", "tdd", "traceability", "reports", "obsidian", "metrics"}

func ProductWorkflowSectionForFinding(f LintSummaryFinding) string {
	if section := ClassifySection(f.Path); section != "" {
		return section
	}

	code := strings.TrimSpace(f.Code)
	switch {
	case strings.HasPrefix(code, "scope_gate_"):
		return "scope_gate"
	case strings.HasPrefix(code, "obsidian_"):
		return "obsidian"
	case strings.HasPrefix(code, "coverage_"):
		return "tdd"
	default:
		return ""
	}
}

func BuildProductAuditResult(lintResult LintSummaryResult) ProductAuditResult {
	sections := newProductAuditSections(productAuditSectionOrder)

	for _, finding := range lintResult.Findings {
		markProductAuditFinding(sections, finding)
	}

	status := ProductWorkflowStatusPass
	if lintResult.Status == ProductWorkflowStatusFail {
		status = ProductWorkflowStatusFail
	}

	return ProductAuditResult{
		Status:       status,
		EffortRoot:   lintResult.EffortRoot,
		CheckedFiles: append([]string(nil), lintResult.CheckedFiles...),
		Sections:     orderedProductAuditSections(sections, productAuditSectionOrder),
		Findings:     append([]LintSummaryFinding(nil), lintResult.Findings...),
	}
}

func BuildCheckedProductAudit(absRoot string) ProductAuditResult {
	lintResult := LintEffortFolder(absRoot)
	result := BuildProductAuditResult(lintResult)

	metrics, metricsErr := BuildProductWorkflowMetrics(absRoot)
	if metricsErr != nil {
		finding := productWorkflowMetricsFinding(metricsErr)

		result.Status = ProductWorkflowStatusFail
		result.Findings = append(result.Findings, finding)
		markProductAuditSectionFailure(&result, "metrics", finding)
	} else {
		result.Metrics = &metrics
	}

	result.NextValidAction = ProductAuditNextValidAction(result.Status)
	return result
}

func ProductAuditNextValidAction(status ProductWorkflowStatus) string {
	if status == ProductWorkflowStatusPass {
		return "run `idiom product status --effort-root <path>` to inspect workflow state"
	}

	return "fix findings, then rerun `idiom product audit --effort-root <path>`"
}

func BuildProductStatusSections(lintResult LintSummaryResult) []StatusSection {
	sections := newStatusSections(productStatusSectionOrder)

	for _, finding := range lintResult.Findings {
		section := ProductWorkflowSectionForFinding(finding)
		if section == "" {
			continue
		}

		if s, ok := sections[section]; ok {
			s.Status = ProductWorkflowStatusFail
			s.FindingCount++
		}
	}

	return orderedStatusSections(sections, productStatusSectionOrder)
}

func newProductAuditSections(names []string) map[string]*ProductAuditSection {
	sections := make(map[string]*ProductAuditSection, len(names))

	for _, name := range names {
		sections[name] = &ProductAuditSection{Name: name, Status: ProductWorkflowStatusPass}
	}

	return sections
}

func markProductAuditFinding(sections map[string]*ProductAuditSection, finding LintSummaryFinding) {
	section := ProductWorkflowSectionForFinding(finding)
	if section == "" {
		return
	}

	if s, ok := sections[section]; ok {
		s.Status = ProductWorkflowStatusFail
		s.Findings = append(s.Findings, finding)
	}
}

func orderedProductAuditSections(sections map[string]*ProductAuditSection, order []string) []ProductAuditSection {
	out := make([]ProductAuditSection, 0, len(order))

	for _, name := range order {
		if section, ok := sections[name]; ok {
			out = append(out, *section)
		}
	}

	return out
}

func newStatusSections(names []string) map[string]*StatusSection {
	sections := make(map[string]*StatusSection, len(names))

	for _, name := range names {
		sections[name] = &StatusSection{Name: name, Status: ProductWorkflowStatusPass}
	}

	return sections
}

func orderedStatusSections(sections map[string]*StatusSection, order []string) []StatusSection {
	out := make([]StatusSection, 0, len(order))

	for _, name := range order {
		if section, ok := sections[name]; ok {
			out = append(out, *section)
		}
	}

	return out
}

func productWorkflowMetricsFinding(err error) LintSummaryFinding {
	path := WorkflowStateFileName

	var metricsErr ProductWorkflowMetricsError
	if errors.As(err, &metricsErr) && strings.TrimSpace(metricsErr.Path) != "" {
		path = metricsErr.Path
	}

	return LintSummaryFinding{
		Code:    "metrics_error",
		Path:    path,
		Message: err.Error(),
	}
}

func markProductAuditSectionFailure(result *ProductAuditResult, name string, finding LintSummaryFinding) {
	for i := range result.Sections {
		if result.Sections[i].Name == name {
			result.Sections[i].Status = ProductWorkflowStatusFail
			result.Sections[i].Findings = append(result.Sections[i].Findings, finding)
			return
		}
	}

	result.Sections = append(result.Sections, ProductAuditSection{
		Name:     name,
		Status:   ProductWorkflowStatusFail,
		Findings: []LintSummaryFinding{finding},
	})
}
