package codingrules

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
)

func DefaultPolicy() PolicyFile {
	enabled := true

	return PolicyFile{
		SchemaVersion: PolicySchemaVersion,
		Defaults: Defaults{
			Enabled:     &enabled,
			Enforcement: EnforcementAdvisory,
			Severity:    SeverityWarning,
			Include:     []string{"**/*.go"},
			Exclude:     []string{"vendor/**", ".git/**", ".idiom/**"},
		},
		Rules: []RuleConfig{{
			ID:          RuleNoPassThroughWrapper,
			Enabled:     &enabled,
			Languages:   []LanguageID{LanguageGo},
			Kind:        RuleKindStructure,
			Enforcement: EnforcementAdvisory,
			Severity:    SeverityWarning,
			AllowWhen: []AllowanceReason{
				AllowImplementsInterface,
				AllowCompatibilityAPI,
				AllowBoundaryAdapter,
				AllowValidation,
				AllowErrorContext,
				AllowObservability,
				AllowPermissionCheck,
				AllowDomainUseCaseName,
			},
			Options: RuleOptions{
				PassThroughWrapper: &PassThroughWrapperOptions{
					MaxBodyStatements: 1,
					IgnoreTests:       true,
					IgnoreGenerated:   true,
				},
			},
		}},
	}
}

func DecodePolicyFile(data []byte) (PolicyFile, error) {
	if unknown, err := UnknownJSONKeys(data, PolicyFile{}); err != nil {
		return PolicyFile{}, fmt.Errorf("inspect coding rules keys: %w", err)
	} else if len(unknown) > 0 {
		return PolicyFile{}, fmt.Errorf("unknown coding rules key(s): %s", strings.Join(unknown, ", "))
	}

	var policy PolicyFile
	if err := json.Unmarshal(data, &policy); err != nil {
		return PolicyFile{}, fmt.Errorf("parse coding rules: %w", err)
	}

	policy = NormalizePolicy(policy)
	if err := ValidatePolicy(policy); err != nil {
		return PolicyFile{}, err
	}

	return policy, nil
}

func DecodePolicy(r io.Reader) (PolicyFile, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return PolicyFile{}, err
	}

	return DecodePolicyFile(data)
}

func NormalizePolicy(policy PolicyFile) PolicyFile {
	defaultPolicy := DefaultPolicy()
	if strings.TrimSpace(policy.SchemaVersion) == "" {
		policy.SchemaVersion = PolicySchemaVersion
	}

	if policy.Defaults.Enforcement == "" {
		policy.Defaults.Enforcement = defaultPolicy.Defaults.Enforcement
	}
	if policy.Defaults.Severity == "" {
		policy.Defaults.Severity = defaultPolicy.Defaults.Severity
	}
	if policy.Defaults.Enabled == nil {
		enabled := true
		policy.Defaults.Enabled = &enabled
	}
	if len(policy.Defaults.Include) == 0 {
		policy.Defaults.Include = append([]string(nil), defaultPolicy.Defaults.Include...)
	}
	if len(policy.Defaults.Exclude) == 0 {
		policy.Defaults.Exclude = append([]string(nil), defaultPolicy.Defaults.Exclude...)
	}

	if len(policy.Rules) == 0 {
		policy.Rules = append([]RuleConfig(nil), defaultPolicy.Rules...)
	}

	for i := range policy.Rules {
		rule := &policy.Rules[i]
		if rule.Enabled == nil {
			enabled := *policy.Defaults.Enabled
			rule.Enabled = &enabled
		}
		if len(rule.Languages) == 0 {
			rule.Languages = []LanguageID{LanguageGo}
		}
		if rule.Kind == "" {
			rule.Kind = RuleKindStructure
		}
		if rule.Enforcement == "" {
			rule.Enforcement = policy.Defaults.Enforcement
		}
		if rule.Severity == "" {
			rule.Severity = policy.Defaults.Severity
		}
		if len(rule.Include) == 0 {
			rule.Include = append([]string(nil), policy.Defaults.Include...)
		}
		if len(rule.Exclude) == 0 {
			rule.Exclude = append([]string(nil), policy.Defaults.Exclude...)
		}
		if rule.ID == RuleNoPassThroughWrapper && rule.Options.PassThroughWrapper == nil {
			rule.Options.PassThroughWrapper = &PassThroughWrapperOptions{
				MaxBodyStatements: 1,
				IgnoreTests:       true,
				IgnoreGenerated:   true,
			}
		}
	}

	return policy
}

