package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/textutil"
)

// WorkspaceSnapshotPurpose names the host-owned reason for taking a rooted
// source snapshot. The closed vocabulary prevents this API from becoming an
// unclassified general-purpose filesystem reader.
type WorkspaceSnapshotPurpose string

const (
	WorkspaceSnapshotSubmitPlanAnchor  WorkspaceSnapshotPurpose = "submit_plan_anchor"
	WorkspaceSnapshotReviewRepair      WorkspaceSnapshotPurpose = "review_repair"
	WorkspaceSnapshotCitation          WorkspaceSnapshotPurpose = "citation"
	WorkspaceSnapshotCompletionSnippet WorkspaceSnapshotPurpose = "completion_snippet"
	WorkspaceSnapshotNewFileWaiver     WorkspaceSnapshotPurpose = "new_file_waiver"
	WorkspaceSnapshotSourceWindow      WorkspaceSnapshotPurpose = "source_window"
)

const (
	WorkspaceSnapshotMaxPaths             = 32
	workspaceSnapshotDefaultBodyByteLimit = 2 * 1024 * 1024
)

type WorkspaceSnapshotEntryKind string

const (
	WorkspaceSnapshotFile    WorkspaceSnapshotEntryKind = "file"
	WorkspaceSnapshotMissing WorkspaceSnapshotEntryKind = "missing"
)

// WorkspaceSnapshotRequest describes one invocation-bound snapshot. Paths are
// interpreted with the same workspace/workdir rules as tool requests.
type WorkspaceSnapshotRequest struct {
	Purpose      WorkspaceSnapshotPurpose
	Paths        []string
	Workdir      string
	AllowMissing bool
	MaxBodyBytes int
}

// WorkspaceSnapshotEvidence is safe to retain after the source body is no
// longer needed. Digest is populated only for regular files.
type WorkspaceSnapshotEvidence struct {
	DigestSHA256 string
	SizeBytes    int64
}

// WorkspaceSnapshotMetadata records the authority applied to a snapshot
// entry. PolicyDecision is the effective workspace tool-policy decision for a
// read_file request against the presented path.
type WorkspaceSnapshotMetadata struct {
	PolicySubject  string
	PolicyDecision toolpolicy.Decision
	Protected      bool
}

type WorkspaceSnapshotAuditEventKind string

const (
	WorkspaceSnapshotAuditAuthority WorkspaceSnapshotAuditEventKind = "authority"
	WorkspaceSnapshotAuditRead      WorkspaceSnapshotAuditEventKind = "read"
	WorkspaceSnapshotAuditError     WorkspaceSnapshotAuditEventKind = "error"
)

type WorkspaceSnapshotAuditOutcome string

const (
	WorkspaceSnapshotOutcomeDenied   WorkspaceSnapshotAuditOutcome = "denied"
	WorkspaceSnapshotOutcomeApproved WorkspaceSnapshotAuditOutcome = "approved"
	WorkspaceSnapshotOutcomeError    WorkspaceSnapshotAuditOutcome = "error"
	WorkspaceSnapshotOutcomeMissing  WorkspaceSnapshotAuditOutcome = "missing"
	WorkspaceSnapshotOutcomeAllowed  WorkspaceSnapshotAuditOutcome = "allowed"
	WorkspaceSnapshotOutcomeMetadata WorkspaceSnapshotAuditOutcome = "metadata"
	WorkspaceSnapshotOutcomeBody     WorkspaceSnapshotAuditOutcome = "body"
	WorkspaceSnapshotOutcomeStale    WorkspaceSnapshotAuditOutcome = "stale"
	WorkspaceSnapshotOutcomeCurrent  WorkspaceSnapshotAuditOutcome = "current"
)

type WorkspaceSnapshotAuditReason string

