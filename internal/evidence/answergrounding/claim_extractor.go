package answergrounding

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	sourceLocationPattern = regexp.MustCompile(`(^|[\s\[` + "`" + `(])([A-Za-z0-9_./-]+\.go):([0-9]+)(?:-([0-9]+))?`)
	pathPattern           = regexp.MustCompile(`(^|[\s\[` + "`" + `(])([A-Za-z0-9_./-]+\.go)(?:$|[\s\]` + "`" + `).,;])`)
)

type ClaimExtractor struct{}

func (ClaimExtractor) Extract(answer string) []AnswerClaim {
	var claims []AnswerClaim

	for lineNumber, rawLine := range strings.Split(answer, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}

		claims = append(claims, extractClaimsFromLine(line, lineNumber+1)...)
	}

	return claims
}

func extractClaimsFromLine(line string, lineNumber int) []AnswerClaim {
	var claims []AnswerClaim

	if verificationClaim(line) {
		claims = append(claims, AnswerClaim{
			Text:     line,
			Kind:     ClaimCommandResult,
			Target:   TargetVerification,
			Line:     lineNumber,
			Severity: SeverityBlocking,
			Reason:   "verification claim requires command_result evidence",
		})
	}

	if changeClaim(line) {
		claims = append(claims, AnswerClaim{
			Text:     line,
			Kind:     Change,
			Target:   TargetChangedPath,
			Line:     lineNumber,
			Severity: SeverityBlocking,
			Reason:   "change claim requires diff_write evidence",
		})
	}

	if match := sourceLocationPattern.FindStringSubmatch(line); match != nil {
		target := TargetNormalizer{}.Normalize(match[2])
		startLine, _ := strconv.Atoi(match[3])

		claims = append(claims, AnswerClaim{
			Text:     strings.TrimSpace(match[0]),
			Kind:     SourceLocation,
			Target:   target,
			Line:     startLine,
			Severity: SeverityBlocking,
			Reason:   "source location claim requires source_body or symbol_body evidence covering the cited line",
		})

		return claims
	}

	if match := pathPattern.FindStringSubmatch(line); match != nil {
		target := TargetNormalizer{}.Normalize(match[2])

		claims = append(claims, AnswerClaim{
			Text:     strings.TrimSpace(match[0]),
			Kind:     Path,
			Target:   target,
			Line:     lineNumber,
			Severity: SeverityBlocking,
			Reason:   "path reference requires path or packet evidence",
		})
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
