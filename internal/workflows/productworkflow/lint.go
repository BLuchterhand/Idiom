package productworkflow

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// LintEffortFolder runs deterministic product-workflow lint checks for an effort folder.
func LintEffortFolder(root string) LintSummaryResult {
	result := LintSummaryResult{
		EffortRoot:   root,
		CheckedFiles: []string{},
		Findings:     []LintSummaryFinding{},
	}
	addFinding := func(code, relPath, message string) {
		result.Findings = append(result.Findings, LintSummaryFinding{
			Code:    code,
			Path:    relPath,
			Message: message,
		})
	}

	var (
		parsedCards          []EvidenceCard
		parsedMatrix         *TraceabilityMatrix
		parsedPRD            *PRDDocument
		parsedADRIndex       *ADRIndex
		parsedTDD            *TDDLedger
		parsedWorkflowState  *WorkflowState
		parsedLedger         []ImplementationLedgerRecord
		parsedADRs           []ParsedADR
		parsedInvestigations []InvestigationReport
	)

	checkFile := func(relPath string, validate func([]byte) error) {
		fullPath := filepath.Join(root, relPath)

		result.CheckedFiles = append(result.CheckedFiles, relPath)

		if info, statErr := os.Lstat(fullPath); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			addFinding("symlink_file", relPath, "required file must not be a symlink")

			return
		} else if statErr != nil && !os.IsNotExist(statErr) {
			addFinding("read_error", relPath, statErr.Error())

			return
		}

		data, err := os.ReadFile(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				addFinding("missing_file", relPath, "required file not found")
			} else {
				addFinding("read_error", relPath, err.Error())
			}

			return
		}

		if err := validate(data); err != nil {
			addFinding("parse_error", relPath, err.Error())
		}
	}

	checkFile(WorkflowStateFileName, func(data []byte) error {
		ws, err := ParseWorkflowStateJSON(data)
		if err == nil {
			parsedWorkflowState = &ws
		}

		return err
	})
	checkFile("tdd.json", func(data []byte) error {
		l, err := ParseTDDLedgerJSON(data)
		if err == nil {
			parsedTDD = &l
		}

		return err
	})
	checkFile("evidence/evidence.jsonl", func(data []byte) error {
		c, err := ParseEvidenceCardsJSONL(data)
		if err == nil {
			parsedCards = c
		}

		return err
	})
	checkFile("evidence/traceability_matrix.json", func(data []byte) error {
		m, err := ParseTraceabilityMatrixJSON(data)
		if err == nil {
			parsedMatrix = &m
		}

		return err
	})
	checkFile("implementation_ledger.jsonl", func(data []byte) error {
		recs, err := ParseImplementationLedgerJSONL(data)
		if err == nil {
			parsedLedger = recs
		}

		return err
	})
	checkFile("prd.md", func(data []byte) error {
		d, err := ParsePRDMarkdown(data)
		if err == nil {
			parsedPRD = &d
		}

		return err
	})
	checkFile("adrs/index.json", func(data []byte) error {
		idx, err := ParseADRIndexJSON(data)
		if err != nil {
			return err
		}

		parsedADRIndex = &idx
		for _, entry := range idx.ADRs {
			adrRelPath := filepath.Join("adrs", entry.Path)
			checkFile(adrRelPath, func(adrData []byte) error {
				adrDoc, adrErr := ParseADRMarkdown(adrData)
				if adrErr != nil {
					return adrErr
				}

				parsedADRs = append(parsedADRs, ParsedADR{
					Doc:         adrDoc,
					RelPath:     adrRelPath,
					RawMarkdown: string(adrData),
				})

				return nil
			})
		}

		return nil
	})

	reportPaths := map[string]bool{}
	reportsDir := filepath.Join(root, "reports")

	entries, err := os.ReadDir(reportsDir)
	if err != nil {
		if os.IsNotExist(err) {
			addFinding("missing_dir", "reports/", "required directory not found")
		} else {
			addFinding("read_error", "reports/", err.Error())
		}
	} else {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}

			relPath := filepath.Join("reports", entry.Name())

			result.CheckedFiles = append(result.CheckedFiles, relPath)
			reportPaths[relPath] = true

			if entry.Type()&os.ModeSymlink != 0 {
				addFinding("symlink_file", relPath, "report file must not be a symlink")

				continue
			}

			data, readErr := os.ReadFile(filepath.Join(reportsDir, entry.Name()))
			if readErr != nil {
				addFinding("read_error", relPath, readErr.Error())

				continue
			}

			reportType, known := ClassifyReportFileName(entry.Name())
			if !known {
				var raw json.RawMessage

				if err := json.Unmarshal(data, &raw); err != nil {
					addFinding("parse_error", relPath, fmt.Sprintf("invalid JSON: %v", err))
				}

				continue
			}

			switch reportType {
			case "implementation":
				if _, err := ParseImplementationReportJSON(data); err != nil {
					addFinding("parse_error", relPath, err.Error())
				}
			case "review":
				if _, err := ParseReviewReportJSON(data); err != nil {
					addFinding("parse_error", relPath, err.Error())
				}
			case "investigation":
				report, err := ParseInvestigationReportJSON(data)
				if err != nil {
					addFinding("parse_error", relPath, err.Error())
				} else {
					parsedInvestigations = append(parsedInvestigations, report)
				}
			}
		}
	}

	briefPaths := map[string]bool{}
	briefsDir := filepath.Join(root, "briefs")

	if briefEntries, briefErr := os.ReadDir(briefsDir); briefErr == nil {
		for _, be := range briefEntries {
			if !be.IsDir() {
				briefPaths[filepath.Join("briefs", be.Name())] = true
			}
		}
	}

	if parsedMatrix != nil {
		var evidenceIDs map[string]bool

		if parsedCards != nil {
			evidenceIDs = EvidenceCardIDSet(parsedCards)
		}

		var prdReqIDs map[string]bool

		if parsedPRD != nil {
			prdReqIDs = PRDRequirementIDSet(parsedPRD.Requirements)
		}

		var adrIDSet map[string]bool

		if parsedADRIndex != nil {
			adrIDSet = ADRIndexIDSet(*parsedADRIndex)
		}

		tddIDSet := knownTDDIDSet(parsedTDD, parsedLedger)
		linkErrs := ValidateTraceabilityCrossLinks(
			*parsedMatrix, evidenceIDs, prdReqIDs, adrIDSet, tddIDSet, briefPaths, reportPaths,
		)
		for _, le := range linkErrs {
			addFinding("broken_link", "evidence/traceability_matrix.json", le.Message)
		}
	}

	if parsedPRD != nil && parsedCards != nil {
		evidenceIDs := EvidenceCardIDSet(parsedCards)

		for _, req := range parsedPRD.Requirements {
			for _, ref := range req.EvidenceRefs {
				if !evidenceIDs[ref] {
					addFinding("broken_link", "prd.md", fmt.Sprintf(
						"PRD requirement %s references evidence card %q not found in evidence ledger", req.ID, ref,
					))
				}
			}
		}
	}

	if len(parsedADRs) > 0 {
		var prdReqIDs map[string]bool

		if parsedPRD != nil {
			prdReqIDs = PRDRequirementIDSet(parsedPRD.Requirements)
		}

		var evidenceIDs map[string]bool

		if parsedCards != nil {
			evidenceIDs = EvidenceCardIDSet(parsedCards)
		}

		tddIDSet := knownTDDIDSet(parsedTDD, parsedLedger)

		adrLinkErrs := ValidateADRCrossLinks(parsedADRs, prdReqIDs, evidenceIDs, tddIDSet)
		for _, le := range adrLinkErrs {
			addFinding("broken_link", le.ADRPath, le.Message)
		}

		supersessionErrs := ValidateADRSupersessionLinks(parsedADRs)
		for _, se := range supersessionErrs {
			addFinding(se.Code, se.ADRPath, se.Message)
		}

		if parsedADRIndex != nil {
			consistencyErrs := ValidateADRIndexFrontMatterConsistency(*parsedADRIndex, parsedADRs)
			for _, ce := range consistencyErrs {
				addFinding(ce.Code, ce.Path, ce.Message)
			}
		}
	}

	var prdForClaimLint PRDDocument

	if parsedPRD != nil {
		prdForClaimLint = *parsedPRD
	}

	for _, finding := range ImplementationAffectingClaimFindings(prdForClaimLint, parsedADRs) {
		addFinding(finding.Kind, finding.File, finding.Message)
	}

	var evidenceIDs map[string]bool

	if parsedCards != nil {
		evidenceIDs = EvidenceCardIDSet(parsedCards)
	}

	for _, finding := range ImplementationAffectingClaimEvidenceFindings(prdForClaimLint, parsedADRs, evidenceIDs) {
		addFinding(finding.Kind, finding.File, finding.Message)
	}

	if parsedPRD != nil {
		prdTDDErrs := ValidatePRDTDDObligationCrossLinks(parsedPRD.Requirements, knownTDDIDSet(parsedTDD, parsedLedger))
		for _, le := range prdTDDErrs {
			addFinding("broken_link", "tdd.json", le.Message)
		}
	}

	if parsedTDD != nil && len(parsedTDD.Obligations) > 0 {
		satisfiedTDDIDs := BuildSatisfiedTDDObligationIDs(*parsedTDD, parsedLedger)
		if len(satisfiedTDDIDs) == 0 {
			addFinding(
				"coverage_tdd_evidence_missing",
				"tdd.json",
				fmt.Sprintf("expected %d TDD obligations to have verification evidence", len(parsedTDD.Obligations)),
			)
		}
	}

	{
		repoRoot := findGitRepoRoot(root)
		if repoRoot == "" {
			repoRoot = root
		}

		if parsedWorkflowState != nil {
			parity := EvaluateObsidianParity(repoRoot, parsedWorkflowState, parsedLedger, parsedInvestigations)

			result.CheckedFiles = append(result.CheckedFiles, parity.CheckedFiles...)

			for _, finding := range parity.Findings {
				addFinding(finding.Code, finding.Path, finding.Message)
			}
		}
	}

	{
		plannedScopes := map[string]*AtomScope{}
		closedTasks := map[string]bool{}

		for _, rec := range parsedLedger {
			switch rec.RecordType {
			case "atom_planned":
				if rec.Scope != nil {
					plannedScopes[rec.TaskID] = rec.Scope
				}
			case "atom_closed":
				closedTasks[rec.TaskID] = true
			}
		}

		taskPaths := map[string][]string{}

		for _, rec := range parsedLedger {
			if rec.RecordType != "implementation_completed" && rec.RecordType != "fallback_completion" {
				continue
			}

			if closedTasks[rec.TaskID] {
				continue
			}

			taskPaths[rec.TaskID] = append(taskPaths[rec.TaskID], rec.ChangedPaths...)
		}

		sortedTIDs := make([]string, 0, len(taskPaths))
		for tid := range taskPaths {
			sortedTIDs = append(sortedTIDs, tid)
		}

		sort.Strings(sortedTIDs)

		for _, tid := range sortedTIDs {
			paths := taskPaths[tid]
			scope, ok := plannedScopes[tid]

			if !ok || scope == nil || len(paths) == 0 {
				continue
			}

			gateResult := EvaluateScopeGate(ScopeGateInput{
				ExpectedFiles:      scope.ExpectedFiles,
				AllowedFiles:       scope.AllowedFiles,
				ForbiddenFiles:     scope.Forbidden,
				ForbiddenPatterns:  scope.ForbiddenPatterns,
				ActualChangedPaths: paths,
			})
			for _, b := range gateResult.Blockers {
				msg := fmt.Sprintf("task %s scope_gate_%s: %s", tid, b.Code, b.Message)
				addFinding("scope_gate_"+b.Code, "implementation_ledger.jsonl", msg)
			}
		}
	}

	var atoms []AtomCoverageCheck

	for _, rec := range parsedLedger {
		if rec.RecordType == "implementation_completed" || rec.RecordType == "fallback_completion" || rec.RecordType == "partial_implementation_recorded" {
			atoms = append(atoms, AtomCoverageCheck{
				TaskID:       rec.TaskID,
				ChangedPaths: rec.ChangedPaths,
			})
		}
	}

	if len(atoms) > 0 {
		gitPaths := gitChangedPaths(root)
		if len(gitPaths) > 0 {
			rel, _ := filepath.Rel(findGitRepoRoot(root), root)
			coverageFindings := ValidateLedgerCoverage(atoms, gitPaths, rel)

			for _, f := range coverageFindings {
				addFinding(f.Code, "implementation_ledger.jsonl", f.Message)
			}
		}
	}

	{
		commitHashRequiredAfter := "2026-05-17T23:00:00Z"

		var commitAtoms []AtomCommitCoverage

		commitHashSet := map[string]bool{}

		for _, rec := range parsedLedger {
			if rec.RecordType == "implementation_completed" || rec.RecordType == "fallback_completion" || rec.RecordType == "partial_implementation_recorded" {
				if rec.Timestamp >= commitHashRequiredAfter {
					commitAtoms = append(commitAtoms, AtomCommitCoverage{
						TaskID:       rec.TaskID,
						CommitHash:   rec.CommitHash,
						ChangedPaths: rec.ChangedPaths,
					})
				}

				if rec.CommitHash != "" {
					commitHashSet[rec.CommitHash] = true
				}
			}
		}

		if len(commitAtoms) > 0 {
			repoRoot := findGitRepoRoot(root)
			commitPaths := map[string][]string{}

			if repoRoot != "" {
				effortRel, _ := filepath.Rel(repoRoot, root)
				hashes := make([]string, 0, len(commitHashSet))

				for h := range commitHashSet {
					hashes = append(hashes, h)
				}

				sort.Strings(hashes)

				rawCommitPaths := gitCommitPaths(repoRoot, hashes)
				prefix := effortRel + "/"

				for hash, paths := range rawCommitPaths {
					var stripped []string

					for _, p := range paths {
						if effortRel != "" && strings.HasPrefix(p, prefix) {
							stripped = append(stripped, strings.TrimPrefix(p, prefix))
						} else if effortRel != "" && p == effortRel {
							continue
						} else {
							stripped = append(stripped, p)
						}
					}

					commitPaths[hash] = stripped
				}

				for i := range commitAtoms {
					var norm []string

					for _, p := range commitAtoms[i].ChangedPaths {
						if effortRel != "" && strings.HasPrefix(p, prefix) {
							norm = append(norm, strings.TrimPrefix(p, prefix))
						} else {
							norm = append(norm, p)
						}
					}

					commitAtoms[i].ChangedPaths = norm
				}
			}

			perAtomFindings := ValidatePerAtomCoverage(commitAtoms, commitPaths)
			for _, f := range perAtomFindings {
				addFinding(f.Code, "implementation_ledger.jsonl", f.Message)
			}
		}
	}

	if len(result.Findings) == 0 {
		result.Status = ProductWorkflowStatusPass
	} else {
		result.Status = ProductWorkflowStatusFail
	}

	wsData, wsErr := os.ReadFile(filepath.Join(root, WorkflowStateFileName))
	if wsErr == nil {
		var ws struct {
			ArtifactPaths map[string]string `json:"artifact_paths"`
		}

		if json.Unmarshal(wsData, &ws) == nil {
			if rp, ok := ws.ArtifactPaths["research_profile"]; ok && rp != "" {
				checkFile(rp, func(data []byte) error {
					_, err := ParseResearchProfileJSON(data)
					return err
				})
			}
		}

		if len(result.Findings) == 0 {
			result.Status = ProductWorkflowStatusPass
		} else {
			result.Status = ProductWorkflowStatusFail
		}
	}

	return result
}

