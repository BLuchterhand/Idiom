package agent

import (
	"slices"
	"strings"

	"idiom/internal/contracts/autonomy"
	contractcompile "idiom/internal/contracts/compile"
)

// --- contract parity ---
type ParityInput struct {
	ContractID               string
	ProtocolHash             string
	SurfaceID                string
	InvariantID              string
	OldDecision              string
	NewDecision              string
	VerificationStatusBefore string
	VerificationStatusAfter  string
	ObligationDeltasBefore   []string
	ObligationDeltasAfter    []string
}

type ParityReport struct {
	ContractID               string
	ProtocolHash             string
	SurfaceID                string
	InvariantID              string
	OldDecision              string
	NewDecision              string
	VerificationStatusBefore string
	VerificationStatusAfter  string
	ObligationDeltasBefore   []string
	ObligationDeltasAfter    []string
	Diverged                 bool
	DivergenceReason         string
}

func CompareParity(input ParityInput) ParityReport {
	report := ParityReport{
		ContractID:               strings.TrimSpace(input.ContractID),
		ProtocolHash:             strings.TrimSpace(input.ProtocolHash),
		SurfaceID:                strings.TrimSpace(input.SurfaceID),
		InvariantID:              strings.TrimSpace(input.InvariantID),
		OldDecision:              strings.TrimSpace(input.OldDecision),
		NewDecision:              strings.TrimSpace(input.NewDecision),
		VerificationStatusBefore: strings.TrimSpace(input.VerificationStatusBefore),
		VerificationStatusAfter:  strings.TrimSpace(input.VerificationStatusAfter),
		ObligationDeltasBefore:   append([]string(nil), input.ObligationDeltasBefore...),
		ObligationDeltasAfter:    append([]string(nil), input.ObligationDeltasAfter...),
	}
	switch {
	case report.OldDecision != report.NewDecision:
		report.Diverged = true
		report.DivergenceReason = "decision_mismatch"
	case report.VerificationStatusBefore != report.VerificationStatusAfter:
		report.Diverged = true
		report.DivergenceReason = "verification_status_mismatch"
	case !sameStrings(report.ObligationDeltasBefore, report.ObligationDeltasAfter):
		report.Diverged = true
		report.DivergenceReason = "obligation_delta_mismatch"
	}
	return report
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- turn contract projection ---
// TurnContract is the runtime projection over canonical contracts. Canonical
// semantics remain in internal/contracts; this package carries the active
// per-turn ids and protocol projection used by runner adapters.
type TurnContract struct {
	ContractID string
	// ProtocolHash carries the active protocol hash once the compiled
	// projection is authoritative. During legacy compatibility it may carry the
	// model-call id so downstream surfaces still have a stable per-turn
	// projection reference.
	ProtocolHash  string
	PhaseID       string
	InvariantIDs  []string
	ObligationIDs []string

	Canonical       autonomy.AutonomyContract
	Protocol        contractcompile.ModelProtocolProjection
	ActiveAuthority contractcompile.ActiveAuthorityBinding
}

func FromCompileResult(result contractcompile.CompileResult) TurnContract {
	return TurnContract{
		ContractID:      firstNonEmpty(result.ActiveAuthority.ContractID, result.Contract.ContractID, result.Protocol.ContractID),
		ProtocolHash:    firstNonEmpty(result.ActiveAuthority.ProtocolHash, result.Protocol.ProtocolHash),
		PhaseID:         firstNonEmpty(result.Protocol.PhaseID, result.Contract.Scope.Phase),
		InvariantIDs:    compactStrings(result.ActiveAuthority.ActiveInvariantID, result.Protocol.ActiveInvariantID),
		ObligationIDs:   obligationIDs(result.Contract.Obligations),
		Canonical:       result.Contract,
		Protocol:        result.Protocol,
		ActiveAuthority: result.ActiveAuthority,
	}
}

func (c TurnContract) HasActiveAuthority() bool {
	return c.ContractID != "" && c.ProtocolHash != ""
}

func (c TurnContract) WithProtocolHash(protocolHash string) TurnContract {
	if protocolHash != "" {
		c.ProtocolHash = protocolHash
		c.Protocol.ProtocolHash = protocolHash
		c.ActiveAuthority.ProtocolHash = protocolHash
	}
	return c
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func compactStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func obligationIDs(obligations []autonomy.Obligation) []string {
	out := make([]string, 0, len(obligations))
	for _, obligation := range obligations {
		if obligation.ObligationID != "" && !slices.Contains(out, obligation.ObligationID) {
			out = append(out, obligation.ObligationID)
		}
	}
	return out
}
