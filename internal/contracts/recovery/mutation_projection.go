package recovery

import "fmt"

var (
	canonicalMutationScopeRuleKinds = map[string]struct{}{
		"exact_file": {}, "directory_subtree": {}, "parent_directory_creation": {},
	}
	canonicalMutationScopeRuleSources = map[string]struct{}{
		"required_path": {}, "allowed_path": {}, "authorized_new_file": {},
	}
	mutationCommitKinds = map[string]struct{}{
		"created_directory": {}, "replaced_file": {}, "appended_file": {}, "removed_file": {},
	}
	mutationTargetRoles = map[string]struct{}{
		"source": {}, "destination": {}, "file": {}, "directory": {},
	}
	mutationPreimageClasses = map[string]struct{}{
		"required": {}, "optional": {}, "absent": {}, "directory": {},
	}
)

// CanonicalMutationScopeRuleProjection is the recovery-owned durable shape of
// one workspace mutation-scope rule. Runtime translates the workspace value
// directly; recovery never reconstructs live authorization from it.
type CanonicalMutationScopeRuleProjection struct {
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	CanonicalID string `json:"canonical_id"`
}

type CanonicalMutationScopePresentationProjection struct {
	PresentedPath string `json:"presented_path"`
	CanonicalID   string `json:"canonical_id"`
	Source        string `json:"source"`
}

type CanonicalMutationScopeProjection struct {
	RequiredCoverageIDs []string                                       `json:"required_coverage_ids,omitempty"`
	Rules               []CanonicalMutationScopeRuleProjection         `json:"rules,omitempty"`
	Presentations       []CanonicalMutationScopePresentationProjection `json:"presentations,omitempty"`
}

type MutationCommitEffectProjection struct {
	CanonicalID string `json:"canonical_id"`
	Kind        string `json:"kind"`
	Role        string `json:"role"`
}

type MutationTargetProjection struct {
	CanonicalID   string `json:"canonical_id"`
	Role          string `json:"role"`
	PreimageClass string `json:"preimage_class"`
}

type MutationCommitProjection struct {
	Scope              *CanonicalMutationScopeProjection `json:"scope,omitempty"`
	Effects            []MutationCommitEffectProjection  `json:"effects,omitempty"`
	PostimageFileIDs   []string                          `json:"postimage_file_ids,omitempty"`
	AffectedFileIDs    []string                          `json:"affected_file_ids,omitempty"`
	FailedTargets      []MutationTargetProjection        `json:"failed_targets,omitempty"`
	UnattemptedTargets []MutationTargetProjection        `json:"unattempted_targets,omitempty"`
}

func validateCanonicalMutationScopeProjection(field string, scope *CanonicalMutationScopeProjection) error {
	if scope == nil {
		return nil
	}
	if err := ValidateRecoveryPaths(field+".required_coverage_ids", scope.RequiredCoverageIDs); err != nil {
		return err
	}
	for index, rule := range scope.Rules {
		if _, ok := canonicalMutationScopeRuleKinds[rule.Kind]; !ok {
			return fmt.Errorf("%s.rules[%d].kind is invalid: %q", field, index, rule.Kind)
		}
		if _, ok := canonicalMutationScopeRuleSources[rule.Source]; !ok {
			return fmt.Errorf("%s.rules[%d].source is invalid: %q", field, index, rule.Source)
		}
		if rule.CanonicalID == "" {
			return fmt.Errorf("%s.rules[%d].canonical_id is required", field, index)
		}
		if err := ValidateRecoveryPaths(
			fmt.Sprintf("%s.rules[%d].canonical_id", field, index),
			[]string{rule.CanonicalID},
		); err != nil {
			return err
		}
	}
	for index, presentation := range scope.Presentations {
		if _, ok := canonicalMutationScopeRuleSources[presentation.Source]; !ok {
			return fmt.Errorf("%s.presentations[%d].source is invalid: %q", field, index, presentation.Source)
		}
		if presentation.PresentedPath == "" || presentation.CanonicalID == "" {
			return fmt.Errorf("%s.presentations[%d] paths are required", field, index)
		}
		if err := ValidateRecoveryPaths(
			fmt.Sprintf("%s.presentations[%d].presented_path", field, index),
			[]string{presentation.PresentedPath},
		); err != nil {
			return err
		}
		if err := ValidateRecoveryPaths(
			fmt.Sprintf("%s.presentations[%d].canonical_id", field, index),
			[]string{presentation.CanonicalID},
		); err != nil {
			return err
		}
	}
	return nil
}

func validateMutationCommitProjections(field string, receipts []MutationCommitProjection) error {
	for receiptIndex, receipt := range receipts {
		receiptField := fmt.Sprintf("%s[%d]", field, receiptIndex)
		if err := validateCanonicalMutationScopeProjection(receiptField+".scope", receipt.Scope); err != nil {
			return err
		}
		if err := ValidateRecoveryPaths(receiptField+".postimage_file_ids", receipt.PostimageFileIDs); err != nil {
			return err
		}
		if err := ValidateRecoveryPaths(receiptField+".affected_file_ids", receipt.AffectedFileIDs); err != nil {
			return err
		}
		for effectIndex, effect := range receipt.Effects {
			if _, ok := mutationCommitKinds[effect.Kind]; !ok {
				return fmt.Errorf("%s.effects[%d].kind is invalid: %q", receiptField, effectIndex, effect.Kind)
			}
			if _, ok := mutationTargetRoles[effect.Role]; !ok {
				return fmt.Errorf("%s.effects[%d].role is invalid: %q", receiptField, effectIndex, effect.Role)
			}
			if effect.CanonicalID == "" {
				return fmt.Errorf("%s.effects[%d].canonical_id is required", receiptField, effectIndex)
			}
			if err := ValidateRecoveryPaths(
				fmt.Sprintf("%s.effects[%d].canonical_id", receiptField, effectIndex),
				[]string{effect.CanonicalID},
			); err != nil {
				return err
			}
		}
		for targetIndex, target := range receipt.FailedTargets {
			if err := validateMutationTargetProjection(receiptField+".failed_targets", targetIndex, target); err != nil {
				return err
			}
			if err := ValidateRecoveryPaths(
				fmt.Sprintf("%s.failed_targets[%d].canonical_id", receiptField, targetIndex),
				[]string{target.CanonicalID},
			); err != nil {
				return err
			}
		}
		for targetIndex, target := range receipt.UnattemptedTargets {
			if err := validateMutationTargetProjection(receiptField+".unattempted_targets", targetIndex, target); err != nil {
				return err
			}
			if err := ValidateRecoveryPaths(
				fmt.Sprintf("%s.unattempted_targets[%d].canonical_id", receiptField, targetIndex),
				[]string{target.CanonicalID},
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMutationTargetProjection(field string, index int, target MutationTargetProjection) error {
	if _, ok := mutationTargetRoles[target.Role]; !ok {
		return fmt.Errorf("%s[%d].role is invalid: %q", field, index, target.Role)
	}
	if _, ok := mutationPreimageClasses[target.PreimageClass]; !ok {
		return fmt.Errorf("%s[%d].preimage_class is invalid: %q", field, index, target.PreimageClass)
	}
	if target.CanonicalID == "" {
		return fmt.Errorf("%s[%d].canonical_id is required", field, index)
	}
	return nil
}
