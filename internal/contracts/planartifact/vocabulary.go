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

func KnownClientClassification(value string) bool {
	return knownTrimmed(value, approvedClientClassifications...)
}

func KnownExecutionPermission(value string) bool {
	return knownTrimmed(value, ExecutionPermissionExecuteAll, ExecutionPermissionExecuteApprovedScope, ExecutionPermissionBlocked)
}

func KnownContractKind(value string) bool {
	return knownTrimmed(value, ContractKindPlanThenExecute, ContractKindImplementation, ContractKindImplementationResult)
}

func KnownOwner(value string) bool {
	return knownTrimmed(value, OwnerModel, OwnerHarness, OwnerPlanner, OwnerExecutor)
}

func KnownApprovalMode(value string) bool {
	return knownTrimmed(value, ApprovalModePlanThenExecute, ApprovalModeLocal, ApprovalModeHarnessPolicy)
}

func KnownApprovalExpirationPolicy(value string) bool {
	return knownTrimmed(value, ApprovalExpirationWorkspaceDigest)
}

func KnownVerificationStatus(value string) bool {
	return knownTrimmed(value,
		VerificationStatusOpen,
		VerificationStatusPending,
		VerificationStatusPassed,
		VerificationStatusComplete,
		VerificationStatusClosed,
		VerificationStatusSatisfied,
		VerificationStatusFailed,
		VerificationStatusSkipped,
		VerificationStatusBlocked,
		VerificationStatusNotApplicable,
		VerificationStatus("n_a"),
	)
}

func KnownPacketStatus(value string) bool {
	return knownTrimmed(value, PacketStatusNotProvided, PacketStatusProvided, PacketStatusAvailable, PacketStatusFresh, PacketStatusNotUsed)
}

func KnownApprovalStatus(value string) bool {
	return knownTrimmed(value,
		ApprovalStatusImplicit,
		ApprovalStatusPartial,
		ApprovalStatusPartiallyApproved,
		ApprovalStatusApproved,
		ApprovalStatusAutoApproved,
		ApprovalStatusNotRequired,
		ApprovalStatusPending,
		ApprovalStatusAwaitingApproval,
		ApprovalStatus("n_a"),
	)
}

func KnownHandoffTarget(value string) bool {
	return knownTrimmed(value, HandoffTargetPlanExecuteExecutor, HandoffTargetExecutor)
}

func IsApprovalStatusPartial(value string) bool {
	return knownTrimmed(value, ApprovalStatusPartial, ApprovalStatusPartiallyApproved)
}

func ApprovalStatusSatisfied(value string) bool {
	return knownTrimmed(value, ApprovalStatusImplicit, ApprovalStatusApproved, ApprovalStatusAutoApproved, ApprovalStatusNotRequired, ApprovalStatus("n_a"), ApprovalStatusPartial, ApprovalStatusPartiallyApproved)
}

func KnownHandoffStatus(value string) bool {
	return knownTrimmed(value, HandoffStatusReady, HandoffStatusBlocked)
}

func knownTrimmed[T ~string](value string, allowed ...T) bool {
	value = strings.TrimSpace(value)
	for _, candidate := range allowed {
		if value == string(candidate) {
			return true
		}
	}
	return false
}
