package planexecute

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	plancontract "idiom/internal/contracts/plan"
	planartifact "idiom/internal/contracts/planartifact"
)

type EditPreflightPolicy struct {
	UnsupportedProofClaimReason                     func(planartifact.PlanEdit) string
	RejectExistingSourceWholeFileRewrite            func(string, []byte, planartifact.PlanEdit) error
	RejectExistingSourceDestructiveLineRangeRewrite func(string, string, planartifact.PlanEdit) error
	RejectSourceLineRangeTopLevelConstructDrift     func(string, string, planartifact.PlanEdit) error
	NeedsAnchorProvenance                           func(planartifact.PlanEdit) bool
	RequiresSubmitPlanAnchorProvenance              func(planartifact.PlanArtifact) bool
	ValidateAnchorProvenance                        func(string, planartifact.PlanEdit) error
	IsCompletedPlanningArtifact                     func(string, []byte) bool
	PreviewEditContent                              func(string, planartifact.PlanEdit) (string, bool, error)
	SuspiciousAdjacentDuplicateCodeLine             func(string, []byte, []byte) (int, string, bool)
}

func preflightPlanUnsupportedProofClaims(plan planartifact.PlanArtifact, policy EditPreflightPolicy) *PlanEditPreflightMismatch {
	policy = normalizeDependencies(Dependencies{EditPreflight: policy}).EditPreflight
	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		if reason := policy.UnsupportedProofClaimReason(edit); reason != "" {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: reason}
		}
	}

	return nil
}

func PreflightPlanUnsupportedProofClaimsWithPolicy(plan planartifact.PlanArtifact, policy EditPreflightPolicy) *PlanEditPreflightMismatch {
	return preflightPlanUnsupportedProofClaims(plan, policy)
}

func preflightPlanExistingSourceDestructiveRewrites(workspaceRoot string, plan planartifact.PlanArtifact, policy EditPreflightPolicy) *PlanEditPreflightMismatch {
	policy = normalizeDependencies(Dependencies{EditPreflight: policy}).EditPreflight
	cache := make(map[string][]byte)

	for i, edit := range plan.Edits {
		path := plancontract.NormalizeScopePath(edit.Path)
		if path == "" {
			continue
		}

		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(strings.TrimSpace(workspaceRoot), filepath.FromSlash(path))
		}

		body, ok := cache[abs]
		if !ok {
			content, err := os.ReadFile(abs)
			if err != nil {
				continue
			}

			body = content
			cache[abs] = body
		}

		if err := policy.RejectExistingSourceWholeFileRewrite(abs, body, edit); err != nil {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: err.Error()}
		}

		if err := policy.RejectExistingSourceDestructiveLineRangeRewrite(abs, string(body), edit); err != nil {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: err.Error()}
		}

		if selected, ok := planExecuteSelectedLineRange(string(body), edit); ok {
			if err := policy.RejectSourceLineRangeTopLevelConstructDrift(abs, selected, edit); err != nil {
				return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: err.Error()}
			}
		}
	}

	return nil
}

func planExecuteSelectedLineRange(current string, edit planartifact.PlanEdit) (string, bool) {
	if strings.TrimSpace(edit.Content) == "" || edit.StartLine <= 0 || edit.EndLine < edit.StartLine {
		return "", false
	}

	lines := strings.SplitAfter(current, "\n")
	if len(lines) == 0 || edit.StartLine > len(lines) || edit.EndLine > len(lines) {
		return "", false
	}

	return strings.Join(lines[edit.StartLine-1:edit.EndLine], ""), true
}