const (
	WorkspaceSnapshotReasonSnapshotFailed        WorkspaceSnapshotAuditReason = "snapshot_failed"
	WorkspaceSnapshotReasonPolicyDenied          WorkspaceSnapshotAuditReason = "policy_denied"
	WorkspaceSnapshotReasonAuthorized            WorkspaceSnapshotAuditReason = "authorized"
	WorkspaceSnapshotReasonApprovalAbsent        WorkspaceSnapshotAuditReason = "approval_absent"
	WorkspaceSnapshotReasonApprovalUnattributed  WorkspaceSnapshotAuditReason = "approval_unattributed"
	WorkspaceSnapshotReasonApprovalWrongSession  WorkspaceSnapshotAuditReason = "approval_wrong_session"
	WorkspaceSnapshotReasonApprovalCanceled      WorkspaceSnapshotAuditReason = "approval_canceled"
	WorkspaceSnapshotReasonApprovalError         WorkspaceSnapshotAuditReason = "approval_error"
	WorkspaceSnapshotReasonApprovalDenied        WorkspaceSnapshotAuditReason = "approval_denied"
	WorkspaceSnapshotReasonApprovalGranted       WorkspaceSnapshotAuditReason = "approval_granted"
	WorkspaceSnapshotReasonRootedAbsence         WorkspaceSnapshotAuditReason = "rooted_absence"
	WorkspaceSnapshotReasonOpenedIdentity        WorkspaceSnapshotAuditReason = "opened_identity"
	WorkspaceSnapshotReasonExistenceOnly         WorkspaceSnapshotAuditReason = "existence_only"
	WorkspaceSnapshotReasonIdentityBoundRead     WorkspaceSnapshotAuditReason = "identity_bound_read"
	WorkspaceSnapshotReasonOpenFailed            WorkspaceSnapshotAuditReason = "open_failed"
	WorkspaceSnapshotReasonFreshnessScanFailed   WorkspaceSnapshotAuditReason = "freshness_scan_failed"
	WorkspaceSnapshotReasonDigestRangeMismatch   WorkspaceSnapshotAuditReason = "digest_or_range_mismatch"
	WorkspaceSnapshotReasonDigestAndRangeMatched WorkspaceSnapshotAuditReason = "digest_and_range_match"
)

type WorkspaceSnapshotAuditEvent struct {
	Kind                   WorkspaceSnapshotAuditEventKind
	Purpose                WorkspaceSnapshotPurpose
	CanonicalID            string
	Outcome                WorkspaceSnapshotAuditOutcome
	Reason                 WorkspaceSnapshotAuditReason
	PolicyDecision         toolpolicy.Decision
	Protected              bool
	ExecutionIdentityState WorkspaceSnapshotExecutionIdentityState
	ApprovalPresent        bool
	ApprovalMatched        bool
	SizeBytes              int64
	DigestSHA256           string
	WindowSHA256           string
	StartLine              int
	EndLine                int
	TotalLines             int
}

type WorkspaceSnapshotExecutionIdentityState string

const (
	WorkspaceSnapshotIdentityFallback         WorkspaceSnapshotExecutionIdentityState = "fallback"
	WorkspaceSnapshotIdentityExplicitEmpty    WorkspaceSnapshotExecutionIdentityState = "explicit_empty"
	WorkspaceSnapshotIdentityExplicitNonempty WorkspaceSnapshotExecutionIdentityState = "explicit_nonempty"
)

type WorkspaceSnapshotAuditor interface {
	RecordWorkspaceSnapshot(context.Context, WorkspaceSnapshotAuditEvent)
}

//go:generate go tool mockgen -source=workspace_snapshot.go -destination=mock_workspace_snapshot_auditor_test.go -package=workspace -typed

type workspaceSnapshotAuditorContextKey struct{}

func WithWorkspaceSnapshotAuditor(ctx context.Context, auditor WorkspaceSnapshotAuditor) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, workspaceSnapshotAuditorContextKey{}, auditor)
}

// WorkspaceSnapshotEntry binds a presented request path, its physical
// workspace identity, the captured body (for regular files), and durable
// evidence metadata from the same identity-bound read.
type WorkspaceSnapshotEntry struct {
	PresentedPath string
	Identity      CanonicalWorkspacePath
	Kind          WorkspaceSnapshotEntryKind
	Evidence      WorkspaceSnapshotEvidence
	Metadata      WorkspaceSnapshotMetadata
	body          []byte
}

// Body returns a defensive copy of the invocation-scoped snapshot body.
func (entry WorkspaceSnapshotEntry) Body() []byte {
	return append([]byte(nil), entry.body...)
}

