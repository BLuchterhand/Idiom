package answergrounding

import (
	"strconv"
	"strings"
)

type GroundingValidator struct {
	ledger EvidenceLedger
}

func NewGroundingValidator(receipts []EvidenceReceipt) GroundingValidator {
	return GroundingValidator{
		ledger: NewEvidenceLedger(receipts),
	}
}

func (v GroundingValidator) Validate(claims []AnswerClaim) []AnswerClaim {
	if len(claims) == 0 {
		return nil
	}
	validated := make([]AnswerClaim, 0, len(claims))
	for _, claim := range claims {
		validated = append(validated, v.ValidateClaim(claim))
	}
	return validated
}

func (v GroundingValidator) ValidateClaim(claim AnswerClaim) AnswerClaim {
	switch claim.Kind {
	case ClaimCommandResult:
		if receipt, ok := v.ledger.FindAny(CommandResult); ok {
			return markSupported(claim, receipt.ID)
		}
	case Change:
		if receipt, ok := v.ledger.FindAny(DiffWrite); ok {
			return markSupported(claim, receipt.ID)
		}
	case SourceLocation:
		startLine, endLine := sourceClaimRange(claim)
		if receipt, ok := v.ledger.FindCoveredTarget(claim.Target, startLine, endLine, SourceBody, SymbolBody); ok {
			return markSupported(claim, receipt.ID)
		}
	case Path:
		if receipt, ok := v.ledger.FindTarget(claim.Target, PathSeen, Packet, SourceBody, SymbolBody, DiffWrite); ok {
			return markSupported(claim, receipt.ID)
		}
	}
	return claim
}

func sourceClaimRange(claim AnswerClaim) (int, int) {
	startLine := claim.Line
	endLine := claim.Line
	if claim.Kind != SourceLocation {
		return startLine, endLine
	}
	match := sourceLocationPattern.FindStringSubmatch(claim.Text)
	if match == nil {
		return startLine, endLine
	}
	if parsed, err := strconv.Atoi(match[3]); err == nil {
		startLine = parsed
		endLine = parsed
	}
	if match[4] != "" {
		if parsed, err := strconv.Atoi(match[4]); err == nil {
			endLine = parsed
		}
	}
	return startLine, endLine
}

func markSupported(claim AnswerClaim, evidenceID string) AnswerClaim {
	claim.Supported = true
	claim.Severity = ""
	claim.Reason = ""
	if strings.TrimSpace(evidenceID) != "" {
		claim.EvidenceIDs = append(claim.EvidenceIDs, evidenceID)
	}
	return claim
}
