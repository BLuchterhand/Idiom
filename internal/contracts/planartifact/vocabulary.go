package planartifact

import (
	"strings"

	plancontract "idiom/internal/contracts/plan"
)

type ContractKind string
type Owner string
type PacketStatus string
type ApprovalStatus string
type ApprovalMode string
type ApprovalExpirationPolicy string
type ExecutionPermission string
type HandoffTarget string
type HandoffStatus string
type EvidenceKind string
type VerificationKind string
type VerificationStatus string
type ClientClassification string

const (
	ContractKindPlanThenExecute      ContractKind = "plan_then_execute"
	ContractKindImplementation       ContractKind = "implementation"
	ContractKindImplementationResult ContractKind = "implementation_result"

	OwnerModel    Owner = "model"
	OwnerHarness  Owner = "harness"
	OwnerPlanner  Owner = "planner"
	OwnerExecutor Owner = "executor"

	PermissionProfilePlanThenExecute = "plan_then_execute"

	PacketStatusNotProvided PacketStatus = "not_provided"
	PacketStatusProvided    PacketStatus = "provided"
	PacketStatusAvailable   PacketStatus = "available"
	PacketStatusFresh       PacketStatus = "fresh"
	PacketStatusNotUsed     PacketStatus = "not_used"

	EvidenceKindContextPacket         EvidenceKind = "context_packet"
	EvidenceKindPlannerSubmission     EvidenceKind = "planner_submission"
	EvidenceKindHarnessObservedSource EvidenceKind = EvidenceKind(plancontract.SourceSatisfiedEvidenceKindHarnessObservedSource)

	ApprovalStatusImplicit          ApprovalStatus = "implicit"
	ApprovalStatusPartial           ApprovalStatus = "partial"
	ApprovalStatusPartiallyApproved ApprovalStatus = "partially_approved"
	ApprovalStatusApproved          ApprovalStatus = "approved"
	ApprovalStatusAutoApproved      ApprovalStatus = "auto_approved"
	ApprovalStatusNotRequired       ApprovalStatus = "not_required"
	ApprovalStatusPending           ApprovalStatus = "pending"
	ApprovalStatusAwaitingApproval  ApprovalStatus = "awaiting_approval"

	ApprovalModePlanThenExecute ApprovalMode = "plan_then_execute"
	ApprovalModeLocal           ApprovalMode = "local"
	ApprovalModeHarnessPolicy   ApprovalMode = "harness_policy"

	ApprovalExpirationWorkspaceDigest ApprovalExpirationPolicy = "workspace_fingerprint"

	ExecutionPermissionExecuteAll           ExecutionPermission = "execute_all"
	ExecutionPermissionExecuteApprovedScope ExecutionPermission = "execute_approved_scope"
	ExecutionPermissionBlocked              ExecutionPermission = "blocked"

	HandoffTargetPlanExecuteExecutor HandoffTarget = "plan_execute_executor"
	HandoffTargetExecutor            HandoffTarget = "executor"
	HandoffStatusReady               HandoffStatus = "ready"
	HandoffStatusBlocked             HandoffStatus = "blocked"

	VerificationKindExecutorLaneSanity VerificationKind = "executor_lane_sanity"

	VerificationStatusOpen          VerificationStatus = "open"
	VerificationStatusPending       VerificationStatus = "pending"
	VerificationStatusPassed        VerificationStatus = "passed"
	VerificationStatusComplete      VerificationStatus = "complete"
	VerificationStatusClosed        VerificationStatus = "closed"
	VerificationStatusSatisfied     VerificationStatus = "satisfied"
	VerificationStatusFailed        VerificationStatus = "failed"
	VerificationStatusSkipped       VerificationStatus = "skipped"
	VerificationStatusBlocked       VerificationStatus = "blocked"
	VerificationStatusNotApplicable VerificationStatus = "not_applicable"

	ClientClassificationFinance                ClientClassification = "finance"
	ClientClassificationHealthcareLifeSciences ClientClassification = "healthcare_life_sciences"
	ClientClassificationPublicSector           ClientClassification = "public_sector"
	ClientClassificationEnterpriseSaaS         ClientClassification = "enterprise_saas"
	ClientClassificationToolingVendor          ClientClassification = "tooling_vendor"
	ClientClassificationLocalModelPrivateCode  ClientClassification = "local_model_private_code"
	ClientClassificationOpenSource             ClientClassification = "open_source"
)