// WorkspaceSnapshot is an invocation-bound rooted observation. Root is the
// physical workspace root used for every canonical identity in Entries.
type WorkspaceSnapshot struct {
	Purpose     WorkspaceSnapshotPurpose
	Root        string `json:"-"`
	Entries     []WorkspaceSnapshotEntry
	auditEvents []WorkspaceSnapshotAuditEvent
}

func (snapshot WorkspaceSnapshot) AuditEvents() []WorkspaceSnapshotAuditEvent {
	return append([]WorkspaceSnapshotAuditEvent(nil), snapshot.auditEvents...)
}

type WorkspaceSnapshotEntryProjection struct {
	CanonicalID string                     `json:"canonical_id"`
	Kind        WorkspaceSnapshotEntryKind `json:"kind"`
	Evidence    WorkspaceSnapshotEvidence  `json:"evidence,omitempty"`
	Metadata    WorkspaceSnapshotMetadata  `json:"metadata"`
}

type WorkspaceSnapshotProjection struct {
	Purpose WorkspaceSnapshotPurpose           `json:"purpose"`
	Entries []WorkspaceSnapshotEntryProjection `json:"entries,omitempty"`
}

func (snapshot WorkspaceSnapshot) DurableProjection() WorkspaceSnapshotProjection {
	projection := WorkspaceSnapshotProjection{Purpose: snapshot.Purpose}
	projection.Entries = make([]WorkspaceSnapshotEntryProjection, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		projection.Entries = append(projection.Entries, WorkspaceSnapshotEntryProjection{
			CanonicalID: entry.Identity.CanonicalID,
			Kind:        entry.Kind,
			Evidence:    entry.Evidence,
			Metadata:    entry.Metadata,
		})
	}
	return projection
}

func (snapshot WorkspaceSnapshot) MarshalJSON() ([]byte, error) {
	return json.Marshal(snapshot.DurableProjection())
}

// SnapshotWorkspace captures workspace source through the identity-bound read
// capability. It applies workspace read policy, retains the invocation's
// execution-session identity, reports protected-path metadata, and revalidates
// missing paths against an opened workspace root. It never reads outside the
// physical workspace root.
func (m *Manager) SnapshotWorkspace(ctx context.Context, request WorkspaceSnapshotRequest) (WorkspaceSnapshot, error) {
	if m == nil || !m.Enabled() {
		return WorkspaceSnapshot{}, fmt.Errorf("workspace root is not configured")
	}
	if !validWorkspaceSnapshotPurpose(request.Purpose) {
		return WorkspaceSnapshot{}, fmt.Errorf("unsupported workspace snapshot purpose %q", request.Purpose)
	}
	if len(request.Paths) == 0 {
		return WorkspaceSnapshot{}, fmt.Errorf("workspace snapshot requires at least one path")
	}
	if len(request.Paths) > WorkspaceSnapshotMaxPaths {
		return WorkspaceSnapshot{}, fmt.Errorf("workspace snapshot path count %d exceeds limit %d", len(request.Paths), WorkspaceSnapshotMaxPaths)
	}
	if request.Purpose == WorkspaceSnapshotNewFileWaiver && !request.AllowMissing {
		return WorkspaceSnapshot{}, fmt.Errorf("new-file waiver snapshots must allow and prove a missing path")
	}
	if request.AllowMissing && request.Purpose != WorkspaceSnapshotNewFileWaiver {
		return WorkspaceSnapshot{}, fmt.Errorf("workspace snapshot purpose %q cannot accept missing paths", request.Purpose)
	}
	bodyLimit, metadataOnly, err := m.workspaceSnapshotBodyPolicy(request)
	if err != nil {
		return WorkspaceSnapshot{}, err
	}

	ctx = m.snapshotExecutionSession(ctx)
	identity, _ := executionSessionIdentityFromContext(ctx)
	if err := ctx.Err(); err != nil {
		return WorkspaceSnapshot{}, err
	}

	snapshot := WorkspaceSnapshot{
		Purpose: request.Purpose,
		Root:    filepath.Clean(m.Root()),
		Entries: make([]WorkspaceSnapshotEntry, 0, len(request.Paths)),
	}
	collector := &workspaceSnapshotAuditCollector{}
	ctx = context.WithValue(ctx, workspaceSnapshotAuditCollectorContextKey{}, collector)
	remainingBodyBytes := bodyLimit
	for _, path := range request.Paths {
		entry, err := m.snapshotWorkspacePath(ctx, identity, request, path, remainingBodyBytes, metadataOnly)
		if err != nil {
			m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{Kind: WorkspaceSnapshotAuditError, Purpose: request.Purpose, Outcome: WorkspaceSnapshotOutcomeError, Reason: WorkspaceSnapshotReasonSnapshotFailed})
			snapshot.auditEvents = append([]WorkspaceSnapshotAuditEvent(nil), collector.events...)
			return snapshot, err
		}
		snapshot.Entries = append(snapshot.Entries, entry)
		remainingBodyBytes -= len(entry.body)
	}
	snapshot.auditEvents = append([]WorkspaceSnapshotAuditEvent(nil), collector.events...)

	return snapshot, nil
}