func preflightPlanEdits(workspaceRoot string, plan planartifact.PlanArtifact, policy EditPreflightPolicy) *PlanEditPreflightMismatch {
	policy = normalizeDependencies(Dependencies{EditPreflight: policy}).EditPreflight
	cache := make(map[string][]byte)

	for i, edit := range plan.Edits {
		oldText := strings.TrimSpace(edit.OldText)
		if !policy.NeedsAnchorProvenance(edit) {
			continue
		}

		rawPath := strings.TrimSpace(edit.Path)
		path := plancontract.NormalizeScopePath(rawPath)

		if path == "" {
			reason := "edit has no path"
			if rawPath != "" {
				reason = "edit has invalid path"
			}

			return &PlanEditPreflightMismatch{Index: i, Path: rawPath, Reason: reason}
		}

		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(strings.TrimSpace(workspaceRoot), filepath.FromSlash(path))
		}

		body, ok := cache[abs]
		if !ok {
			content, err := os.ReadFile(abs)
			if err != nil {
				return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: fmt.Sprintf("could not read file: %s", err.Error())}
			}

			body = content
			cache[abs] = body
		}

		current := string(body)

		if policy.RequiresSubmitPlanAnchorProvenance(plan) && edit.AnchorProvenance == nil {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "current-schema plan edit is missing submit_plan anchor provenance",
			}
		}

		if err := policy.ValidateAnchorProvenance(workspaceRoot, edit); err != nil {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: err.Error(),
			}
		}

		if oldText == "" {
			continue
		}

		matchCount := strings.Count(current, edit.OldText)
		if matchCount == 0 {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "old_text does not appear in file (whitespace/indentation mismatch is the most common cause)",
			}
		}

		if matchCount > 1 && !edit.ReplaceAll {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: fmt.Sprintf("old_text matched %d locations; use a line-range edit, extend old_text with surrounding context, or set replace_all=true when every occurrence is intended", matchCount),
			}
		}
	}

	if mismatch := preflightPlanFinalState(workspaceRoot, plan, policy); mismatch != nil {
		return mismatch
	}

	return nil
}

func PreflightPlanEditsWithPolicy(workspaceRoot string, plan planartifact.PlanArtifact, policy EditPreflightPolicy) *PlanEditPreflightMismatch {
	return preflightPlanEdits(workspaceRoot, plan, policy)
}

func preflightPlanFinalState(workspaceRoot string, plan planartifact.PlanArtifact, policy EditPreflightPolicy) *PlanEditPreflightMismatch {
	policy = normalizeDependencies(Dependencies{EditPreflight: policy}).EditPreflight
	cache := make(map[string][]byte)

	for i, edit := range plan.Edits {
		rawPath := strings.TrimSpace(edit.Path)
		path := plancontract.NormalizeScopePath(rawPath)

		if path == "" {
			reason := "edit has no path"
			if rawPath != "" {
				reason = "edit has invalid path"
			}

			return &PlanEditPreflightMismatch{Index: i, Path: rawPath, Reason: reason}
		}

		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(strings.TrimSpace(workspaceRoot), filepath.FromSlash(path))
		}

		body, ok := cache[abs]
		if !ok {
			content, err := os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) && edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" {
					content = nil
				} else {
					return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: fmt.Sprintf("could not read file: %s", err.Error())}
				}
			}

			body = content
			cache[abs] = body
		}

		if edit.ReplaceAll && strings.TrimSpace(edit.OldText) == "" && strings.TrimSpace(edit.NewText) != "" && policy.IsCompletedPlanningArtifact(path, body) {
			return &PlanEditPreflightMismatch{
				Index:  i,
				Path:   path,
				Reason: "whole-file edit would overwrite a completed planning artifact; use a bounded anchored edit or ask the user to reopen/replace it explicitly",
			}
		}

		updated, changed, err := policy.PreviewEditContent(string(body), edit)
		if err != nil {
			return &PlanEditPreflightMismatch{Index: i, Path: path, Reason: err.Error()}
		}

		if changed {
			if lineNo, line, ok := policy.SuspiciousAdjacentDuplicateCodeLine(path, body, []byte(updated)); ok {
				return &PlanEditPreflightMismatch{
					Index:  i,
					Path:   path,
					Reason: fmt.Sprintf("edit would introduce adjacent duplicate code line %d: %q", lineNo, line),
				}
			}
		}

		cache[abs] = []byte(updated)
	}

	return nil
}

func projectedInsertAfterLineContent(workspaceRoot string, edit planartifact.PlanEdit, policy EditPreflightPolicy) (string, error) {
	policy = normalizeDependencies(Dependencies{EditPreflight: policy}).EditPreflight
	path := plancontract.NormalizeScopePath(edit.Path)
	if path == "" {
		return "", fmt.Errorf("edit has invalid path")
	}

	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(strings.TrimSpace(workspaceRoot), filepath.FromSlash(path))
	}

	body, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("insert_after_line could not read existing %s: %w", path, err)
	}

	updated, changed, err := policy.PreviewEditContent(string(body), edit)
	if err != nil {
		return "", err
	}

	if !changed {
		return "", fmt.Errorf("insert_after_line would not change %s", path)
	}

	return updated, nil
}