func knownTDDIDSet(ledger *TDDLedger, records []ImplementationLedgerRecord) map[string]bool {
	if ledger == nil {
		return BuildKnownTDDObligationIDs(TDDLedger{}, records)
	}
	return BuildKnownTDDObligationIDs(*ledger, records)
}

func gitChangedPaths(effortRoot string) []string {
	repoRoot := findGitRepoRoot(effortRoot)
	if repoRoot == "" {
		return nil
	}

	rel, err := filepath.Rel(repoRoot, effortRoot)
	if err != nil {
		return nil
	}

	out, err := exec.Command("git", "-C", repoRoot, "log", "--reverse", "--format=%H", "--", rel).Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return nil
	}

	firstCommit := lines[0]

	diffOut, err := exec.Command("git", "-C", repoRoot, "diff", "--name-only", firstCommit+"^", "HEAD").Output()
	if err != nil {
		diffOut, err = exec.Command("git", "-C", repoRoot, "diff", "--name-only", firstCommit, "HEAD").Output()
		if err != nil {
			return nil
		}
	}

	var paths []string

	for _, line := range strings.Split(strings.TrimSpace(string(diffOut)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}

	return paths
}

func gitCommitPaths(repoRoot string, hashes []string) map[string][]string {
	result := make(map[string][]string)

	for _, h := range hashes {
		out, err := exec.Command("git", "-C", repoRoot, "show", "--name-only", "--format=", h).Output()
		if err != nil {
			continue
		}

		var paths []string

		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				paths = append(paths, line)
			}
		}

		result[h] = paths
	}

	return result
}

func findGitRepoRoot(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}

		dir = parent
	}
}
