package plan

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// --- submit_plan coverage validation ---
const CoverageMismatchCode = "plan_then_execute_submit_plan_coverage_mismatch"

type SubmitPlanCoverageEdit struct {
	Path            string
	Purpose         string
	OldText         string
	NewText         string
	Content         string
	StartLine       int
	EndLine         int
	InsertAfterLine int
	ReplaceAll      bool
}

type SubmitPlanCoverageScope struct {
	Coverage               []string
	AllowedExact           []string
	DirectoryScopes        []string
	AllowedDirectoryScopes []string
	AllowedOnly            bool
}

func RequiredScope(required []string, allowed []string, isDir func(string) bool) SubmitPlanCoverageScope {
	normalizedRequired := normalizedUnique(required)
	if len(normalizedRequired) == 0 && len(normalizedUnique(allowed)) > 0 {
		return AllowedOnlyScope(allowed, isDir)
	}

	scope := requiredScopeFromPaths(required, isDir)
	for _, path := range normalizedUnique(allowed) {
		if requiredPathIsDirectoryScope(path, normalizedUnique(allowed), isDir) {
			scope.AllowedDirectoryScopes = appendUniqueNormalized(scope.AllowedDirectoryScopes, path)
			continue
		}
		scope.AllowedExact = appendUniqueNormalized(scope.AllowedExact, path)
	}
	return normalizeScope(scope)
}

func CoveragePaths(required []string, isDir func(string) bool) []string {
	return requiredScopeFromPaths(required, isDir).Coverage
}

func AllowedOnlyScope(allowed []string, isDir func(string) bool) SubmitPlanCoverageScope {
	scope := SubmitPlanCoverageScope{AllowedOnly: true}
	normalizedAllowed := normalizedUnique(allowed)
	for _, path := range normalizedAllowed {
		if requiredPathIsDirectoryScope(path, normalizedAllowed, isDir) {
			scope.DirectoryScopes = appendUniqueNormalized(scope.DirectoryScopes, path)
			continue
		}
		scope.AllowedExact = appendUniqueNormalized(scope.AllowedExact, path)
	}
	return normalizeScope(scope)
}

func requiredScopeFromPaths(required []string, isDir func(string) bool) SubmitPlanCoverageScope {
	scope := SubmitPlanCoverageScope{}
	normalizedRequired := normalizedUnique(required)
	for _, path := range normalizedRequired {
		scope.AllowedExact = appendUniqueNormalized(scope.AllowedExact, path)
		if requiredPathIsDirectoryScope(path, normalizedRequired, isDir) {
			scope.DirectoryScopes = appendUniqueNormalized(scope.DirectoryScopes, path)
			continue
		}
		scope.Coverage = appendUniqueNormalized(scope.Coverage, path)
	}
	return normalizeScope(scope)
}

func requiredPathIsDirectoryScope(path string, all []string, isDir func(string) bool) bool {
	path = normalizePath(path)
	if path == "" {
		return false
	}
	if isDir != nil && isDir(path) {
		return true
	}
	if looksLikeFilePath(path) {
		return false
	}
	prefix := strings.TrimSuffix(path, "/") + "/"
	for _, other := range all {
		if normalizePath(other) != path && strings.HasPrefix(normalizePath(other), prefix) {
			return true
		}
	}
	return false
}

func looksLikeFilePath(path string) bool {
	base := path
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	return strings.Contains(base, ".")
}

func (s SubmitPlanCoverageScope) Active() bool {
	return len(s.Coverage) > 0 ||
		len(s.AllowedExact) > 0 ||
		len(s.DirectoryScopes) > 0 ||
		len(s.AllowedDirectoryScopes) > 0
}

func (s SubmitPlanCoverageScope) Allows(path string) bool {
	path = normalizePath(path)
	if path == "" {
		return false
	}

	for _, required := range s.Coverage {
		if path == normalizePath(required) {
			return true
		}
	}

	for _, dir := range s.DirectoryScopes {
		if pathInDirectory(path, dir) {
			return true
		}
	}

	for _, dir := range s.AllowedDirectoryScopes {
		if pathInDirectory(path, dir) {
			return true
		}
	}

	for _, allowed := range s.AllowedExact {
		if path == normalizePath(allowed) {
			return true
		}
	}

	return false
}

type PathRoleCoverage struct {
	Path                       string   `json:"path,omitempty"`
	OriginalPath               string   `json:"original_path,omitempty"`
	Roles                      []string `json:"roles,omitempty"`
	EffectiveRole              string   `json:"effective_role,omitempty"`
	EffectiveInvariantID       string   `json:"effective_invariant_id,omitempty"`
	Decision                   string   `json:"decision,omitempty"`
	SatisfiesRequiredCoverage  bool     `json:"satisfies_required_coverage,omitempty"`
	WithinAllowedCandidatePath bool     `json:"within_allowed_candidate_path,omitempty"`
}

