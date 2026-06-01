package answergrounding

import (
	"regexp"
	"strconv"
	"strings"
)

type EvidenceKind string

const (
	SourceBody    EvidenceKind = "source_body"
	SymbolBody    EvidenceKind = "symbol_body"
	LogWindow     EvidenceKind = "log_window"
	CommandResult EvidenceKind = "command_result"
	DiffWrite     EvidenceKind = "diff_write"
	PathSeen      EvidenceKind = "path_seen"
	Packet        EvidenceKind = "packet"
)

type ClaimKind string

const (
	Path               ClaimKind = "path"
	SourceLocation     ClaimKind = "source_location"
	ClaimCommandResult ClaimKind = "command_result"
	Change             ClaimKind = "change"
	Unsupported        ClaimKind = "unsupported"
)

type EvidenceReceipt struct {
	ID        string
	Kind      EvidenceKind
	Target    string
	StartLine int
	EndLine   int
	Supports  []ClaimKind
	Summary   string
	Fresh     bool
}

type AnswerClaim struct {
	Text        string
	Kind        ClaimKind
	Target      string
	Line        int
	EvidenceIDs []string
	Supported   bool
	Severity    string
	Reason      string
}

type AnswerGroundingResult struct {
	Claims              []AnswerClaim
	BlockingClaims      []AnswerClaim
	HasBlockingFindings bool
}

var (
	sourceLocationPattern = regexp.MustCompile(`(^|[\s\[` + "`" + `(])([A-Za-z0-9_./-]+\.go):([0-9]+)(?:-([0-9]+))?`)
	pathPattern           = regexp.MustCompile(`(^|[\s\[` + "`" + `(])([A-Za-z0-9_./-]+\.go)(?:$|[\s\]` + "`" + `).,;])`)
)

func Evaluate(answer string, receipts []EvidenceReceipt) AnswerGroundingResult {
	var result AnswerGroundingResult
	for lineNumber, rawLine := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		for _, claim := range extractClaimsFromLine(line, lineNumber+1, receipts) {
			result.Claims = append(result.Claims, claim)
			if claim.Severity == "blocking" {
				result.BlockingClaims = append(result.BlockingClaims, claim)
			}
		}
	}
	result.HasBlockingFindings = len(result.BlockingClaims) > 0
	return result
}

func extractClaimsFromLine(line string, lineNumber int, receipts []EvidenceReceipt) []AnswerClaim {
	var claims []AnswerClaim
	if verificationClaim(line) {
		claims = append(claims, supportCommandResultClaim(AnswerClaim{
			Text:     line,
			Kind:     ClaimCommandResult,
			Target:   "verification",
			Line:     lineNumber,
			Severity: "blocking",
			Reason:   "verification claim requires command_result evidence",
		}, receipts))
	}
	if changeClaim(line) {
		claims = append(claims, supportChangeClaim(AnswerClaim{
			Text:     line,
			Kind:     Change,
			Target:   "changed_path",
			Line:     lineNumber,
			Severity: "blocking",
			Reason:   "change claim requires diff_write evidence",
		}, receipts))
	}
	if match := sourceLocationPattern.FindStringSubmatch(line); match != nil {
		target := normalizeTarget(match[2])
		startLine, _ := strconv.Atoi(match[3])
		endLine := startLine
		if match[4] != "" {
			if parsed, err := strconv.Atoi(match[4]); err == nil {
				endLine = parsed
			}
		}
		claims = append(claims, supportSourceLocationClaim(AnswerClaim{
			Text:     strings.TrimSpace(match[0]),
			Kind:     SourceLocation,
			Target:   target,
			Line:     startLine,
			Severity: "blocking",
			Reason:   "source location claim requires source_body or symbol_body evidence covering the cited line",
		}, receipts, startLine, endLine))
		return claims
	}
	if match := pathPattern.FindStringSubmatch(line); match != nil {
		target := normalizeTarget(match[2])
		claims = append(claims, supportPathClaim(AnswerClaim{
			Text:     strings.TrimSpace(match[0]),
			Kind:     Path,
			Target:   target,
			Line:     lineNumber,
			Severity: "blocking",
			Reason:   "path reference requires path or packet evidence",
		}, receipts))
	}
	return claims
}

func verificationClaim(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "verification passed") ||
		strings.Contains(lower, "tests passed") ||
		strings.Contains(lower, "test passed") ||
		strings.Contains(lower, "verified")
}

func changeClaim(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "changed") ||
		strings.Contains(lower, "modified") ||
		strings.Contains(lower, "implemented") ||
		strings.Contains(lower, "updated")
}

func supportCommandResultClaim(claim AnswerClaim, receipts []EvidenceReceipt) AnswerClaim {
	for _, receipt := range receipts {
		if receipt.Kind == CommandResult {
			return markSupported(claim, receipt.ID)
		}
	}
	return claim
}

func supportChangeClaim(claim AnswerClaim, receipts []EvidenceReceipt) AnswerClaim {
	for _, receipt := range receipts {
		if receipt.Kind == DiffWrite {
			return markSupported(claim, receipt.ID)
		}
	}
	return claim
}

func supportSourceLocationClaim(claim AnswerClaim, receipts []EvidenceReceipt, startLine int, endLine int) AnswerClaim {
	for _, receipt := range receipts {
		if receipt.Kind != SourceBody && receipt.Kind != SymbolBody {
			continue
		}
		if !sameTarget(receipt.Target, claim.Target) {
			continue
		}
		if receipt.StartLine <= startLine && endLine <= receipt.EndLine {
			return markSupported(claim, receipt.ID)
		}
	}
	return claim
}

func supportPathClaim(claim AnswerClaim, receipts []EvidenceReceipt) AnswerClaim {
	for _, receipt := range receipts {
		if !sameTarget(receipt.Target, claim.Target) {
			continue
		}
		switch receipt.Kind {
		case PathSeen, Packet, SourceBody, SymbolBody, DiffWrite:
			return markSupported(claim, receipt.ID)
		}
	}
	return claim
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

func sameTarget(receiptTarget string, claimTarget string) bool {
	receiptTarget = normalizeTarget(receiptTarget)
	claimTarget = normalizeTarget(claimTarget)
	return receiptTarget == claimTarget
}

func normalizeTarget(target string) string {
	target = strings.TrimSpace(target)
	target = strings.Trim(target, "`'\".,;:()[]")
	target = strings.ReplaceAll(target, "\\", "/")
	for strings.HasPrefix(target, "./") {
		target = strings.TrimPrefix(target, "./")
	}
	return target
}
