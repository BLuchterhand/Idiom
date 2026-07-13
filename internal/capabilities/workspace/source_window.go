package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"idiom/internal/contracts/toolfacts"
	"idiom/internal/contracts/toolpolicy"
	"idiom/internal/platform/textutil"
)

type WorkspaceSourceWindow struct {
	Evidence toolfacts.SourceWindowEvidence
	Content  string `json:"-"`
}

func (window WorkspaceSourceWindow) DurableProjection() toolfacts.SourceWindowEvidence {
	return window.Evidence
}

func (window WorkspaceSourceWindow) MarshalJSON() ([]byte, error) {
	return json.Marshal(window.DurableProjection())
}

func (m *Manager) sourceWindowFromReadCapability(capability *workspaceReadCapability, fullBody []byte, start int, end int, truncated bool, mutationEpoch uint64) (WorkspaceSourceWindow, error) {
	if m == nil || capability == nil || mutationEpoch == 0 || mutationEpoch != m.MutationEpoch() {
		return WorkspaceSourceWindow{}, fmt.Errorf("workspace mutation generation changed while binding source evidence")
	}
	identity, err := m.CanonicalizeWorkspacePath(capability.authorization.canonical, "")
	if err != nil {
		return WorkspaceSourceWindow{}, err
	}
	authority, err := m.sourceWindowAuthority(capability.authorization.candidate, "", identity)
	if err != nil {
		return WorkspaceSourceWindow{}, err
	}
	window, err := workspaceSourceWindow(identity, authority, fullBody, start, end, truncated, mutationEpoch)
	if err != nil {
		return WorkspaceSourceWindow{}, err
	}
	if err := capability.Validate(); err != nil {
		return WorkspaceSourceWindow{}, err
	}
	if mutationEpoch != m.MutationEpoch() {
		return WorkspaceSourceWindow{}, fmt.Errorf("workspace mutation generation changed while binding source evidence")
	}
	return window, nil
}

func workspaceSourceWindow(identity CanonicalWorkspacePath, authority toolfacts.SourceWindowAuthorityEvidence, fullBody []byte, requestedStart int, requestedEnd int, truncated bool, mutationEpoch uint64) (WorkspaceSourceWindow, error) {
	total := len(textutil.SplitWorkspaceFileLines(fullBody))
	if total == 0 {
		if len(fullBody) != 0 {
			return WorkspaceSourceWindow{}, fmt.Errorf("empty source line projection has non-empty body")
		}
		return NewWorkspaceSourceWindow(toolfacts.SourceWindowEvidence{
			CanonicalID:    identity.CanonicalID,
			Authority:      authority,
			RevisionSHA256: toolfacts.SourceWindowSHA256(fullBody),
			WindowSHA256:   toolfacts.SourceWindowSHA256(nil),
			MutationEpoch:  mutationEpoch,
		}, "")
	}

	start := requestedStart
	end := requestedEnd
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > total {
		end = total
	}
	if start > total || end < start {
		return WorkspaceSourceWindow{}, fmt.Errorf("source window %d-%d is outside a %d-line file", requestedStart, requestedEnd, total)
	}

	window := sourceWindowBody(fullBody, start, end)
	if len(window) > 0 && window[len(window)-1] == '\n' {
		window = window[:len(window)-1]
	}
	return NewWorkspaceSourceWindow(toolfacts.SourceWindowEvidence{
		CanonicalID:    identity.CanonicalID,
		Authority:      authority,
		StartLine:      start,
		EndLine:        end,
		TotalLines:     total,
		Truncated:      truncated,
		RevisionSHA256: toolfacts.SourceWindowSHA256(fullBody),
		WindowSHA256:   toolfacts.SourceWindowSHA256(window),
		MutationEpoch:  mutationEpoch,
	}, string(window))
}

func (m *Manager) sourceWindowAuthority(presented string, workdir string, identity CanonicalWorkspacePath) (toolfacts.SourceWindowAuthorityEvidence, error) {
	if m == nil {
		return toolfacts.SourceWindowAuthorityEvidence{}, fmt.Errorf("workspace manager is not configured")
	}
	subject, err := m.workspaceSnapshotPolicySubject(presented, workdir)
	if err != nil {
		return toolfacts.SourceWindowAuthorityEvidence{}, err
	}
	return toolfacts.SourceWindowAuthorityEvidence{
		PolicySubject:  subject,
		PolicyDecision: m.workspaceSnapshotPolicyDecision(subject, "", subject, identity.AbsolutePath),
	}, nil
}