type workspaceSnapshotAuditCollectorContextKey struct{}

type workspaceSnapshotAuditCollector struct {
	events []WorkspaceSnapshotAuditEvent
}

func (m *Manager) workspaceSnapshotBodyPolicy(request WorkspaceSnapshotRequest) (int, bool, error) {
	metadataOnly := request.Purpose == WorkspaceSnapshotCitation || request.Purpose == WorkspaceSnapshotNewFileWaiver
	limit := workspaceSnapshotDefaultBodyByteLimit
	if request.Purpose != WorkspaceSnapshotSourceWindow && m != nil && m.cfg.MaxFileBytes > 0 && m.cfg.MaxFileBytes < limit {
		limit = m.cfg.MaxFileBytes
	}
	if request.MaxBodyBytes < 0 || request.MaxBodyBytes > limit {
		return 0, false, fmt.Errorf("workspace snapshot max body bytes %d exceeds purpose limit %d", request.MaxBodyBytes, limit)
	}
	if request.MaxBodyBytes > 0 {
		limit = request.MaxBodyBytes
	}
	if metadataOnly {
		limit = 0
	}
	return limit, metadataOnly, nil
}

func validWorkspaceSnapshotPurpose(purpose WorkspaceSnapshotPurpose) bool {
	switch purpose {
	case WorkspaceSnapshotSubmitPlanAnchor,
		WorkspaceSnapshotReviewRepair,
		WorkspaceSnapshotCitation,
		WorkspaceSnapshotCompletionSnippet,
		WorkspaceSnapshotNewFileWaiver,
		WorkspaceSnapshotSourceWindow:
		return true
	default:
		return false
	}
}

