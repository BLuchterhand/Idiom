package productworkflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"idiom/internal/platform/gitstate"
)

// LintEffortFolder runs deterministic product-workflow lint checks for an effort folder.
func LintEffortFolder(effortRoot string) LintSummaryResult {
	absRoot := filepath.Clean(effortRoot)
	if resolvedRoot, err := filepath.Abs(effortRoot); err == nil {
		absRoot = resolvedRoot
	}
	batch, err := openProductWorkflowMutationLease(absRoot)
	if err != nil {
		return LintSummaryResult{
			Status:          ProductWorkflowStatusFail,
			EffortRoot:      effortRoot,
			CheckedFiles:    []string{},
			Findings:        []LintSummaryFinding{{Code: "adr_batch_recovery_error", Path: "adrs/index.json", Message: err.Error()}},
			NextValidAction: "recover the ADR batch transaction, then rerun `idiom product lint --effort-root <path>`",
		}
	}
	defer batch.Close()

	return lintEffortFolderRootObserved(absRoot, batch.root, nil)
}

func lintEffortFolderRootObserved(
	effortRoot string,
	root *os.Root,
	recorder *workflowCompletionInputRecorder,
) LintSummaryResult {
	if recorder == nil {
		recorder = newWorkflowCompletionInputRecorder()
	}
	result := LintSummaryResult{
		EffortRoot:   effortRoot,
		CheckedFiles: []string{},
		Findings:     []LintSummaryFinding{},
	}
	absRoot := filepath.Clean(effortRoot)
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
		result.CheckedFiles = append(result.CheckedFiles, relPath)

		cleanRelPath, ok := cleanEffortRelativePath(relPath)
		if !ok {
			addFinding("invalid_path", relPath, "required file path must stay beneath the effort root")

			return
		}

		data, err := recorder.ReadFile(root, cleanRelPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
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
	entries, err := readEffortArtifactDirectoryRoot(root, "reports")
	recorder.ObserveExternal(lintDirectoryCompletionInput(root, "reports", "reports-json-regular", entries, err))
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
			info, statErr := root.Lstat(relPath)
			if statErr != nil {
				addFinding("read_error", relPath, statErr.Error())
				continue
			}

			result.CheckedFiles = append(result.CheckedFiles, relPath)

			if info.Mode()&os.ModeSymlink != 0 {
				addFinding("symlink_file", relPath, "report file must not be a symlink")

				continue
			}
			if !info.Mode().IsRegular() {
				continue
			}
			reportPaths[relPath] = true

			data, readErr := recorder.ReadFile(root, relPath)
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

			report, err := parseKnownReportJSON(reportType, data)
			if err != nil {
				addFinding("parse_error", relPath, err.Error())
			} else if investigation, ok := report.(InvestigationReport); ok {
				parsedInvestigations = append(parsedInvestigations, investigation)
			}
		}
	}

	briefPaths := map[string]bool{}
	briefEntries, briefErr := readEffortArtifactDirectoryRoot(root, "briefs")
	recorder.ObserveExternal(lintDirectoryCompletionInput(root, "briefs", "briefs-nondirectory", briefEntries, briefErr))
	if briefErr == nil {
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
		repoRoot, ok := lintGitMetadataRoot(effortRoot, recorder)
		if !ok {
			repoRoot = effortRoot
		}

		if parsedWorkflowState != nil {
			parity := EvaluateObsidianParity(repoRoot, parsedWorkflowState, parsedLedger, parsedInvestigations)
			for _, input := range parity.completionInputs {
				recorder.ObserveExternal(input)
			}

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
			case LedgerRecordAtomPlanned:
				if rec.Scope != nil {
					plannedScopes[rec.TaskID] = rec.Scope
				}
			case LedgerRecordAtomClosed:
				closedTasks[rec.TaskID] = true
			}
		}

		taskPaths := map[string][]string{}

		for _, rec := range parsedLedger {
			if !IsImplementationCompletionRecordType(rec.RecordType) {
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
		if IsImplementationChangeRecordType(rec.RecordType) {
			atoms = append(atoms, AtomCoverageCheck{
				TaskID:       rec.TaskID,
				ChangedPaths: rec.ChangedPaths,
			})
		}
	}

	if len(atoms) > 0 {
		if repoRoot, ok := lintGitMetadataRoot(effortRoot, recorder); ok {
			if rel, err := filepath.Rel(repoRoot, absRoot); err == nil {
				gitPaths, err := lintGitChangedPaths(repoRoot, rel, recorder)
				if err != nil {
					gitPaths = nil
				}
				if len(gitPaths) > 0 {
					coverageFindings := ValidateLedgerCoverage(atoms, gitPaths, rel)

					for _, f := range coverageFindings {
						addFinding(f.Code, "implementation_ledger.jsonl", f.Message)
					}
				}
			}
		}
	}

	{
		commitHashRequiredAfter := "2026-05-17T23:00:00Z"

		var commitAtoms []AtomCommitCoverage

		commitHashSet := map[string]bool{}

		for _, rec := range parsedLedger {
			if IsImplementationChangeRecordType(rec.RecordType) {
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
			commitPaths := map[string][]string{}

			if repoRoot, ok := lintGitMetadataRoot(effortRoot, recorder); ok {
				effortRel, relErr := filepath.Rel(repoRoot, absRoot)
				if relErr != nil {
					effortRel = ""
				}
				hashes := make([]string, 0, len(commitHashSet))

				for h := range commitHashSet {
					hashes = append(hashes, h)
				}

				sort.Strings(hashes)

				prefix := effortRel + "/"

				for _, hash := range hashes {
					paths, err := lintGitCommitChangedPaths(repoRoot, hash, recorder)
					if err != nil {
						continue
					}

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

	wsData, wsErr := recorder.ReadFile(root, WorkflowStateFileName)
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

	result.NextValidAction = "fix findings, then rerun `idiom product lint --effort-root <path>`"
	if result.Status == ProductWorkflowStatusPass {
		result.NextValidAction = "run `idiom product audit --effort-root <path>` for section-level checks"
	}
	return result
}

func lintDirectoryCompletionInput(
	root *os.Root,
	relPath string,
	kind string,
	entries []os.DirEntry,
	readErr error,
) workflowCompletionExternalInput {
	value := lintDirectoryCompletionValue(root, relPath, kind, entries, readErr)

	return workflowCompletionExternalInput{
		key:   "effort-directory-membership:" + filepath.ToSlash(relPath) + "\x00" + kind,
		value: value,
		inspect: func() string {
			current, err := readEffortArtifactDirectoryRoot(root, relPath)
			return lintDirectoryCompletionValue(root, relPath, kind, current, err)
		},
	}
}

func lintDirectoryCompletionValue(
	root *os.Root,
	relPath string,
	kind string,
	entries []os.DirEntry,
	readErr error,
) string {
	if errors.Is(readErr, os.ErrNotExist) {
		return "missing"
	}
	if readErr != nil {
		return "error"
	}
	var names []string
	for _, entry := range entries {
		switch kind {
		case "reports-json-regular":
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			info, err := root.Lstat(filepath.Join(relPath, entry.Name()))
			if err != nil {
				return "error"
			}
			if info.Mode().IsRegular() {
				names = append(names, entry.Name())
			}
		case "briefs-nondirectory":
			if !entry.IsDir() {
				names = append(names, entry.Name())
			}
		}
	}
	sort.Strings(names)

	return workflowCompletionStringSliceValue(names, nil)
}

func lintGitMetadataRoot(effortRoot string, recorder *workflowCompletionInputRecorder) (string, bool) {
	repoRoot, ok := gitstate.MetadataRoot(effortRoot)
	value := fmt.Sprintf("%t:%s", ok, filepath.Clean(repoRoot))
	recorder.ObserveExternal(workflowCompletionExternalInput{
		key:   "git-metadata-root:" + filepath.Clean(effortRoot),
		value: value,
		inspect: func() string {
			currentRoot, currentOK := gitstate.MetadataRoot(effortRoot)
			return fmt.Sprintf("%t:%s", currentOK, filepath.Clean(currentRoot))
		},
	})

	return repoRoot, ok
}

func lintGitChangedPaths(repoRoot, relPath string, recorder *workflowCompletionInputRecorder) ([]string, error) {
	paths, err := gitstate.ChangedPathsSinceFirstPathCommit(context.Background(), repoRoot, relPath)
	recorder.ObserveExternal(workflowCompletionExternalInput{
		key:   "git-changed-paths:" + filepath.Clean(repoRoot) + "\x00" + filepath.ToSlash(relPath),
		value: workflowCompletionStringSliceValue(paths, err),
		inspect: func() string {
			current, currentErr := gitstate.ChangedPathsSinceFirstPathCommit(context.Background(), repoRoot, relPath)
			return workflowCompletionStringSliceValue(current, currentErr)
		},
	})

	return paths, err
}

func lintGitCommitChangedPaths(repoRoot, hash string, recorder *workflowCompletionInputRecorder) ([]string, error) {
	paths, err := gitstate.CommitChangedPaths(context.Background(), repoRoot, hash)
	recorder.ObserveExternal(workflowCompletionExternalInput{
		key:   "git-commit-paths:" + filepath.Clean(repoRoot) + "\x00" + hash,
		value: workflowCompletionStringSliceValue(paths, err),
		inspect: func() string {
			current, currentErr := gitstate.CommitChangedPaths(context.Background(), repoRoot, hash)
			return workflowCompletionStringSliceValue(current, currentErr)
		},
	})

	return paths, err
}

func knownTDDIDSet(ledger *TDDLedger, records []ImplementationLedgerRecord) map[string]bool {
	if ledger == nil {
		return BuildKnownTDDObligationIDs(TDDLedger{}, records)
	}
	return BuildKnownTDDObligationIDs(*ledger, records)
}