// NewWorkspaceSourceWindow is the sole attachment constructor. It validates
// canonical durable metadata against the exact exposed body before that body
// can enter an ephemeral ToolResult attachment.
func NewWorkspaceSourceWindow(evidence toolfacts.SourceWindowEvidence, content string) (WorkspaceSourceWindow, error) {
	normalized := toolfacts.NormalizeSourceWindows([]toolfacts.SourceWindowEvidence{evidence})
	if len(normalized) != 1 || normalized[0] != evidence {
		return WorkspaceSourceWindow{}, fmt.Errorf("source window evidence is not canonical and complete")
	}
	if evidence.WindowSHA256 != toolfacts.SourceWindowSHA256([]byte(content)) {
		return WorkspaceSourceWindow{}, fmt.Errorf("source window body digest does not match durable evidence")
	}
	if strings.HasSuffix(content, "\n") {
		return WorkspaceSourceWindow{}, fmt.Errorf("source window body must not contain an unexposed trailing line terminator")
	}
	if evidence.TotalLines == 0 {
		if content != "" {
			return WorkspaceSourceWindow{}, fmt.Errorf("empty-file source window must have an empty body")
		}
		return WorkspaceSourceWindow{Evidence: evidence}, nil
	}
	contentLines := strings.Count(content, "\n") + 1
	if contentLines != evidence.EndLine-evidence.StartLine+1 {
		return WorkspaceSourceWindow{}, fmt.Errorf("source window body line count does not match its inclusive range")
	}
	return WorkspaceSourceWindow{Evidence: evidence, Content: content}, nil
}

func (m *Manager) sourceWindowFromLineRead(capability *workspaceReadCapability, read workspaceReadLineWindow, truncated bool, mutationEpoch uint64) (WorkspaceSourceWindow, error) {
	if m == nil || capability == nil || mutationEpoch == 0 || mutationEpoch != m.MutationEpoch() {
		return WorkspaceSourceWindow{}, fmt.Errorf("workspace mutation generation changed while binding source evidence")
	}
	identity, err := m.CanonicalizeWorkspacePath(capability.authorization.canonical, "")
	if err != nil {
		return WorkspaceSourceWindow{}, err
	}
	authority, err := m.sourceWindowAuthority(capability.authorization.candidate, "", identity)
	if err != nil {
		return WorkspaceSourceWindow{}, err
	}
	window, err := NewWorkspaceSourceWindow(toolfacts.SourceWindowEvidence{
		CanonicalID:    identity.CanonicalID,
		Authority:      authority,
		StartLine:      read.startLine,
		EndLine:        read.endLine,
		TotalLines:     read.totalLines,
		Truncated:      truncated,
		RevisionSHA256: fmt.Sprintf("sha256:%x", read.revisionSHA256),
		WindowSHA256:   fmt.Sprintf("sha256:%x", read.windowSHA256),
		MutationEpoch:  mutationEpoch,
	}, string(read.body))
	if err != nil {
		return WorkspaceSourceWindow{}, err
	}
	if err := capability.Validate(); err != nil {
		return WorkspaceSourceWindow{}, err
	}
	if mutationEpoch != m.MutationEpoch() {
		return WorkspaceSourceWindow{}, fmt.Errorf("workspace mutation generation changed while binding source evidence")
	}
	return window, nil
}

func sourceWindowBody(fullBody []byte, start int, end int) []byte {
	if len(fullBody) == 0 || start <= 0 || end < start {
		return nil
	}

	line := 1
	startOffset := 0
	endOffset := len(fullBody)
	foundStart := start == 1
	for index, value := range fullBody {
		if value != '\n' {
			continue
		}
		if line == end {
			endOffset = index + 1
			break
		}
		line++
		if line == start {
			startOffset = index + 1
			foundStart = true
		}
	}
	if !foundStart || startOffset > endOffset {
		return nil
	}
	return append([]byte(nil), fullBody[startOffset:endOffset]...)
}