func (m *Manager) snapshotWorkspacePath(ctx context.Context, session executionSessionIdentity, request WorkspaceSnapshotRequest, path string, remainingBodyBytes int, metadataOnly bool) (WorkspaceSnapshotEntry, error) {
	presented := strings.TrimSpace(path)
	if presented == "" {
		return WorkspaceSnapshotEntry{}, fmt.Errorf("workspace snapshot path is required")
	}
	canonical, err := m.CanonicalizeWorkspacePath(presented, request.Workdir)
	if err != nil {
		return WorkspaceSnapshotEntry{}, fmt.Errorf("canonicalize workspace snapshot path %q: %w", presented, err)
	}
	policySubject, err := m.workspaceSnapshotPolicySubject(presented, request.Workdir)
	if err != nil {
		return WorkspaceSnapshotEntry{}, fmt.Errorf("bind workspace snapshot policy subject %q: %w", presented, err)
	}
	decision := m.workspaceSnapshotPolicyDecision(presented, request.Workdir, policySubject, canonical.AbsolutePath)
	protected := m.workspaceSnapshotProtected(canonical.AbsolutePath)
	authorization, approval, err := m.authorizeWorkspaceSnapshotRead(ctx, session, request.Purpose, canonical.CanonicalID, decision, protected)
	if err != nil {
		m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
			Kind: WorkspaceSnapshotAuditAuthority, Purpose: request.Purpose, CanonicalID: canonical.CanonicalID,
			Outcome: WorkspaceSnapshotOutcomeDenied, Reason: approval.reason, PolicyDecision: decision, Protected: protected,
			ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
		})
		return WorkspaceSnapshotEntry{}, err
	}
	if decision == toolpolicy.Ask {
		m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
			Kind: WorkspaceSnapshotAuditAuthority, Purpose: request.Purpose, CanonicalID: canonical.CanonicalID,
			Outcome: WorkspaceSnapshotOutcomeApproved, Reason: approval.reason, PolicyDecision: decision, Protected: protected,
			ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
		})
	}
	metadata := WorkspaceSnapshotMetadata{
		PolicySubject:  policySubject,
		PolicyDecision: decision,
		Protected:      protected,
	}

	capability, err := m.pathAccess.openReadCapability(ctx, session, presented, request.Workdir)
	if err != nil {
		if request.AllowMissing && os.IsNotExist(err) {
			if err := m.validateMissingWorkspaceSnapshotPath(ctx, presented, request.Workdir, canonical); err != nil {
				return WorkspaceSnapshotEntry{}, fmt.Errorf("validate missing workspace snapshot path %q: %w", presented, err)
			}
			currentDecision := m.workspaceSnapshotPolicyDecision(presented, request.Workdir, policySubject, canonical.AbsolutePath)
			currentProtected := m.workspaceSnapshotProtected(canonical.AbsolutePath)
			if currentDecision != decision || currentProtected != protected {
				return WorkspaceSnapshotEntry{}, fmt.Errorf("workspace snapshot authority changed while validating absence")
			}
			if err := authorization.validate(session, request.Purpose, canonical.CanonicalID, currentDecision); err != nil {
				return WorkspaceSnapshotEntry{}, err
			}
			m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
				Kind: WorkspaceSnapshotAuditRead, Purpose: request.Purpose, CanonicalID: canonical.CanonicalID,
				Outcome: WorkspaceSnapshotOutcomeMissing, Reason: WorkspaceSnapshotReasonRootedAbsence, PolicyDecision: decision, Protected: protected,
				ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
			})
			return WorkspaceSnapshotEntry{
				PresentedPath: presented,
				Identity:      canonical,
				Kind:          WorkspaceSnapshotMissing,
				Metadata:      metadata,
			}, nil
		}
		return WorkspaceSnapshotEntry{}, fmt.Errorf("open workspace snapshot path %q: %w", presented, err)
	}
	defer capability.Close()
	openedCanonical, err := m.CanonicalizeWorkspacePath(capability.authorization.canonical, "")
	if err != nil || openedCanonical.CanonicalID != canonical.CanonicalID {
		return WorkspaceSnapshotEntry{}, fmt.Errorf("workspace snapshot identity changed while opening")
	}
	currentDecision := m.workspaceSnapshotPolicyDecision(presented, request.Workdir, policySubject, openedCanonical.AbsolutePath)
	currentProtected := m.workspaceSnapshotProtected(openedCanonical.AbsolutePath)
	if currentDecision != decision || currentProtected != protected {
		return WorkspaceSnapshotEntry{}, fmt.Errorf("workspace snapshot authority changed while opening")
	}
	if err := authorization.validate(session, request.Purpose, openedCanonical.CanonicalID, currentDecision); err != nil {
		return WorkspaceSnapshotEntry{}, err
	}
	m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
		Kind: WorkspaceSnapshotAuditAuthority, Purpose: request.Purpose, CanonicalID: openedCanonical.CanonicalID,
		Outcome: WorkspaceSnapshotOutcomeAllowed, Reason: WorkspaceSnapshotReasonOpenedIdentity, PolicyDecision: decision, Protected: protected,
		ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
	})

	opened := capability.Info()
	entry := WorkspaceSnapshotEntry{
		PresentedPath: presented,
		Identity:      openedCanonical,
		Metadata:      metadata,
	}
	if opened.IsDir() {
		return WorkspaceSnapshotEntry{}, fmt.Errorf("workspace snapshot purpose %q requires a regular file", request.Purpose)
	}
	if !opened.Mode().IsRegular() {
		return WorkspaceSnapshotEntry{}, fmt.Errorf("workspace snapshot path %q must be a regular file or directory", presented)
	}
	if request.Purpose == WorkspaceSnapshotNewFileWaiver {
		return WorkspaceSnapshotEntry{}, fmt.Errorf("new-file waiver path %q already exists", presented)
	}
	if metadataOnly {
		entry.Kind = WorkspaceSnapshotFile
		entry.Evidence.SizeBytes = opened.Size()
		if err := capability.Validate(); err != nil {
			return WorkspaceSnapshotEntry{}, err
		}
		if m.workspaceSnapshotPolicyDecision(presented, request.Workdir, policySubject, openedCanonical.AbsolutePath) != decision || m.workspaceSnapshotProtected(openedCanonical.AbsolutePath) != protected {
			return WorkspaceSnapshotEntry{}, fmt.Errorf("workspace snapshot authority changed while validating metadata")
		}
		if err := authorization.validate(session, request.Purpose, openedCanonical.CanonicalID, decision); err != nil {
			return WorkspaceSnapshotEntry{}, err
		}
		m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
			Kind: WorkspaceSnapshotAuditRead, Purpose: request.Purpose, CanonicalID: openedCanonical.CanonicalID,
			Outcome: WorkspaceSnapshotOutcomeMetadata, Reason: WorkspaceSnapshotReasonExistenceOnly, PolicyDecision: decision, Protected: protected,
			ApprovalPresent: approval.present, ApprovalMatched: approval.matched, SizeBytes: opened.Size(),
		})
		return entry, nil
	}
	body, err := capability.ReadAllLimit(ctx, remainingBodyBytes)
	if err != nil {
		if errors.Is(err, errWorkspaceReadLimitExceeded) {
			return WorkspaceSnapshotEntry{}, fmt.Errorf("workspace snapshot body for %q exceeds remaining byte limit %d", presented, remainingBodyBytes)
		}
		return WorkspaceSnapshotEntry{}, fmt.Errorf("read workspace snapshot path %q: %w", presented, err)
	}
	if m.workspaceSnapshotPolicyDecision(presented, request.Workdir, policySubject, openedCanonical.AbsolutePath) != decision || m.workspaceSnapshotProtected(openedCanonical.AbsolutePath) != protected {
		return WorkspaceSnapshotEntry{}, fmt.Errorf("workspace snapshot authority changed while reading")
	}
	if err := authorization.validate(session, request.Purpose, openedCanonical.CanonicalID, decision); err != nil {
		return WorkspaceSnapshotEntry{}, err
	}
	digest := sha256.Sum256(body)
	entry.Kind = WorkspaceSnapshotFile
	entry.body = append([]byte(nil), body...)
	entry.Evidence = WorkspaceSnapshotEvidence{
		DigestSHA256: fmt.Sprintf("sha256:%x", digest),
		SizeBytes:    int64(len(body)),
	}
	m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
		Kind: WorkspaceSnapshotAuditRead, Purpose: request.Purpose, CanonicalID: openedCanonical.CanonicalID,
		Outcome: WorkspaceSnapshotOutcomeBody, Reason: WorkspaceSnapshotReasonIdentityBoundRead, PolicyDecision: decision, Protected: protected,
		ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
		SizeBytes: int64(len(body)), DigestSHA256: entry.Evidence.DigestSHA256,
		StartLine: sourceSnapshotStartLine(body), EndLine: len(textutil.SplitWorkspaceFileLines(body)), TotalLines: len(textutil.SplitWorkspaceFileLines(body)),
	})
	return entry, nil
}