type CoverageInput struct {
	Scope                   SubmitPlanCoverageScope
	Edits                   []SubmitPlanCoverageEdit
	SourceSatisfiedPaths    []string
	MissingRequiredSnippets []string
	InvalidAnchors          []string
	PathRoleCoverage        []PathRoleCoverage
	MissingPathRoleCoverage []string
}

type CoverageDetails struct {
	Code                    string             `json:"code,omitempty"`
	MissingRequiredPaths    []string           `json:"missing_required_paths,omitempty"`
	MissingPathEdits        []string           `json:"missing_path_edits,omitempty"`
	OutsideRequiredPaths    []string           `json:"outside_required_paths,omitempty"`
	PathRoleCoverage        []PathRoleCoverage `json:"path_role_coverage,omitempty"`
	MissingRequiredSnippets []string           `json:"missing_required_snippets,omitempty"`
	InvalidAnchors          []string           `json:"invalid_anchors,omitempty"`
	PlaceholderEditBodies   []string           `json:"placeholder_edit_bodies,omitempty"`
}

type CoverageDecision struct {
	Rejected     bool
	Details      CoverageDetails
	Requirement  string
	ProblemParts []string
	Reason       string
}

func (d CoverageDecision) ErrorText() string {
	if !d.Rejected {
		return ""
	}
	return d.Requirement + "; " + strings.Join(d.ProblemParts, "; ") + ". Submit a corrected plan with `edits` as a JSON array, not a quoted string, and include `path` on every edit. Use insert_after_line/content only for small inline insertions/appends after an existing line; use old_text/new_text for exact replacements/deletions; use start_line/end_line/content to replace an exact inclusive current-source range, including large additions by replacing unchanged anchor line(s) with those line(s) plus inserted content. Edit bodies must be complete executable source text, not placeholders, ellipses, or prose."
}

func ValidateCoverage(input CoverageInput) CoverageDecision {
	scope := normalizeScope(input.Scope)
	scopeActive := scope.Active()
	planned := make(map[string]struct{}, len(input.Edits)+len(input.SourceSatisfiedPaths))

	var outside []string
	var missingPathEdits []string

	for _, path := range input.SourceSatisfiedPaths {
		if normalized := normalizePath(path); normalized != "" {
			planned[normalized] = struct{}{}
			if scopeActive && !scope.Allows(normalized) {
				outside = append(outside, normalized)
			}
		}
	}

	for i, edit := range input.Edits {
		path := normalizePath(edit.Path)
		if path == "" {
			missingPathEdits = append(missingPathEdits, EditName(i, edit))
			continue
		}

		planned[path] = struct{}{}
		if scopeActive && !scope.Allows(path) {
			outside = append(outside, path)
		}
	}

	var missing []string
	if scopeActive {
		missing = append(missing, normalizedUnique(input.MissingPathRoleCoverage)...)
		if len(input.MissingPathRoleCoverage) == 0 {
			for _, path := range scope.Coverage {
				if _, ok := planned[path]; !ok {
					missing = append(missing, path)
				}
			}
		}

		if !scope.AllowedOnly {
			for _, dir := range scope.DirectoryScopes {
				if !plannedCoversDirectory(planned, dir) {
					missing = append(missing, dir)
				}
			}
		} else if len(scope.Coverage) == 0 && len(scope.DirectoryScopes) > 0 && len(planned) == 0 {
			missing = append(missing, scope.DirectoryScopes...)
		}
	}

	missing = normalizedUnique(missing)
	outside = normalizedUnique(outside)
	missingPathEdits = uniqueStrings(missingPathEdits)
	missingSnippets := uniqueStrings(input.MissingRequiredSnippets)
	invalidAnchors := uniqueStrings(input.InvalidAnchors)
	placeholderBodies := PlaceholderEditBodies(input.Edits)

	if len(missing) == 0 &&
		len(missingPathEdits) == 0 &&
		len(outside) == 0 &&
		len(missingSnippets) == 0 &&
		len(invalidAnchors) == 0 &&
		len(placeholderBodies) == 0 {

		return CoverageDecision{}
	}

	details := CoverageDetails{
		Code:                    CoverageMismatchCode,
		MissingRequiredPaths:    append([]string(nil), missing...),
		MissingPathEdits:        append([]string(nil), missingPathEdits...),
		OutsideRequiredPaths:    append([]string(nil), outside...),
		PathRoleCoverage:        append([]PathRoleCoverage(nil), input.PathRoleCoverage...),
		MissingRequiredSnippets: append([]string(nil), missingSnippets...),
		InvalidAnchors:          append([]string(nil), invalidAnchors...),
		PlaceholderEditBodies:   append([]string(nil), placeholderBodies...),
	}

	scopeName := "required_edit_paths"
	outsideLabel := "path(s) outside required_edit_paths: "
	if scope.AllowedOnly {
		scopeName = "allowed_edit_paths"
		outsideLabel = "path(s) outside allowed_edit_paths: "
	}

	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "missing required edit path(s): "+quotedStrings(missing))
	}
	if len(missingPathEdits) > 0 {
		parts = append(parts, "edit entry missing `path`: "+quotedStrings(missingPathEdits))
	}
	if len(outside) > 0 {
		parts = append(parts, outsideLabel+quotedStrings(outside))
	}
	if len(missingSnippets) > 0 {
		parts = append(parts, "missing required edit snippet(s): "+quotedStrings(missingSnippets))
	}
	if len(invalidAnchors) > 0 {
		parts = append(parts, "invalid edit anchor(s): "+quotedStrings(invalidAnchors))
	}
	if len(placeholderBodies) > 0 {
		parts = append(parts, "placeholder edit body path(s): "+quotedStrings(placeholderBodies))
	}

	requirement := "submit_plan edit entries must include `path` and executable edit bodies with valid current-source anchors or line ranges"
	if scopeActive {
		requirement = "submit_plan must use edits[] for changed required paths and top-level source_satisfied_paths for already-final required paths, no edits outside " + scopeName + ", and coverage for every required_edit_snippets final-state snippet"
	}

	return CoverageDecision{
		Rejected:     true,
		Details:      details,
		Requirement:  requirement,
		ProblemParts: parts,
		Reason:       "plan-then-execute planner phase: submit_plan edits did not satisfy required path/snippet coverage",
	}
}

