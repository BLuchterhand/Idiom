package implementationdispatch

import (
	"path/filepath"
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

func PlanDocumentsNeedRefineBeforeDispatch(planDocuments []PlanDocument) bool {
	for _, doc := range planDocuments {
		if IsAtomizedPlanDocumentPath(doc.Path) {
			return true
		}
	}

	return false
}

func TestObligationsFromDocuments(docs []PlanDocument) []string {
	var obligations []string

	for _, doc := range docs {
		obligations = appendUniqueStrings(obligations, doc.TestObligations...)
	}

	return obligations
}

func AuthorizedNewFiles(prompt string, planDocuments []PlanDocument) []string {
	paths := append([]string{}, plancontract.AuthorizedNewFilePathsFromPrompt(prompt)...)
	for _, doc := range planDocuments {
		paths = append(paths, doc.AuthorizedNewFiles...)
		paths = append(paths, plancontract.AuthorizedNewFilePathsFromPrompt(doc.Content)...)
	}

	return appendUniqueStrings(nil, paths...)
}

func PlanExecuteMaxTokens(prompt string, requiredPaths []string, allowedPaths []string, planDocuments []PlanDocument) int {
	if PlanExecuteNeedsLargeBodyBudget(prompt, requiredPaths, allowedPaths, planDocuments) {
		return 16384
	}

	return 8192
}

func PlanExecuteNeedsLargeBodyBudget(prompt string, requiredPaths []string, allowedPaths []string, planDocuments []PlanDocument) bool {
	if len(planDocuments) > 0 {
		for _, doc := range planDocuments {
			docText := strings.ToLower(doc.Content)
			if strings.Contains(docText, "new file") || strings.Contains(docText, "replace_all") || strings.Contains(docText, "full file body") {
				return true
			}

			for _, path := range doc.EditPaths {
				base := strings.ToLower(filepath.Base(filepath.ToSlash(path)))
				if strings.Contains(base, "new") {
					return true
				}
			}
		}
	}

	lower := strings.ToLower(prompt)
	for _, phrase := range []string{"create new file", "add new file", "write new file", "whole-file", "replace_all", "full file body"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	for _, path := range append(append([]string(nil), requiredPaths...), allowedPaths...) {
		lowerPath := strings.ToLower(filepath.ToSlash(path))
		if strings.Contains(lowerPath, "/new/") || strings.Contains(filepath.Base(lowerPath), "new_") {
			return true
		}

		if promptImpliesNewFilePath(lower, lowerPath) {
			return true
		}
	}

	return false
}

func promptImpliesNewFilePath(lowerPrompt string, lowerPath string) bool {
	if lowerPrompt == "" || lowerPath == "" || !strings.Contains(lowerPrompt, lowerPath) {
		return false
	}

	if strings.Contains(lowerPrompt, " create ") ||
		strings.HasPrefix(lowerPrompt, "create ") ||
		strings.Contains(lowerPrompt, " write ") ||
		strings.HasPrefix(lowerPrompt, "write ") {

		return true
	}

	if strings.Contains(lowerPrompt, " add "+lowerPath) || strings.Contains(lowerPrompt, " add new") || strings.Contains(lowerPrompt, " add file") {
		return true
	}

	return strings.HasPrefix(lowerPrompt, "add "+lowerPath) ||
		strings.HasPrefix(lowerPrompt, "add new ") ||
		strings.HasPrefix(lowerPrompt, "add file ")
}
