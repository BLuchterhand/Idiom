package agent

import (
	"fmt"
	"strings"

	"idiom/internal/frontier/workerruntime"
	"idiom/internal/workspace"
)

type readOnlyClosureState string

const (
	readOnlyClosureAnswerReady readOnlyClosureState = "answer_ready"
	readOnlyClosureNotReady    readOnlyClosureState = "not_ready"
)

type readOnlyClosureReason string

const (
	readOnlyClosureReasonAnswerable               readOnlyClosureReason = "answerable_evidence"
	readOnlyClosureReasonHighCardinalitySearch    readOnlyClosureReason = "high_cardinality_localization_only"
	readOnlyClosureReasonMissingNamedSourceBody   readOnlyClosureReason = "missing_named_source_body"
	readOnlyClosureReasonCandidatesNeedSource     readOnlyClosureReason = "candidate_paths_need_source_body"
	readOnlyClosureReasonNoRelevantSourceEvidence readOnlyClosureReason = "no_relevant_source_or_diagnostic_evidence"
)

type readOnlyClosureDecision struct {
	State    readOnlyClosureState
	Reason   readOnlyClosureReason
	Evidence readOnlyAnswerabilityLedger
}

func readOnlyClosureDecisionForResults(brief workerruntime.WorkerRuntimeBrief, results []workspace.ToolResult) readOnlyClosureDecision {
	evidence := readOnlyAnswerabilityForResults(brief, results)
	if evidence.Answerable() {
		return readOnlyClosureDecision{
			State:    readOnlyClosureAnswerReady,
			Reason:   readOnlyClosureReasonAnswerable,
			Evidence: evidence,
		}
	}
	reason := readOnlyClosureReasonNoRelevantSourceEvidence
	switch {
	case evidence.HighCardinalitySearch && len(evidence.CandidatePaths) > 0:
		reason = readOnlyClosureReasonHighCardinalitySearch
	case len(evidence.MissingNamedSourcePaths) > 0:
		reason = readOnlyClosureReasonMissingNamedSourceBody
	case len(evidence.CandidatePaths) > 0:
		reason = readOnlyClosureReasonCandidatesNeedSource
	}
	return readOnlyClosureDecision{
		State:    readOnlyClosureNotReady,
		Reason:   reason,
		Evidence: evidence,
	}
}

func (d readOnlyClosureDecision) Answerable() bool {
	return d.State == readOnlyClosureAnswerReady
}

func (d readOnlyClosureDecision) BlockerText() string {
	evidence := d.Evidence
	switch d.Reason {
	case readOnlyClosureReasonHighCardinalitySearch:
		objective := strings.TrimSpace(evidence.Objective)
		if objective != "" {
			return fmt.Sprintf("<<QUESTION>>: The current read-only answer has only localization evidence for `%s`. The broad search returned %d hit(s); read one objective-relevant source candidate such as %s before answering, or name the exact missing claim.", objective, evidence.SearchHitCount, formatInlinePathList(d.PrioritizedCandidatePaths(), 3))
		}
		return fmt.Sprintf("<<QUESTION>>: The current read-only answer has only localization evidence. The broad search returned %d hit(s); read one objective-relevant source candidate such as %s before answering, or name the exact missing claim.", evidence.SearchHitCount, formatInlinePathList(d.PrioritizedCandidatePaths(), 3))
	case readOnlyClosureReasonMissingNamedSourceBody:
		return fmt.Sprintf("<<QUESTION>>: The current read-only answer is missing source-body evidence for named path(s): %s. Read those path(s), or name the exact blocker.", formatInlinePathList(evidence.MissingNamedSourcePaths, 4))
	case readOnlyClosureReasonCandidatesNeedSource:
		return fmt.Sprintf("<<QUESTION>>: The current read-only answer has candidate paths but no relevant source-body evidence. Inspect one candidate such as %s with a source-body read, or name the exact missing claim.", formatInlinePathList(evidence.CandidatePaths, 3))
	default:
		if paths := readOnlyClosureKnownEvidencePaths(evidence); len(paths) > 0 {
			return fmt.Sprintf("<<QUESTION>>: The current read-only answer has evidence in %s, but it is not backed by relevant source-body or diagnostic evidence for the requested claim. State the supported concrete finding from that evidence, or name the exact missing claim.", formatInlinePathList(paths, 4))
		}
		return "<<QUESTION>>: The current read-only answer is not backed by relevant source-body or diagnostic evidence. Gather one source-body read for the claim, or name the exact missing claim."
	}
}

func readOnlyClosureKnownEvidencePaths(evidence readOnlyAnswerabilityLedger) []string {
	paths := mergeRunnerStateStrings(evidence.CandidatePaths, evidence.SourceBodyPaths, evidence.NamedSourcePaths)
	return readOnlySourceLikePaths(paths, 8)
}

func (d readOnlyClosureDecision) PrioritizedCandidatePaths() []string {
	evidence := d.Evidence
	var priority []string
	var rest []string
	for _, path := range evidence.CandidatePaths {
		if implementationSourcePathOverlapsObjective(path, evidence.Objective) || sourceEvidencePathSharesObjectiveTerm(path, evidence.Objective) {
			priority = append(priority, path)
		} else {
			rest = append(rest, path)
		}
	}
	return mergeRunnerStateStrings(priority, rest)
}
