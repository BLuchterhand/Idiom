package comprehension

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	RankerLearningEnv = "IDIOM_RANKER_LEARNING"

	LearningStateAccepted = "accepted"
	LearningStateRejected = "rejected"

	LearningReasonVerifiedFeedback = "verified_feedback"
	LearningReasonDisabled         = "learning_disabled"
	LearningReasonUnverified       = "unverified_evidence"
	LearningReasonNoValidFiles     = "no_valid_files"
	LearningReasonUnsupported      = "unsupported_action"
	LearningReasonNoOpBand         = "score_noop_band"
	LearningReasonUpdateFailed     = "update_failed"
	LearningReasonRollbackRestore  = "rollback_restored"
)

var (
	learningSafeTokenPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,127}$`)
	learningGeneratedEventIDPattern = regexp.MustCompile(`^ranker_[0-9a-f]{16}$`)
	learningHashedIDPatterns        = map[string]*regexp.Regexp{
		"task":   regexp.MustCompile(`^task_[0-9a-f]{16}$`),
		"corpus": regexp.MustCompile(`^corpus_[0-9a-f]{16}$`),
		"result": regexp.MustCompile(`^result_[0-9a-f]{16}$`),
	}

	learningLogAllowedSources = map[string]struct{}{
		"eval":             {},
		"cli":              {},
		"rollback":         {},
		"verified_edit":    {},
		"harness_feedback": {},
		"audit_watch":      {},
	}
	learningLogAllowedReasons = map[string]struct{}{
		LearningReasonVerifiedFeedback: {},
		LearningReasonDisabled:         {},
		LearningReasonUnverified:       {},
		LearningReasonNoValidFiles:     {},
		LearningReasonUnsupported:      {},
		LearningReasonNoOpBand:         {},
		LearningReasonUpdateFailed:     {},
		LearningReasonRollbackRestore:  {},
		"swe_groundedness_oracle":      {},
		"unknown":                      {},
	}
	learningLogAllowedEvidenceKinds = map[string]struct{}{
		LearningReasonVerifiedFeedback: {},
		"swe_groundedness_oracle":      {},
		"learning_event_rollback":      {},
		"unknown":                      {},
	}
	learningLogAllowedMetricKeys = map[string]struct{}{
		"score":      {},
		"boost_at":   {},
		"penalty_at": {},
		"min_score":  {},
		"unknown":    {},
	}
	learningLogAllowedWeightFeatureKeys = map[string]struct{}{}
	learningLogKnownPathBasenames       = map[string]struct{}{
		".editorconfig":     {},
		".gitattributes":    {},
		".gitignore":        {},
		"agents.md":         {},
		"cargo.lock":        {},
		"cargo.toml":        {},
		"copying":           {},
		"dockerfile":        {},
		"gemfile":           {},
		"go.mod":            {},
		"go.sum":            {},
		"gradlew":           {},
		"gradlew.bat":       {},
		"justfile":          {},
		"license":           {},
		"makefile":          {},
		"notice":            {},
		"package-lock.json": {},
		"package.json":      {},
		"pnpm-lock.yaml":    {},
		"pom.xml":           {},
		"procfile":          {},
		"pyproject.toml":    {},
		"rakefile":          {},
		"readme":            {},
		"requirements.txt":  {},
		"settings.gradle":   {},
		"yarn.lock":         {},
	}
	learningLogAllowedFileExtensions = map[string]struct{}{
		".bash":       {},
		".c":          {},
		".cc":         {},
		".cfg":        {},
		".clj":        {},
		".cpp":        {},
		".cs":         {},
		".css":        {},
		".csv":        {},
		".dockerfile": {},
		".erl":        {},
		".ex":         {},
		".exs":        {},
		".fs":         {},
		".go":         {},
		".gradle":     {},
		".h":          {},
		".hpp":        {},
		".hrl":        {},
		".html":       {},
		".java":       {},
		".js":         {},
		".json":       {},
		".jsonl":      {},
		".jsx":        {},
		".kt":         {},
		".lock":       {},
		".lua":        {},
		".m":          {},
		".md":         {},
		".mk":         {},
		".mm":         {},
		".mod":        {},
		".php":        {},
		".pl":         {},
		".proto":      {},
		".py":         {},
		".r":          {},
		".rb":         {},
		".rs":         {},
		".sbt":        {},
		".scala":      {},
		".sh":         {},
		".sql":        {},
		".sum":        {},
		".swift":      {},
		".tf":         {},
		".toml":       {},
		".ts":         {},
		".tsx":        {},
		".txt":        {},
		".xml":        {},
		".yaml":       {},
		".yml":        {},
		".zsh":        {},
	}
)

// VerifiedRankerLearningRequest is the input contract for the automated ranker
// learning path. Callers must supply deterministic, already-verified evidence;
// unverified input is logged as rejected and never mutates weights.
type VerifiedRankerLearningRequest struct {
	Source   string
	Action   string
	Files    []string
	Score    float64
	TaskID   string
	CorpusID string
	ResultID string
	Reason   string
	Verified bool
	Evidence LearningEvidenceRef
}

// ApplyVerifiedRankerLearning is the single automated weight-mutation gate for
// verified feedback. It records every accepted or rejected decision in the
// inspectable learning log with rollback data for accepted mutations.
func ApplyVerifiedRankerLearning(root string, req VerifiedRankerLearningRequest) (LearningLogEntry, map[string]float64, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return LearningLogEntry{}, nil, errors.New("workspace root is required for ranker learning")
	}

	source := sanitizeLearningSource(req.Source)
	action := normalizeLearningAction(req.Action)
	logAction := action
	if logAction == "" {
		logAction = "no_op"
	}
	taskID := sanitizeLearningCorrelationID("task", req.TaskID)
	corpusID := sanitizeLearningCorrelationID("corpus", req.CorpusID)
	resultID := sanitizeLearningCorrelationID("result", req.ResultID)
	files, invalidPathCount := sanitizeLearningPaths(req.Files)

	before, err := LoadLearnedWeights(root)
	if err != nil {
		return LearningLogEntry{}, nil, err
	}
	beforeCopy := cloneWeights(before)
	afterCopy := cloneWeights(before)
	entry := LearningLogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Source:    source,
		Action:    logAction,
		State:     LearningStateRejected,
		Score:     req.Score,
		Files:     files,
		TaskID:    taskID,
		CorpusID:  corpusID,
		ResultID:  resultID,
		Evidence:  learningEvidenceRef(req, files, invalidPathCount),
	}
	entry.BeforeWeights = &beforeCopy
	entry.AfterWeights = &afterCopy
	entry.Rollback = &LearningRollbackData{Weights: cloneWeights(before)}

	verified := req.Verified || (entry.Evidence != nil && entry.Evidence.Verified)
	switch {
	case RankerLearningDisabled():
		entry.Reason = LearningReasonDisabled
	case !verified:
		entry.Reason = LearningReasonUnverified
	case action == "":
		entry.Action = "no_op"
		entry.Reason = LearningReasonUnsupported
	case action == "boost" || action == "penalize":
		if len(files) == 0 {
			entry.Reason = LearningReasonNoValidFiles
		} else {
			updateOpts := UpdateLearnedWeightsOptions{}
			if action == "boost" {
				updateOpts.Boost = files
			} else {
				updateOpts.Penalty = files
			}
			after, updateErr := UpdateLearnedWeights(root, updateOpts)
			if updateErr != nil {
				entry.Reason = LearningReasonUpdateFailed
				if appendErr := appendLearningDecision(root, &entry); appendErr != nil {
					return entry, before, appendErr
				}
				return entry, before, updateErr
			}
			afterCopy = cloneWeights(after)
			entry.AfterWeights = &afterCopy
			entry.State = LearningStateAccepted
			entry.Reason = sanitizeVerifiedLearningReason(req.Reason)
		}
	case action == "no_op":
		entry.State = LearningStateAccepted
		entry.Reason = LearningReasonNoOpBand
	default:
		entry.Action = "no_op"
		entry.Reason = LearningReasonUnsupported
	}

	if err := appendLearningDecision(root, &entry); err != nil {
		if entry.State == LearningStateAccepted && (entry.Action == "boost" || entry.Action == "penalize") {
			_ = SaveLearnedWeights(root, before)
		}
		return entry, before, err
	}
	if entry.AfterWeights != nil {
		return entry, cloneWeights(*entry.AfterWeights), nil
	}
	return entry, before, nil
}

// RestoreLearnedWeightsFromLearningLog restores weights to the state captured
// before the referenced learning event and appends a restore event.
func RestoreLearnedWeightsFromLearningLog(root string, eventID string) (map[string]float64, LearningLogEntry, error) {
	root = strings.TrimSpace(root)
	eventID = strings.TrimSpace(eventID)
	if root == "" {
		return nil, LearningLogEntry{}, errors.New("workspace root is required for ranker rollback")
	}
	if eventID == "" {
		return nil, LearningLogEntry{}, errors.New("learning event id is required for ranker rollback")
	}
	if !learningGeneratedEventIDPattern.MatchString(eventID) {
		return nil, LearningLogEntry{}, fmt.Errorf("learning event %q has invalid generated-id shape", eventID)
	}
	entries, err := ReadLearningLog(root)
	if err != nil {
		return nil, LearningLogEntry{}, err
	}
	var target *LearningLogEntry
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].EventID == eventID {
			entry := entries[i]
			target = &entry
			break
		}
	}
	if target == nil {
		return nil, LearningLogEntry{}, fmt.Errorf("learning event %q not found", eventID)
	}
	restoreWeights, ok := rollbackWeightsForEntry(*target)
	if !ok {
		return nil, LearningLogEntry{}, fmt.Errorf("learning event %q has no rollback weights", eventID)
	}
	before, err := LoadLearnedWeights(root)
	if err != nil {
		return nil, LearningLogEntry{}, err
	}
	if err := SaveLearnedWeights(root, restoreWeights); err != nil {
		return nil, LearningLogEntry{}, err
	}
	after, err := LoadLearnedWeights(root)
	if err != nil {
		return nil, LearningLogEntry{}, err
	}
	beforeCopy := cloneWeights(before)
	afterCopy := cloneWeights(after)
	restoreEntry := LearningLogEntry{
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Source:        "rollback",
		Action:        "restore",
		State:         LearningStateAccepted,
		Reason:        LearningReasonRollbackRestore,
		ResultID:      eventID,
		BeforeWeights: &beforeCopy,
		AfterWeights:  &afterCopy,
		Rollback:      &LearningRollbackData{Weights: cloneWeights(before)},
		Evidence: &LearningEvidenceRef{
			Kind:     "learning_event_rollback",
			Verified: true,
			ResultID: eventID,
		},
	}
	if err := appendLearningDecision(root, &restoreEntry); err != nil {
		return after, restoreEntry, err
	}
	return after, restoreEntry, nil
}

func RankerLearningDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(RankerLearningEnv))) {
	case "0", "false", "off", "disabled", "disable", "no":
		return true
	default:
		return false
	}
}

func appendLearningDecision(root string, entry *LearningLogEntry) error {
	if entry == nil {
		return errors.New("learning decision entry is nil")
	}
	if entry.EventID == "" {
		entry.EventID = learningEventID(*entry)
	}
	return AppendLearningLog(root, *entry)
}

func learningEvidenceRef(req VerifiedRankerLearningRequest, files []string, invalidPathCount int) *LearningEvidenceRef {
	evidence := req.Evidence
	evidence.Kind = sanitizeLearningEvidenceKind(evidence.Kind)
	if evidence.Kind == "" {
		evidence.Kind = LearningReasonVerifiedFeedback
	}
	evidence.Verified = evidence.Verified || req.Verified
	evidence.TaskID = firstNonEmpty(sanitizeLearningCorrelationID("task", evidence.TaskID), sanitizeLearningCorrelationID("task", req.TaskID))
	evidence.CorpusID = firstNonEmpty(sanitizeLearningCorrelationID("corpus", evidence.CorpusID), sanitizeLearningCorrelationID("corpus", req.CorpusID))
	evidence.ResultID = firstNonEmpty(sanitizeLearningCorrelationID("result", evidence.ResultID), sanitizeLearningCorrelationID("result", req.ResultID))
	evidence.Files = sanitizeLearningPathList(firstNonEmptyStrings(evidence.Files, files))
	evidence.InvalidPathCount += invalidPathCount
	evidence.Metrics = sanitizeLearningMetrics(evidence.Metrics)
	if evidence.Metrics == nil {
		evidence.Metrics = make(map[string]float64)
	}
	evidence.Metrics["score"] = req.Score
	return &evidence
}

func sanitizeVerifiedLearningReason(value string) string {
	reason := sanitizeLearningReason(value)
	if reason == "" || reason == "unknown" {
		return LearningReasonVerifiedFeedback
	}
	return reason
}

func rollbackWeightsForEntry(entry LearningLogEntry) (map[string]float64, bool) {
	if entry.Rollback != nil {
		return cloneWeights(entry.Rollback.Weights), true
	}
	if entry.BeforeWeights != nil {
		return cloneWeights(*entry.BeforeWeights), true
	}
	return nil, false
}

func normalizeLearningAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "boost":
		return "boost"
	case "penalize", "penalty":
		return "penalize"
	case "no_op", "noop", "none":
		return "no_op"
	default:
		return ""
	}
}

func sanitizeLearningPaths(paths []string) ([]string, int) {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	invalid := 0
	for _, path := range paths {
		key := normalizeWeightKey(path)
		if key == "" {
			invalid++
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out, invalid
}

func sanitizeLearningPathList(paths []string) []string {
	clean, _ := sanitizeLearningPaths(paths)
	return clean
}

func sanitizeLearningMetrics(metrics map[string]float64) map[string]float64 {
	if len(metrics) == 0 {
		return nil
	}
	out := make(map[string]float64, len(metrics))
	unknownCount := 0
	for key, value := range metrics {
		if value != value {
			continue
		}
		safeKey := sanitizeLearningMetricKey(key)
		if safeKey == "" {
			continue
		}
		if safeKey == "unknown" {
			unknownCount++
			continue
		}
		out[safeKey] = value
	}
	if unknownCount > 0 {
		out["unknown"] = float64(unknownCount)
	}
	return out
}

func cloneWeights(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for key, value := range in {
		clean := normalizeWeightKey(key)
		if clean == "" {
			continue
		}
		out[clean] = value
	}
	return out
}

func learningEventID(entry LearningLogEntry) string {
	h := sha256.New()
	h.Write([]byte(entry.Timestamp))
	h.Write([]byte{0})
	h.Write([]byte(entry.Source))
	h.Write([]byte{0})
	h.Write([]byte(entry.Action))
	h.Write([]byte{0})
	h.Write([]byte(entry.State))
	h.Write([]byte{0})
	h.Write([]byte(entry.Reason))
	h.Write([]byte{0})
	for _, value := range []string{entry.TaskID, entry.CorpusID, entry.ResultID} {
		h.Write([]byte(value))
		h.Write([]byte{0})
	}
	files := append([]string(nil), entry.Files...)
	sort.Strings(files)
	for _, file := range files {
		h.Write([]byte(file))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return "ranker_" + hex.EncodeToString(sum[:8])
}

func firstNonEmptyStrings(primary []string, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}
