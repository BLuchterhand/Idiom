package plancontract

import "strings"

type SourceSatisfiedEvidenceRef struct {
	Path string
	Kind string
}

type SourceSatisfiedPathCoverageInput struct {
	RequiredPaths        []string
	SourceSatisfiedPaths []string
	Evidence             []SourceSatisfiedEvidenceRef
	ApprovedPaths        []string
	PendingPaths         []string
	RequireEvidence      bool
}

type SourceSatisfiedPathViolation struct {
	Path   string
	Code   string
	Reason string
}

func ValidateSourceSatisfiedPathCoverage(input SourceSatisfiedPathCoverageInput) []SourceSatisfiedPathViolation {
	sourceSatisfied := UniqueScopePaths(input.SourceSatisfiedPaths)
	if len(sourceSatisfied) == 0 {
		return nil
	}
	required := scopePathSet(input.RequiredPaths)
	approved := scopePathSet(input.ApprovedPaths)
	pending := scopePathSet(input.PendingPaths)
	evidence := sourceSatisfiedCurrentStateEvidenceSet(input.Evidence)
	var violations []SourceSatisfiedPathViolation
	for _, path := range sourceSatisfied {
		if len(required) == 0 {
			violations = append(violations, SourceSatisfiedPathViolation{
				Path:   path,
				Code:   "missing_required_scope",
				Reason: "source-satisfied path has no declared required scope",
			})
		} else if _, ok := required[path]; !ok {
			violations = append(violations, SourceSatisfiedPathViolation{
				Path:   path,
				Code:   "outside_required_scope",
				Reason: "source-satisfied path is outside declared required scope",
			})
		}
		if _, ok := pending[path]; ok {
			violations = append(violations, SourceSatisfiedPathViolation{
				Path:   path,
				Code:   "pending_approval",
				Reason: "source-satisfied path is still pending approval",
			})
		}
		if len(approved) > 0 {
			if _, ok := approved[path]; !ok {
				violations = append(violations, SourceSatisfiedPathViolation{
					Path:   path,
					Code:   "outside_approved_scope",
					Reason: "source-satisfied path is outside approved scope",
				})
			}
		}
		if input.RequireEvidence {
			if _, ok := evidence[path]; !ok {
				violations = append(violations, SourceSatisfiedPathViolation{
					Path:   path,
					Code:   "missing_current_state_evidence",
					Reason: "source-satisfied path lacks current-state source evidence",
				})
			}
		}
	}
	return violations
}

func SourceSatisfiedCoveragePathSet(paths []string, violations []SourceSatisfiedPathViolation) map[string]struct{} {
	blocked := make(map[string]struct{}, len(violations))
	for _, violation := range violations {
		if path := NormalizeScopePath(violation.Path); path != "" {
			blocked[path] = struct{}{}
		}
	}
	out := make(map[string]struct{}, len(paths))
	for _, path := range UniqueScopePaths(paths) {
		if _, ok := blocked[path]; ok {
			continue
		}
		out[path] = struct{}{}
	}
	return out
}

func sourceSatisfiedCurrentStateEvidenceSet(evidence []SourceSatisfiedEvidenceRef) map[string]struct{} {
	out := map[string]struct{}{}
	for _, ref := range evidence {
		path := NormalizeScopePath(ref.Path)
		if path == "" || !sourceSatisfiedEvidenceKindProvesCurrentState(ref.Kind) {
			continue
		}
		out[path] = struct{}{}
	}
	return out
}

func sourceSatisfiedEvidenceKindProvesCurrentState(kind string) bool {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.ReplaceAll(kind, "-", "_")
	switch kind {
	case "harness_observed_source", "harness_observed_current_state",
		"verified_current_source", "verified_current_state":
		return true
	default:
		return false
	}
}

func scopePathSet(paths []string) map[string]struct{} {
	out := make(map[string]struct{}, len(paths))
	for _, path := range UniqueScopePaths(paths) {
		out[path] = struct{}{}
	}
	return out
}
