package workspace

import (
	"fmt"

	"idiom/internal/contracts/toolfacts"
)

// attachDiagnosticLogReadFacts binds metadata-only log evidence to the same
// live capability that produced the result. Trusted or granted paths outside
// the workspace deliberately receive no workspace-relative durable fact.
func (m *Manager) attachDiagnosticLogReadFacts(
	result *ToolResult,
	capability *workspaceReadCapability,
	startLine int,
	endLine int,
	totalLines int,
	truncated bool,
) error {
	if result == nil {
		return fmt.Errorf("workspace diagnostic-log result is not configured")
	}
	if m == nil || capability == nil {
		return fmt.Errorf("workspace diagnostic-log read capability is not configured")
	}
	if capability.authorization.authorityKind != workspaceReadAuthorityWorkspace {
		return nil
	}
	if classifyReadArtifact(m.pathAccess.DisplayPath(capability.authorization.candidate)) != readArtifactLog {
		return fmt.Errorf("workspace diagnostic-log evidence requires a log artifact")
	}

	identity, err := m.CanonicalizeWorkspacePath(capability.authorization.canonical, "")
	if err != nil {
		return err
	}
	policySubject, err := m.workspaceSnapshotPolicySubject(capability.authorization.candidate, "")
	if err != nil {
		return err
	}
	policyDecision := m.workspaceSnapshotPolicyDecision(
		capability.authorization.candidate,
		"",
		policySubject,
		identity.AbsolutePath,
	)
	if err := capability.Validate(); err != nil {
		return err
	}

	diagnosticFacts := toolfacts.NewDiagnosticLogReadFacts(toolfacts.DiagnosticLogReadEvidence{
		CanonicalID:    identity.CanonicalID,
		PolicySubject:  policySubject,
		PolicyDecision: policyDecision,
		StartLine:      startLine,
		EndLine:        endLine,
		TotalLines:     totalLines,
		Truncated:      truncated,
	})
	if diagnosticFacts == nil {
		return fmt.Errorf("workspace diagnostic-log read evidence is not canonical and complete")
	}
	if result.Facts == nil {
		result.Facts = diagnosticFacts
	} else {
		result.Facts = result.Facts.Clone()
		result.Facts.DiagnosticLogReads = diagnosticFacts.DiagnosticLogReads
	}

	return nil
}
