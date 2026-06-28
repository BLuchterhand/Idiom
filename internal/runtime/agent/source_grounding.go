package agent

import "strings"

type sourceGroundingRejectionKind string

const (
	sourceGroundingRejectionUnknown           sourceGroundingRejectionKind = ""
	sourceGroundingRejectionCandidateOnly     sourceGroundingRejectionKind = "candidate_only"
	sourceGroundingRejectionMissingCandidate  sourceGroundingRejectionKind = "missing_candidate"
	sourceGroundingRejectionUnsupportedDetail sourceGroundingRejectionKind = "unsupported_detail"
	sourceGroundingRejectionExactDetail       sourceGroundingRejectionKind = "exact_detail"
)

type sourceGroundingRejection struct {
	Kind  sourceGroundingRejectionKind
	Paths []string
	Text  string
}

func classifySourceGroundingRejectionText(text string) (sourceGroundingRejection, bool) {
	text = strings.TrimSpace(stripGateRetryPrefix(text))
	if text == "" {
		return sourceGroundingRejection{}, false
	}

	lower := strings.ToLower(text)
	switch {
	case strings.Contains(text, "Candidate/search/context paths are not source-body evidence"):
		return sourceGroundingRejection{
			Kind:  sourceGroundingRejectionCandidateOnly,
			Paths: sourceGroundingPathsAfterMarker(text, "Candidate path(s):"),
			Text:  text,
		}, true
	case strings.Contains(text, "not read as source-body evidence"):
		return sourceGroundingRejection{
			Kind:  sourceGroundingRejectionMissingCandidate,
			Paths: sourceGroundingPathsAfterMarker(text, "not read as source-body evidence:"),
			Text:  text,
		}, true
	case strings.Contains(text, "not backed by a source-body read"):
		return sourceGroundingRejection{
			Kind:  sourceGroundingRejectionUnsupportedDetail,
			Paths: sourceGroundingPathsAfterMarker(text, "not backed by a source-body read:"),
			Text:  text,
		}, true
	case strings.Contains(lower, "source-body evidence") && strings.Contains(lower, "answer cites path"):
		return sourceGroundingRejection{
			Kind:  sourceGroundingRejectionMissingCandidate,
			Paths: sourceGroundingPathsAfterMarker(text, ":"),
			Text:  text,
		}, true
	case strings.Contains(lower, "without an exact inspected probe"):
		return sourceGroundingRejection{
			Kind:  sourceGroundingRejectionExactDetail,
			Paths: sourceGroundingPathsAfterMarker(text, ":"),
			Text:  text,
		}, true
	default:
		return sourceGroundingRejection{}, false
	}
}

func sourceGroundingPathsAfterMarker(text string, marker string) []string {
	text = strings.TrimSpace(stripGateRetryPrefix(text))
	marker = strings.TrimSpace(marker)
	if text == "" || marker == "" {
		return nil
	}

	lower := strings.ToLower(text)
	markerLower := strings.ToLower(marker)
	idx := strings.Index(lower, markerLower)
	if idx < 0 {
		return nil
	}

	pathText := strings.TrimSpace(text[idx+len(marker):])
	if pathText == "" {
		return nil
	}

	var paths []string
	for _, token := range strings.Split(pathText, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if idx := strings.Index(token, ". "); idx > 0 {
			token = token[:idx]
		} else if strings.HasSuffix(token, ".") {
			token = strings.TrimSuffix(token, ".")
		}
		if idx := strings.Index(token, " — "); idx > 0 {
			token = token[:idx]
		}
		token = strings.Trim(token, "`'\" ")
		if token != "" {
			paths = append(paths, token)
		}
	}

	return uniqueSortedRunnerPaths(paths)
}

func sourceGroundingCandidateOnlyMessage(paths []string) string {
	paths = nonEmptyRunnerStrings(paths, 8)
	pathSummary := strings.Join(paths, ", ")
	if pathSummary == "" {
		pathSummary = "(candidate path unavailable)"
	}
	return "read-only workspace answers require bounded source-body or diagnostic evidence tied to the user's objective before a final answer. Candidate/search/context paths are not source-body evidence. Candidate path(s): " + pathSummary + ". Inspect one candidate with source-body evidence, use one materially different source-owner probe if these candidates do not establish the claim, or answer `<<QUESTION>>:` with the exact unavailable evidence source."
}

func sourceGroundingMissingCandidateMessage(paths []string) string {
	return "the answer cites path(s) that were discovered as index candidates but not read as source-body evidence: " + quotedRunnerStrings(uniqueSortedRunnerPaths(paths)) + ". Read the first unread path with one bounded `read_file` or `code_navigate` call and cite the actual evidence from the file body. If the path was from search or graph results and does not need a full read, qualify the claim as `unverified` next to the citation."
}

func sourceGroundingUnsupportedDetailMessage(paths []string) string {
	return "the answer cites source path/line/symbol detail not backed by a source-body read: " + quotedRunnerStrings(uniqueSortedRunnerPaths(paths)) + ". Use one exact read/code_navigate body probe for the claim, or qualify it as unverified."
}

func sourceGroundingRejectionPaths(text string) []string {
	rejection, ok := classifySourceGroundingRejectionText(text)
	if !ok {
		return nil
	}
	return rejection.Paths
}

func sourceGroundingRejectionPresentableDetail(text string) string {
	rejection, ok := classifySourceGroundingRejectionText(text)
	if !ok {
		return ""
	}

	pathSuffix := ""
	if len(rejection.Paths) > 0 {
		pathSuffix = ": " + strings.Join(uniqueSortedRunnerPaths(rejection.Paths), ", ")
	}

	switch rejection.Kind {
	case sourceGroundingRejectionCandidateOnly, sourceGroundingRejectionMissingCandidate:
		return "the answer cited path(s) that were not supported by inspected evidence" + pathSuffix
	case sourceGroundingRejectionUnsupportedDetail:
		return "the answer cited source path, line, or symbol detail that was not supported by inspected evidence" + pathSuffix
	case sourceGroundingRejectionExactDetail:
		detail := firstSentenceBeforeAny(rejection.Text,
			" Either anchor that detail",
			" Either issue ONE concrete tool call",
			" Answer with the exact",
			" Use one exact read",
			"\n\nEvidence paths observed before rejection:",
		)
		detail = strings.TrimSpace(stripGateRetryPrefix(detail))
		if detail != "" {
			return lowercaseFirstSentence(detail)
		}
		return "the answer cited source path, line, or symbol detail that was not supported by inspected evidence" + pathSuffix
	default:
		return ""
	}
}
