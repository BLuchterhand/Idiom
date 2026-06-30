package codingrules

const (
	PolicySchemaVersion = "idiom.coding_rules.v1"
	ReportSchemaVersion = "idiom.coding_rules.report.v1"
)

type RuleID string
type LanguageID string
type RuleKind string
type EnforcementLevel string
type FindingSeverity string
type FindingStatus string
type EvidenceKind string
type AllowanceReason string

const (
	LanguageGo LanguageID = "go"

	RuleNoPassThroughWrapper RuleID = "go.no_pass_through_wrapper"

	RuleKindStructure RuleKind = "structure"

	EnforcementDisabled EnforcementLevel = "disabled"
	EnforcementAdvisory EnforcementLevel = "advisory"
	EnforcementBlocking EnforcementLevel = "blocking"

	SeverityInfo    FindingSeverity = "info"
	SeverityWarning FindingSeverity = "warning"
	SeverityError   FindingSeverity = "error"

	StatusPass FindingStatus = "pass"
	StatusWarn FindingStatus = "warn"
	StatusFail FindingStatus = "fail"

	EvidenceSingleStatement         EvidenceKind = "single_statement"
	EvidenceSingleReturnCall        EvidenceKind = "single_return_call"
	EvidenceSingleExpressionCall    EvidenceKind = "single_expression_call"
	EvidenceSameArgumentForwarding  EvidenceKind = "same_argument_forwarding"
	EvidenceNoValidation            EvidenceKind = "no_validation"
	EvidenceNoErrorContext          EvidenceKind = "no_error_context"
	EvidenceNoTypeTranslation       EvidenceKind = "no_type_translation"
	EvidenceNoObservability         EvidenceKind = "no_observability"
	EvidenceNoPermissionCheck       EvidenceKind = "no_permission_check"
	EvidenceSuppressionsRequireNote EvidenceKind = "suppression_requires_rationale"

	AllowImplementsInterface AllowanceReason = "implements_interface"
	AllowCompatibilityAPI    AllowanceReason = "compatibility_api"
	AllowBoundaryAdapter     AllowanceReason = "boundary_adapter"
	AllowValidation          AllowanceReason = "validation"
	AllowErrorContext        AllowanceReason = "error_context"
	AllowObservability       AllowanceReason = "observability"
	AllowPermissionCheck     AllowanceReason = "permission_check"
	AllowDomainUseCaseName   AllowanceReason = "domain_use_case_name"
)

type PolicyFile struct {
	SchemaVersion string        `json:"schema_version"`
	Defaults      Defaults      `json:"defaults,omitempty"`
	Rules         []RuleConfig  `json:"rules,omitempty"`
	Suppressions  []Suppression `json:"suppressions,omitempty"`
}

type Defaults struct {
	Enabled     *bool            `json:"enabled,omitempty"`
	Enforcement EnforcementLevel `json:"enforcement,omitempty"`
	Severity    FindingSeverity  `json:"severity,omitempty"`
	Include     []string         `json:"include,omitempty"`
	Exclude     []string         `json:"exclude,omitempty"`
}

type RuleConfig struct {
	ID          RuleID            `json:"id"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Languages   []LanguageID      `json:"languages,omitempty"`
	Kind        RuleKind          `json:"kind,omitempty"`
	Enforcement EnforcementLevel  `json:"enforcement,omitempty"`
	Severity    FindingSeverity   `json:"severity,omitempty"`
	Include     []string          `json:"include,omitempty"`
	Exclude     []string          `json:"exclude,omitempty"`
	AllowWhen   []AllowanceReason `json:"allow_when,omitempty"`
	Options     RuleOptions       `json:"options,omitempty"`
}

type RuleOptions struct {
	PassThroughWrapper *PassThroughWrapperOptions `json:"pass_through_wrapper,omitempty"`
}

type PassThroughWrapperOptions struct {
	MaxBodyStatements int  `json:"max_body_statements,omitempty"`
	IgnoreTests       bool `json:"ignore_tests,omitempty"`
	IgnoreGenerated   bool `json:"ignore_generated,omitempty"`
}

type Suppression struct {
	RuleID    RuleID          `json:"rule_id"`
	Path      string          `json:"path"`
	Symbol    string          `json:"symbol,omitempty"`
	Reason    AllowanceReason `json:"reason"`
	Rationale string          `json:"rationale"`
}

type Summary struct {
	FilesScanned int `json:"files_scanned"`
	Findings     int `json:"findings"`
	Infos        int `json:"infos"`
	Warnings     int `json:"warnings"`
	Errors       int `json:"errors"`
	Blocking     int `json:"blocking"`
	Suppressed   int `json:"suppressed"`
}

type Report struct {
	SchemaVersion string          `json:"schema_version"`
	WorkspaceRoot string          `json:"workspace_root,omitempty"`
	ConfigPath    string          `json:"config_path,omitempty"`
	Status        FindingStatus   `json:"status"`
	Summary       Summary         `json:"summary"`
	Findings      []Finding       `json:"findings,omitempty"`
	Warnings      []ReportWarning `json:"warnings,omitempty"`
}

type ReportWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Finding struct {
	Hash            string           `json:"hash,omitempty"`
	RuleID          RuleID           `json:"rule_id"`
	Language        LanguageID       `json:"language,omitempty"`
	Severity        FindingSeverity  `json:"severity"`
	Enforcement     EnforcementLevel `json:"enforcement"`
	Path            string           `json:"path"`
	Line            int              `json:"line,omitempty"`
	Column          int              `json:"column,omitempty"`
	Symbol          string           `json:"symbol,omitempty"`
	Message         string           `json:"message"`
	Evidence        []Evidence       `json:"evidence,omitempty"`
	SuggestedAction string           `json:"suggested_action,omitempty"`
	SuppressionKey  string           `json:"suppression_key,omitempty"`
}

type Evidence struct {
	Kind   EvidenceKind `json:"kind"`
	Detail string       `json:"detail,omitempty"`
	Path   string       `json:"path,omitempty"`
	Line   int          `json:"line,omitempty"`
}

func SeverityRank(severity FindingSeverity) int {
	switch severity {
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

func EnforcementBlocks(enforcement EnforcementLevel) bool {
	return enforcement == EnforcementBlocking
}