var approvedClientClassifications = []ClientClassification{
	ClientClassificationFinance,
	ClientClassificationHealthcareLifeSciences,
	ClientClassificationPublicSector,
	ClientClassificationEnterpriseSaaS,
	ClientClassificationToolingVendor,
	ClientClassificationLocalModelPrivateCode,
	ClientClassificationOpenSource,
}

func ApprovedClientClassifications() []string {
	out := make([]string, 0, len(approvedClientClassifications))
	for _, classification := range approvedClientClassifications {
		out = append(out, string(classification))
	}

	return out
}

func KnownClientClassification(value string) bool {
	value = strings.TrimSpace(value)
	for _, classification := range approvedClientClassifications {
		if value == string(classification) {
			return true
		}
	}

	return false
}

func KnownExecutionPermission(value string) bool {
	switch ExecutionPermission(strings.TrimSpace(value)) {
	case ExecutionPermissionExecuteAll,
		ExecutionPermissionExecuteApprovedScope,
		ExecutionPermissionBlocked:
		return true
	default:
		return false
	}
}

func KnownContractKind(value string) bool {
	switch ContractKind(strings.TrimSpace(value)) {
	case ContractKindPlanThenExecute, ContractKindImplementation, ContractKindImplementationResult:
		return true
	default:
		return false
	}
}

func KnownOwner(value string) bool {
	switch Owner(strings.TrimSpace(value)) {
	case OwnerModel, OwnerHarness, OwnerPlanner, OwnerExecutor:
		return true
	default:
		return false
	}
}

func KnownApprovalMode(value string) bool {
	switch ApprovalMode(strings.TrimSpace(value)) {
	case ApprovalModePlanThenExecute, ApprovalModeLocal, ApprovalModeHarnessPolicy:
		return true
	default:
		return false
	}
}

func KnownApprovalExpirationPolicy(value string) bool {
	switch ApprovalExpirationPolicy(strings.TrimSpace(value)) {
	case ApprovalExpirationWorkspaceDigest:
		return true
	default:
		return false
	}
}

func KnownVerificationStatus(value string) bool {
	switch VerificationStatus(strings.TrimSpace(value)) {
	case VerificationStatusOpen,
		VerificationStatusPending,
		VerificationStatusPassed,
		VerificationStatusComplete,
		VerificationStatusClosed,
		VerificationStatusSatisfied,
		VerificationStatusFailed,
		VerificationStatusSkipped,
		VerificationStatusBlocked,
		VerificationStatusNotApplicable:
		return true
	case "n_a":
		return true
	default:
		return false
	}
}

func KnownPacketStatus(value string) bool {
	switch PacketStatus(strings.TrimSpace(value)) {
	case PacketStatusNotProvided, PacketStatusProvided, PacketStatusAvailable, PacketStatusFresh, PacketStatusNotUsed:
		return true
	default:
		return false
	}
}

func KnownApprovalStatus(value string) bool {
	switch ApprovalStatus(strings.TrimSpace(value)) {
	case ApprovalStatusImplicit,
		ApprovalStatusPartial,
		ApprovalStatusPartiallyApproved,
		ApprovalStatusApproved,
		ApprovalStatusAutoApproved,
		ApprovalStatusNotRequired,
		ApprovalStatusPending,
		ApprovalStatusAwaitingApproval:
		return true
	case "n_a":
		return true
	default:
		return false
	}
}

func KnownHandoffTarget(value string) bool {
	switch HandoffTarget(strings.TrimSpace(value)) {
	case HandoffTargetPlanExecuteExecutor, HandoffTargetExecutor:
		return true
	default:
		return false
	}
}

func IsApprovalStatusPartial(value string) bool {
	switch ApprovalStatus(strings.TrimSpace(value)) {
	case ApprovalStatusPartial, ApprovalStatusPartiallyApproved:
		return true
	default:
		return false
	}
}

func ApprovalStatusSatisfied(value string) bool {
	switch strings.TrimSpace(value) {
	case string(ApprovalStatusImplicit), string(ApprovalStatusApproved), string(ApprovalStatusAutoApproved), string(ApprovalStatusNotRequired), "n_a", string(ApprovalStatusPartial), string(ApprovalStatusPartiallyApproved):
		return true
	default:
		return false
	}
}

func KnownHandoffStatus(value string) bool {
	switch HandoffStatus(strings.TrimSpace(value)) {
	case HandoffStatusReady,
		HandoffStatusBlocked:
		return true
	default:
		return false
	}
}