func attachWorkspaceSourceWindows(result *ToolResult, windows ...WorkspaceSourceWindow) {
	if result == nil || len(windows) == 0 {
		return
	}
	allWindows := append(cloneWorkspaceSourceWindows(result.sourceWindows), windows...)
	evidence := make([]toolfacts.SourceWindowEvidence, 0, len(allWindows))
	for _, window := range allWindows {
		validated, err := NewWorkspaceSourceWindow(window.Evidence, window.Content)
		if err != nil || validated != window {
			return
		}
		evidence = append(evidence, window.Evidence)
	}
	normalized := toolfacts.NormalizeSourceWindows(evidence)
	if len(normalized) == 0 {
		return
	}
	attachments := make([]WorkspaceSourceWindow, 0, len(normalized))
	for _, fact := range normalized {
		for _, window := range allWindows {
			if !sameSourceWindowIdentity(window.Evidence, fact) {
				continue
			}
			window.Evidence = fact
			attachments = append(attachments, window)
			break
		}
	}
	if len(attachments) != len(normalized) {
		return
	}
	if result.Facts == nil {
		result.Facts = &toolfacts.ToolResultFacts{}
	} else {
		result.Facts = result.Facts.Clone()
	}
	result.Facts.SourceWindows = append([]toolfacts.SourceWindowEvidence(nil), normalized...)
	result.sourceWindows = attachments
}

func sameSourceWindowIdentity(left toolfacts.SourceWindowEvidence, right toolfacts.SourceWindowEvidence) bool {
	left.Truncated = false
	right.Truncated = false
	return left == right
}

func cloneWorkspaceSourceWindows(windows []WorkspaceSourceWindow) []WorkspaceSourceWindow {
	if len(windows) == 0 {
		return nil
	}
	cloned := make([]WorkspaceSourceWindow, len(windows))
	copy(cloned, windows)
	return cloned
}