func ValidatePolicy(policy PolicyFile) error {
	if policy.SchemaVersion != PolicySchemaVersion {
		return fmt.Errorf("coding rules schema_version must be %q", PolicySchemaVersion)
	}
	if err := validateEnforcement("defaults.enforcement", policy.Defaults.Enforcement); err != nil {
		return err
	}
	if err := validateSeverity("defaults.severity", policy.Defaults.Severity); err != nil {
		return err
	}

	seen := make(map[RuleID]struct{}, len(policy.Rules))
	for i, rule := range policy.Rules {
		prefix := fmt.Sprintf("rules[%d]", i)
		if !knownRuleID(rule.ID) {
			return fmt.Errorf("%s.id is not recognized: %q", prefix, rule.ID)
		}
		if _, ok := seen[rule.ID]; ok {
			return fmt.Errorf("%s.id duplicates rule %q", prefix, rule.ID)
		}
		seen[rule.ID] = struct{}{}
		if err := validateRuleKind(prefix+".kind", rule.Kind); err != nil {
			return err
		}
		if err := validateEnforcement(prefix+".enforcement", rule.Enforcement); err != nil {
			return err
		}
		if err := validateSeverity(prefix+".severity", rule.Severity); err != nil {
			return err
		}
		for j, lang := range rule.Languages {
			if lang != LanguageGo {
				return fmt.Errorf("%s.languages[%d] is not supported: %q", prefix, j, lang)
			}
		}
		for j, reason := range rule.AllowWhen {
			if !knownAllowance(reason) {
				return fmt.Errorf("%s.allow_when[%d] is not recognized: %q", prefix, j, reason)
			}
		}
		if rule.ID == RuleNoPassThroughWrapper && rule.Options.PassThroughWrapper != nil {
			maxStatements := rule.Options.PassThroughWrapper.MaxBodyStatements
			if maxStatements < 0 {
				return fmt.Errorf("%s.options.pass_through_wrapper.max_body_statements must be non-negative", prefix)
			}
			if maxStatements > 1 {
				return fmt.Errorf("%s.options.pass_through_wrapper.max_body_statements must be 0 or 1 for %s", prefix, RuleNoPassThroughWrapper)
			}
		}
	}

	for i, suppression := range policy.Suppressions {
		prefix := fmt.Sprintf("suppressions[%d]", i)
		if !knownRuleID(suppression.RuleID) {
			return fmt.Errorf("%s.rule_id is not recognized: %q", prefix, suppression.RuleID)
		}
		if strings.TrimSpace(suppression.Path) == "" {
			return fmt.Errorf("%s.path is required", prefix)
		}
		if !knownAllowance(suppression.Reason) {
			return fmt.Errorf("%s.reason is not recognized: %q", prefix, suppression.Reason)
		}
		if strings.TrimSpace(suppression.Rationale) == "" {
			return fmt.Errorf("%s.rationale is required", prefix)
		}
	}

	return nil
}

func knownRuleID(id RuleID) bool {
	return id == RuleNoPassThroughWrapper
}

func validateRuleKind(path string, kind RuleKind) error {
	if kind != RuleKindStructure {
		return fmt.Errorf("%s must be %q", path, RuleKindStructure)
	}

	return nil
}

func validateEnforcement(path string, enforcement EnforcementLevel) error {
	switch enforcement {
	case EnforcementDisabled, EnforcementAdvisory, EnforcementBlocking:
		return nil
	default:
		return fmt.Errorf("%s must be one of disabled, advisory, blocking", path)
	}
}

func validateSeverity(path string, severity FindingSeverity) error {
	switch severity {
	case SeverityInfo, SeverityWarning, SeverityError:
		return nil
	default:
		return fmt.Errorf("%s must be one of info, warning, error", path)
	}
}

func knownAllowance(reason AllowanceReason) bool {
	return slices.Contains([]AllowanceReason{
		AllowImplementsInterface,
		AllowCompatibilityAPI,
		AllowBoundaryAdapter,
		AllowValidation,
		AllowErrorContext,
		AllowObservability,
		AllowPermissionCheck,
		AllowDomainUseCaseName,
	}, reason)
}

func UnknownJSONKeys(data []byte, target any) ([]string, error) {
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}

	var unknown []string
	collectUnknownJSONKeys(decoded, reflect.TypeOf(target), "", &unknown)
	slices.Sort(unknown)

	return unknown, nil
}

var rawMessageType = reflect.TypeOf(json.RawMessage{})

func collectUnknownJSONKeys(value any, typ reflect.Type, path string, unknown *[]string) {
	if typ == nil || value == nil {
		return
	}

	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	if typ == rawMessageType {
		return
	}

	switch typ.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return
		}

		fields := jsonFieldTypes(typ)
		for key, child := range object {
			if strings.HasPrefix(key, "_") {
				continue
			}

			fieldType, ok := fields[key]
			if !ok {
				*unknown = append(*unknown, joinConfigPath(path, key))
				continue
			}

			collectUnknownJSONKeys(child, fieldType, joinConfigPath(path, key), unknown)
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return
		}

		for i, item := range items {
			collectUnknownJSONKeys(item, typ.Elem(), fmt.Sprintf("%s[%d]", path, i), unknown)
		}
	case reflect.Map:
		object, ok := value.(map[string]any)
		if !ok {
			return
		}

		elem := typ.Elem()
		for key, child := range object {
			collectUnknownJSONKeys(child, elem, joinConfigPath(path, key), unknown)
		}
	}
}

func jsonFieldTypes(typ reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}

		tag := field.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}

		fields[name] = field.Type
	}

	return fields
}

func joinConfigPath(base string, key string) string {
	key = strings.TrimSpace(key)
	if base == "" {
		return key
	}
	if key == "" {
		return base
	}

	return base + "." + key
}

func FindingHash(finding Finding) string {
	payload := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s\x00%s",
		finding.RuleID,
		finding.Path,
		finding.Line,
		finding.Column,
		finding.Symbol,
		finding.Message,
	)
	sum := sha256.Sum256([]byte(payload))

	return fmt.Sprintf("%x", sum[:8])
}

func AddFindingHashes(findings []Finding) {
	for i := range findings {
		if findings[i].Hash == "" {
			findings[i].Hash = FindingHash(findings[i])
		}
	}
}

func EffectiveStatus(findings []Finding) FindingStatus {
	if len(findings) == 0 {
		return StatusPass
	}

	for _, finding := range findings {
		if finding.Enforcement == EnforcementBlocking {
			return StatusFail
		}
	}

	return StatusWarn
}

func FindingSuppressionKey(ruleID RuleID, path string, symbol string) string {
	return fmt.Sprintf("%s:%s:%s", ruleID, path, symbol)
}