func sourceSnapshotStartLine(body []byte) int {
	if len(textutil.SplitWorkspaceFileLines(body)) == 0 {
		return 0
	}
	return 1
}

func (m *Manager) workspaceSnapshotPolicyDecision(presented string, workdir string, policySubject string, canonicalAbsolute string) toolpolicy.Decision {
	requests := []ToolRequest{
		{Tool: ToolNameReadFile, Path: presented, Workdir: workdir},
	}
	if strings.TrimSpace(policySubject) != "" {
		requests = append(requests, ToolRequest{Tool: ToolNameReadFile, Path: policySubject})
	}
	requests = append(requests, ToolRequest{Tool: ToolNameReadFile, Path: canonicalAbsolute})
	decision := toolpolicy.None
	for _, request := range requests {
		candidate := m.EvaluateToolRule(request)
		if permissionDecisionPriority(candidate) > permissionDecisionPriority(decision) {
			decision = candidate
		}
	}
	return decision
}

func (m *Manager) workspaceSnapshotPolicySubject(presented string, workdir string) (string, error) {
	base, err := workspaceMutationBasePath(m.Root(), m.cfg.CWD, workdir)
	if err != nil {
		return "", err
	}
	candidate := filepath.Clean(strings.TrimSpace(presented))
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(base, candidate)
	}
	subject, within := workspaceRelativePath(filepath.Clean(m.Root()), candidate)
	if !within || subject == "." {
		return "", fmt.Errorf("policy subject must be workspace-relative")
	}
	return filepath.ToSlash(filepath.Clean(subject)), nil
}