// SourceWindowCurrent re-opens the canonical source through the rooted
// snapshot boundary and verifies both the mutation generation and file/window
// digests. A false result is ordinary stale evidence, not an authorization to
// inspect the host filesystem directly.
func (m *Manager) SourceWindowCurrent(ctx context.Context, evidence toolfacts.SourceWindowEvidence) (bool, error) {
	if m == nil || evidence.MutationEpoch == 0 || evidence.MutationEpoch != m.MutationEpoch() {
		return false, nil
	}
	normalized := toolfacts.NormalizeSourceWindows([]toolfacts.SourceWindowEvidence{evidence})
	if len(normalized) != 1 || normalized[0] != evidence {
		return false, fmt.Errorf("source window evidence is not canonical and complete")
	}
	ctx = m.snapshotExecutionSession(ctx)
	session, _ := executionSessionIdentityFromContext(ctx)
	rootWorkdir := m.Root()
	canonical, err := m.CanonicalizeWorkspacePath(evidence.CanonicalID, rootWorkdir)
	if err != nil {
		return false, err
	}
	if canonical.CanonicalID != evidence.CanonicalID {
		return false, fmt.Errorf("workspace source canonical identity changed")
	}
	presentedCanonical, err := m.CanonicalizeWorkspacePath(evidence.Authority.PolicySubject, rootWorkdir)
	if err != nil {
		return false, fmt.Errorf("recanonicalize source window policy subject: %w", err)
	}
	if presentedCanonical.CanonicalID != evidence.CanonicalID {
		return false, fmt.Errorf("workspace source policy subject no longer resolves to the recorded canonical identity")
	}
	decision := m.sourceWindowPolicyDecision(evidence, canonical.AbsolutePath)
	protected := m.workspaceSnapshotProtected(canonical.AbsolutePath)
	authorization, approval, err := m.authorizeWorkspaceSnapshotRead(ctx, session, WorkspaceSnapshotSourceWindow, canonical.CanonicalID, decision, protected)
	if err != nil {
		m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
			Kind: WorkspaceSnapshotAuditAuthority, Purpose: WorkspaceSnapshotSourceWindow, CanonicalID: canonical.CanonicalID,
			Outcome: WorkspaceSnapshotOutcomeDenied, Reason: approval.reason, PolicyDecision: decision, Protected: protected,
			ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
		})
		return false, err
	}
	if decision == toolpolicy.Ask {
		m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
			Kind: WorkspaceSnapshotAuditAuthority, Purpose: WorkspaceSnapshotSourceWindow, CanonicalID: canonical.CanonicalID,
			Outcome: WorkspaceSnapshotOutcomeApproved, Reason: approval.reason, PolicyDecision: decision, Protected: protected,
			ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
		})
	}
	capability, err := m.pathAccess.openReadCapability(ctx, session, evidence.Authority.PolicySubject, rootWorkdir)
	if err != nil {
		m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
			Kind: WorkspaceSnapshotAuditError, Purpose: WorkspaceSnapshotSourceWindow, CanonicalID: canonical.CanonicalID,
			Outcome: WorkspaceSnapshotOutcomeError, Reason: WorkspaceSnapshotReasonOpenFailed, PolicyDecision: decision, Protected: protected,
			ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
		})
		return false, err
	}
	defer capability.Close()
	openedCanonical, err := m.CanonicalizeWorkspacePath(capability.authorization.canonical, "")
	if err != nil || openedCanonical.CanonicalID != evidence.CanonicalID {
		return false, fmt.Errorf("workspace source identity changed while opening")
	}
	openedPresentation, err := m.CanonicalizeWorkspacePath(evidence.Authority.PolicySubject, rootWorkdir)
	if err != nil || openedPresentation.CanonicalID != evidence.CanonicalID {
		return false, fmt.Errorf("workspace source policy subject changed while opening")
	}
	currentDecision := m.sourceWindowPolicyDecision(evidence, openedCanonical.AbsolutePath)
	if currentDecision != decision || m.workspaceSnapshotProtected(openedCanonical.AbsolutePath) != protected {
		return false, fmt.Errorf("workspace source authority changed while opening")
	}
	if err := authorization.validate(session, WorkspaceSnapshotSourceWindow, openedCanonical.CanonicalID, currentDecision); err != nil {
		return false, err
	}
	m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
		Kind: WorkspaceSnapshotAuditAuthority, Purpose: WorkspaceSnapshotSourceWindow, CanonicalID: openedCanonical.CanonicalID,
		Outcome: WorkspaceSnapshotOutcomeAllowed, Reason: WorkspaceSnapshotReasonOpenedIdentity, PolicyDecision: decision, Protected: protected,
		ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
	})
	read, err := capability.InspectLineWindow(ctx, evidence.StartLine, evidence.EndLine)
	if err != nil {
		m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
			Kind: WorkspaceSnapshotAuditError, Purpose: WorkspaceSnapshotSourceWindow, CanonicalID: openedCanonical.CanonicalID,
			Outcome: WorkspaceSnapshotOutcomeError, Reason: WorkspaceSnapshotReasonFreshnessScanFailed, PolicyDecision: decision, Protected: protected,
			ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
			StartLine: evidence.StartLine, EndLine: evidence.EndLine, TotalLines: evidence.TotalLines,
		})
		return false, err
	}
	currentPresentation, err := m.CanonicalizeWorkspacePath(evidence.Authority.PolicySubject, rootWorkdir)
	if err != nil || currentPresentation.CanonicalID != evidence.CanonicalID {
		return false, fmt.Errorf("workspace source policy subject changed while reading")
	}
	if evidence.MutationEpoch != m.MutationEpoch() {
		return false, nil
	}
	current := toolfacts.SourceWindowEvidence{
		CanonicalID: openedCanonical.CanonicalID, Authority: evidence.Authority, StartLine: read.startLine, EndLine: read.endLine, TotalLines: read.totalLines,
		Truncated: evidence.Truncated, RevisionSHA256: fmt.Sprintf("sha256:%x", read.revisionSHA256),
		WindowSHA256: fmt.Sprintf("sha256:%x", read.windowSHA256), MutationEpoch: evidence.MutationEpoch,
	}
	isCurrent := current == evidence
	outcome := WorkspaceSnapshotOutcomeStale
	reason := WorkspaceSnapshotReasonDigestRangeMismatch
	if isCurrent {
		outcome = WorkspaceSnapshotOutcomeCurrent
		reason = WorkspaceSnapshotReasonDigestAndRangeMatched
	}
	m.recordWorkspaceSnapshotAudit(ctx, WorkspaceSnapshotAuditEvent{
		Kind: WorkspaceSnapshotAuditRead, Purpose: WorkspaceSnapshotSourceWindow, CanonicalID: openedCanonical.CanonicalID,
		Outcome: outcome, Reason: reason, PolicyDecision: decision, Protected: protected,
		ApprovalPresent: approval.present, ApprovalMatched: approval.matched,
		DigestSHA256: current.RevisionSHA256, WindowSHA256: current.WindowSHA256,
		StartLine: current.StartLine, EndLine: current.EndLine, TotalLines: current.TotalLines,
	})
	return isCurrent, nil
}

func (m *Manager) sourceWindowPolicyDecision(evidence toolfacts.SourceWindowEvidence, canonicalAbsolute string) toolpolicy.Decision {
	current := m.workspaceSnapshotPolicyDecision(evidence.Authority.PolicySubject, "", evidence.Authority.PolicySubject, canonicalAbsolute)
	if permissionDecisionPriority(evidence.Authority.PolicyDecision) > permissionDecisionPriority(current) {
		return evidence.Authority.PolicyDecision
	}
	return current
}