func CoverageDetailsJSON(details CoverageDetails) string {
	body, err := json.Marshal(details)
	if err != nil {
		return ""
	}
	return string(body)
}

func CoverageDetailsFromJSON(raw string) (CoverageDetails, bool) {
	var details CoverageDetails
	if strings.TrimSpace(raw) == "" {
		return details, false
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &details); err != nil {
		return details, false
	}
	return details, strings.TrimSpace(details.Code) == CoverageMismatchCode
}

func PlaceholderEditBodies(edits []SubmitPlanCoverageEdit) []string {
	var out []string
	for i, edit := range edits {
		path := normalizePath(edit.Path)
		if path == "" {
			path = EditName(i, edit)
		}
		if field, marker := PlanEditPlaceholderField(edit); field != "" {
			out = append(out, path+": "+field+" contains placeholder marker "+strconv.Quote(marker))
		}
	}
	return out
}

func PlanEditPlaceholderField(edit SubmitPlanCoverageEdit) (string, string) {
	fields := []struct {
		name  string
		value string
	}{
		{name: "old_text", value: edit.OldText},
		{name: "new_text", value: edit.NewText},
		{name: "content", value: edit.Content},
	}
	for _, field := range fields {
		if marker := PlanEditPlaceholderMarker(field.value); marker != "" {
			return field.name, marker
		}
	}
	return "", ""
}

func PlanEditPlaceholderMarker(value string) string {
	text := strings.ToLower(value)
	if strings.TrimSpace(text) == "" {
		return ""
	}

	for _, marker := range []string{
		"... existing code ...",
		"...existing code...",
		"existing code",
		"rest of file",
		"unchanged",
		"placeholder",
		"todo",
	} {
		if strings.Contains(text, marker) {
			return marker
		}
	}
	return ""
}

func EditName(index int, edit SubmitPlanCoverageEdit) string {
	if path := normalizePath(edit.Path); path != "" {
		return path
	}
	if purpose := strings.TrimSpace(edit.Purpose); purpose != "" {
		if len(purpose) > 60 {
			purpose = strings.TrimSpace(purpose[:60]) + "..."
		}
		return "edits[" + strconv.Itoa(index) + "] " + purpose
	}
	return fmt.Sprintf("edits[%d]", index)
}

func normalizeScope(scope SubmitPlanCoverageScope) SubmitPlanCoverageScope {
	return SubmitPlanCoverageScope{
		Coverage:               normalizedUnique(scope.Coverage),
		AllowedExact:           normalizedUnique(scope.AllowedExact),
		DirectoryScopes:        normalizedUnique(scope.DirectoryScopes),
		AllowedDirectoryScopes: normalizedUnique(scope.AllowedDirectoryScopes),
		AllowedOnly:            scope.AllowedOnly,
	}
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "./")
	path = strings.Trim(path, "/")
	return path
}

func normalizedUnique(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = normalizePath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

func appendUniqueNormalized(paths []string, path string) []string {
	path = normalizePath(path)
	if path == "" {
		return paths
	}
	for _, existing := range paths {
		if normalizePath(existing) == path {
			return paths
		}
	}
	return append(paths, path)
}

func uniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func pathInDirectory(path string, dir string) bool {
	path = normalizePath(path)
	dir = normalizePath(dir)
	if path == "" || dir == "" {
		return false
	}
	return strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

func plannedCoversDirectory(planned map[string]struct{}, dir string) bool {
	for path := range planned {
		if pathInDirectory(path, dir) {
			return true
		}
	}
	return false
}

func quotedStrings(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		quoted = append(quoted, "`"+value+"`")
	}
	return strings.Join(quoted, ", ")
}