func (m *Manager) workspaceSnapshotProtected(canonicalAbsolute string) bool {
	return m.protectedWritePathMatched(ToolRequest{Tool: ToolNameWriteFile, Path: canonicalAbsolute})
}

type workspaceSnapshotReadAuthorization struct {
	sessionKey executionSessionGrantKey
	purpose    WorkspaceSnapshotPurpose
	canonical  string
	decision   toolpolicy.Decision
}

type workspaceSnapshotApprovalOutcome struct {
	present bool
	matched bool
	reason  WorkspaceSnapshotAuditReason
}

func (m *Manager) authorizeWorkspaceSnapshotRead(
	ctx context.Context,
	session executionSessionIdentity,
	purpose WorkspaceSnapshotPurpose,
	canonicalID string,
	decision toolpolicy.Decision,
	protected bool,
) (workspaceSnapshotReadAuthorization, workspaceSnapshotApprovalOutcome, error) {
	authorization := workspaceSnapshotReadAuthorization{
		sessionKey: session.grantKey(),
		purpose:    purpose,
		canonical:  canonicalID,
		decision:   decision,
	}
	if decision == toolpolicy.Deny {
		return workspaceSnapshotReadAuthorization{}, workspaceSnapshotApprovalOutcome{reason: WorkspaceSnapshotReasonPolicyDenied}, fmt.Errorf("workspace policy denies snapshot read of %q", canonicalID)
	}
	if decision != toolpolicy.Ask {
		return authorization, workspaceSnapshotApprovalOutcome{reason: WorkspaceSnapshotReasonAuthorized}, nil
	}

	binding, present := ctx.Value(workspaceSnapshotReadApproverContextKey{}).(boundWorkspaceSnapshotReadApprover)
	outcome := workspaceSnapshotApprovalOutcome{present: present, reason: WorkspaceSnapshotReasonApprovalAbsent}
	if !present || binding.approver == nil {
		return workspaceSnapshotReadAuthorization{}, outcome, fmt.Errorf("workspace snapshot read approval requires an invocation-bound approver")
	}
	if session.explicit && session.sessionID == "" {
		outcome.reason = WorkspaceSnapshotReasonApprovalUnattributed
		return workspaceSnapshotReadAuthorization{}, outcome, fmt.Errorf("workspace snapshot read approval requires a non-empty execution session id")
	}
	if !binding.sessionBound || binding.session.grantKey() != session.grantKey() {
		outcome.reason = WorkspaceSnapshotReasonApprovalWrongSession
		return workspaceSnapshotReadAuthorization{}, outcome, fmt.Errorf("workspace snapshot read approver belongs to another execution session")
	}
	if err := ctx.Err(); err != nil {
		outcome.reason = WorkspaceSnapshotReasonApprovalCanceled
		return workspaceSnapshotReadAuthorization{}, outcome, fmt.Errorf("workspace snapshot read approval canceled: %w", err)
	}
	approved, err := binding.approver.ApproveWorkspaceSnapshotRead(ctx, WorkspaceSnapshotReadApprovalRequest{
		SessionID:      session.sessionID,
		Purpose:        purpose,
		CanonicalID:    canonicalID,
		PolicyDecision: decision,
		Protected:      protected,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		outcome.reason = WorkspaceSnapshotReasonApprovalCanceled
		return workspaceSnapshotReadAuthorization{}, outcome, fmt.Errorf("workspace snapshot read approval canceled: %w", ctxErr)
	}
	if err != nil {
		outcome.reason = WorkspaceSnapshotReasonApprovalError
		return workspaceSnapshotReadAuthorization{}, outcome, fmt.Errorf("workspace snapshot read approval failed: %w", err)
	}
	if !approved.Approved {
		outcome.reason = WorkspaceSnapshotReasonApprovalDenied
		return workspaceSnapshotReadAuthorization{}, outcome, fmt.Errorf("workspace snapshot read approval denied for %q", canonicalID)
	}
	outcome.matched = true
	outcome.reason = WorkspaceSnapshotReasonApprovalGranted
	return authorization, outcome, nil
}

func (authorization workspaceSnapshotReadAuthorization) validate(
	session executionSessionIdentity,
	purpose WorkspaceSnapshotPurpose,
	canonicalID string,
	decision toolpolicy.Decision,
) error {
	if authorization.sessionKey != session.grantKey() ||
		authorization.purpose != purpose ||
		authorization.canonical != canonicalID ||
		authorization.decision != decision {
		return fmt.Errorf("workspace snapshot read authorization no longer matches the opened identity")
	}
	return nil
}

func (m *Manager) recordWorkspaceSnapshotAudit(ctx context.Context, event WorkspaceSnapshotAuditEvent) {
	if ctx == nil {
		return
	}
	identity, _ := executionSessionIdentityFromContext(ctx)
	switch {
	case !identity.explicit:
		event.ExecutionIdentityState = WorkspaceSnapshotIdentityFallback
	case identity.sessionID == "":
		event.ExecutionIdentityState = WorkspaceSnapshotIdentityExplicitEmpty
	default:
		event.ExecutionIdentityState = WorkspaceSnapshotIdentityExplicitNonempty
	}
	if collector, _ := ctx.Value(workspaceSnapshotAuditCollectorContextKey{}).(*workspaceSnapshotAuditCollector); collector != nil {
		collector.events = append(collector.events, event)
	}
	auditor, _ := ctx.Value(workspaceSnapshotAuditorContextKey{}).(WorkspaceSnapshotAuditor)
	if auditor != nil {
		auditor.RecordWorkspaceSnapshot(ctx, event)
	}
}

func (m *Manager) validateMissingWorkspaceSnapshotPath(ctx context.Context, presented string, workdir string, expected CanonicalWorkspacePath) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rootPath := filepath.Clean(m.Root())
	before, err := os.Lstat(rootPath)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return fmt.Errorf("workspace snapshot root must be a directory and must not be a symlink")
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, openErr := root.Stat(".")
	after, afterErr := os.Lstat(rootPath)
	if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		if openErr != nil {
			return openErr
		}
		if afterErr != nil {
			return afterErr
		}
		return fmt.Errorf("workspace snapshot root changed while opening")
	}

	relative := filepath.FromSlash(expected.CanonicalID)
	if _, err := root.Lstat(relative); !os.IsNotExist(err) {
		if err == nil {
			return fmt.Errorf("path appeared while validating absence")
		}
		return err
	}
	if checkpoint := workspaceReadCheckpointFromContext(ctx); checkpoint != nil {
		checkpoint(workspaceReadCheckpoint{Stage: workspaceReadBeforeOpen, Path: expected.AbsolutePath})
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	current, err := m.CanonicalizeWorkspacePath(presented, workdir)
	if err != nil {
		return err
	}
	if current.CanonicalID != expected.CanonicalID {
		return fmt.Errorf("workspace snapshot path identity changed while validating absence")
	}
	if _, err := root.Lstat(relative); !os.IsNotExist(err) {
		if err == nil {
			return fmt.Errorf("path appeared while validating absence")
		}
		return err
	}
	currentRoot, err := os.Lstat(rootPath)
	if err != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !currentRoot.IsDir() || !os.SameFile(opened, currentRoot) {
		if err != nil {
			return err
		}
		return fmt.Errorf("workspace snapshot root changed while validating absence")
	}
	return nil
}
