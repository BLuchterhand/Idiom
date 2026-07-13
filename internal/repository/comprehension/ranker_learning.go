package comprehension

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"idiom/internal/platform/textutil"
)

const (
	RankerLearningEnv = "IDIOM_RANKER_LEARNING"

	LearningSourceRecoveryReconciliation = "recovery_reconciliation"

	LearningStateAccepted = "accepted"
	LearningStateRejected = "rejected"

	LearningReasonVerifiedFeedback            = "verified_feedback"
	LearningReasonVerifiedRecovery            = "verified_recovery"
	LearningReasonDisabled                    = "learning_disabled"
	LearningReasonUnverified                  = "unverified_evidence"
	LearningReasonNoValidFiles                = "no_valid_files"
	LearningReasonUnsupported                 = "unsupported_action"
	LearningReasonNoOpBand                    = "score_noop_band"
	LearningReasonUpdateFailed                = "update_failed"
	LearningReasonRollbackRestore             = "rollback_restored"
	LearningReasonVerificationEvidenceMissing = "verification_evidence_missing"
	LearningReasonNoEditPaths                 = "no_edit_paths"
	LearningReasonVerificationNotPassed       = "verification_not_passed"
	LearningReasonProtectedPathInEdits        = "protected_path_in_edits"
	LearningReasonEditPathMismatch            = "edit_path_mismatch"

	LearningEvidenceKindRecoveryReconciliation = "recovery_reconciliation"
)

var (
	learningSafeTokenPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,127}$`)
	learningGeneratedEventIDPattern = regexp.MustCompile(`^ranker_[0-9a-f]{16}$`)
	learningHashedIDPatterns        = map[string]*regexp.Regexp{
		"request": regexp.MustCompile(`^request_[0-9a-f]{16}$`),
		"task":    regexp.MustCompile(`^task_[0-9a-f]{16}$`),
		"corpus":  regexp.MustCompile(`^corpus_[0-9a-f]{16}$`),
		"result":  regexp.MustCompile(`^result_[0-9a-f]{16}$`),
	}

	learningLogAllowedSources = map[string]struct{}{
		"eval":                               {},
		"cli":                                {},
		"rollback":                           {},
		"verified_edit":                      {},
		"harness_feedback":                   {},
		"audit_watch":                        {},
		LearningSourceRecoveryReconciliation: {},
	}
	learningLogAllowedReasons = map[string]struct{}{
		LearningReasonVerifiedFeedback:            {},
		LearningReasonVerifiedRecovery:            {},
		LearningReasonDisabled:                    {},
		LearningReasonUnverified:                  {},
		LearningReasonNoValidFiles:                {},
		LearningReasonUnsupported:                 {},
		LearningReasonNoOpBand:                    {},
		LearningReasonUpdateFailed:                {},
		LearningReasonRollbackRestore:             {},
		LearningReasonVerificationEvidenceMissing: {},
		LearningReasonNoEditPaths:                 {},
		LearningReasonVerificationNotPassed:       {},
		LearningReasonProtectedPathInEdits:        {},
		LearningReasonEditPathMismatch:            {},
		"swe_groundedness_oracle":                 {},
		"unknown":                                 {},
	}
	learningLogAllowedEvidenceKinds = map[string]struct{}{
		LearningReasonVerifiedFeedback:             {},
		LearningEvidenceKindRecoveryReconciliation: {},
		"swe_groundedness_oracle":                  {},
		"learning_event_rollback":                  {},
		"unknown":                                  {},
	}
	learningLogAllowedMetricKeys = map[string]struct{}{
		"score":                      {},
		"boost_at":                   {},
		"penalty_at":                 {},
		"min_score":                  {},
		"candidate_atom_count":       {},
		"completed_atom_count":       {},
		"remaining_atom_count":       {},
		"required_test_count":        {},
		"verification_command_count": {},
		"localization_improved":      {},
		"packet_quality":             {},
		"unknown":                    {},
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
	RequestID            string
	Source               string
	Action               string
	Files                []string
	Score                float64
	TaskID               string
	CorpusID             string
	ResultID             string
	Reason               string
	Verified             bool
	Evidence             LearningEvidenceRef
	VerificationEvidence *VerificationEvidence
}

// VerificationEvidence carries deterministic proof that edits were applied and
// verified before ranker learning is permitted to mutate weights. It is
// required by the post-write verification gate in ApplyVerifiedRankerLearning.
// Recovery reconciliation uses RecordVerifiedRankerEvidence (no weight
// mutation) and does not require this gate.
type VerificationEvidence struct {
	// EditPaths are the file paths that were edited and verified.
	EditPaths []string `json:"edit_paths"`
	// VerificationCommand is the command run to verify the edits (e.g. "go test ./...").
	VerificationCommand string `json:"verification_command"`
	// VerificationPassed is true when the verification command succeeded.
	VerificationPassed bool `json:"verification_passed"`
	// VerificationOutput is a bounded sanitized summary of the command output.
	VerificationOutput string `json:"verification_output,omitempty"`
}

// ApplyVerifiedRankerLearning is the single automated weight-mutation gate for
// verified feedback. It records every accepted or rejected decision in the
// inspectable learning log with rollback data for accepted mutations.
func ApplyVerifiedRankerLearning(ctx context.Context, root string, req VerifiedRankerLearningRequest) (LearningLogEntry, map[string]float64, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return LearningLogEntry{}, nil, errors.New("workspace root is required for ranker learning")
	}
	store, err := openRankerStateStore(ctx, root)
	if err != nil {
		return LearningLogEntry{}, nil, err
	}
	defer store.Close()
	if err := store.recoverLocked(); err != nil {
		return LearningLogEntry{}, nil, err
	}
	before, err := store.loadWeightsLocked()
	if err != nil {
		return LearningLogEntry{}, nil, err
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
	after := cloneWeights(before)
	entry := LearningLogEntry{
		RequestID: req.RequestID,
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
	entry.Verification = learningLogVerificationEvidence(req.VerificationEvidence)
	materialize := false

	verified := req.Verified || (entry.Evidence != nil && entry.Evidence.Verified)
	switch {
	case RankerLearningDisabled():
		entry.Reason = LearningReasonDisabled
	case !verified:
		entry.Reason = LearningReasonUnverified
	case req.VerificationEvidence == nil:
		entry.Reason = LearningReasonVerificationEvidenceMissing
	case len(req.VerificationEvidence.EditPaths) == 0:
		entry.Reason = LearningReasonNoEditPaths
	case !req.VerificationEvidence.VerificationPassed:
		entry.Reason = LearningReasonVerificationNotPassed
	case hasProtectedEditPath(req.VerificationEvidence.EditPaths):
		entry.Reason = LearningReasonProtectedPathInEdits
	case !hasEditPathFileOverlap(req.VerificationEvidence.EditPaths, files):
		entry.Reason = LearningReasonEditPathMismatch
	case action == "":
		entry.Action = "no_op"
		entry.Reason = LearningReasonUnsupported
	case action == "boost" || action == "penalize":
		if len(files) == 0 {
			entry.Reason = LearningReasonNoValidFiles
		} else {
			update := learnedWeightUpdate{}
			if action == "boost" {
				update.boost = files
			} else {
				update.penalty = files
			}
			after = applyLearnedWeightUpdate(before, update)
			materialize = true
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

	committedEntry, replayed, err := store.commitLocked(rankerStateChange{
		entry:           entry,
		before:          before,
		after:           after,
		materialize:     materialize,
		captureSnapshot: true,
	})
	if err != nil {
		if committedEntry.EventID != "" {
			entry = committedEntry
		}
		return entry, cloneWeights(after), err
	}
	if replayed {
		return committedEntry, cloneWeights(before), nil
	}

	return committedEntry, cloneWeights(after), nil
}

// RecordVerifiedRankerEvidence records deterministic, verified learning
// evidence without mutating ranker weights. It is for audit-readable outcomes
// such as recovery reconciliation where the event is useful to the learning
// flywheel but should not directly change future context ranking.
func RecordVerifiedRankerEvidence(ctx context.Context, root string, req VerifiedRankerLearningRequest) (LearningLogEntry, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return LearningLogEntry{}, errors.New("workspace root is required for ranker evidence")
	}
	store, err := openRankerStateStore(ctx, root)
	if err != nil {
		return LearningLogEntry{}, err
	}
	defer store.Close()
	if err := store.recoverLocked(); err != nil {
		return LearningLogEntry{}, err
	}
	weights, err := store.loadWeightsLocked()
	if err != nil {
		return LearningLogEntry{}, err
	}

	source := sanitizeLearningSource(req.Source)
	taskID := sanitizeLearningCorrelationID("task", req.TaskID)
	corpusID := sanitizeLearningCorrelationID("corpus", req.CorpusID)
	resultID := sanitizeLearningCorrelationID("result", req.ResultID)
	files, invalidPathCount := sanitizeLearningPaths(req.Files)
	entry := LearningLogEntry{
		RequestID: req.RequestID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Source:    source,
		Action:    "no_op",
		State:     LearningStateRejected,
		Score:     req.Score,
		Files:     files,
		TaskID:    taskID,
		CorpusID:  corpusID,
		ResultID:  resultID,
		Evidence:  learningEvidenceRef(req, files, invalidPathCount),
	}

	verified := req.Verified || (entry.Evidence != nil && entry.Evidence.Verified)
	switch {
	case RankerLearningDisabled():
		entry.Reason = LearningReasonDisabled
	case !verified:
		entry.Reason = LearningReasonUnverified
	case len(files) == 0:
		entry.Reason = LearningReasonNoValidFiles
	default:
		entry.State = LearningStateAccepted
		entry.Reason = sanitizeVerifiedLearningReason(req.Reason)
	}

	committedEntry, _, err := store.commitLocked(rankerStateChange{
		entry:  entry,
		before: weights,
		after:  weights,
	})
	if err != nil {
		if committedEntry.EventID != "" {
			entry = committedEntry
		}
		return entry, err
	}

	return committedEntry, nil
}

// RestoreLearnedWeightsFromLearningLog restores weights to the state captured
// before the referenced learning event and appends a restore event.
func RestoreLearnedWeightsFromLearningLog(ctx context.Context, root string, eventID string, requestID string) (map[string]float64, LearningLogEntry, error) {
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
	store, err := openRankerStateStore(ctx, root)
	if err != nil {
		return nil, LearningLogEntry{}, err
	}
	defer store.Close()
	if err := store.recoverLocked(); err != nil {
		return nil, LearningLogEntry{}, err
	}
	logBytes, err := store.readLearningLogBytesLocked()
	if err != nil {
		return nil, LearningLogEntry{}, err
	}
	entries, err := parseCanonicalLearningLog(logBytes)
	if err != nil {
		return nil, LearningLogEntry{}, fmt.Errorf("read ranker learning log for rollback: %w", err)
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
	before, err := store.loadWeightsLocked()
	if err != nil {
		return nil, LearningLogEntry{}, err
	}
	restoreWeights = canonicalLearnedWeights(restoreWeights)
	restoreEntry := LearningLogEntry{
		RequestID: requestID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Source:    "rollback",
		Action:    "restore",
		State:     LearningStateAccepted,
		Reason:    LearningReasonRollbackRestore,
		ResultID:  eventID,
		Evidence: &LearningEvidenceRef{
			Kind:     "learning_event_rollback",
			Verified: true,
			ResultID: eventID,
		},
	}
	committedEntry, replayed, err := store.commitLocked(rankerStateChange{
		entry:           restoreEntry,
		before:          before,
		after:           restoreWeights,
		materialize:     true,
		captureSnapshot: true,
	})
	if err != nil {
		if committedEntry.EventID != "" {
			restoreEntry = committedEntry
		}
		return cloneWeights(restoreWeights), restoreEntry, err
	}
	if replayed {
		return cloneWeights(before), committedEntry, nil
	}

	return cloneWeights(restoreWeights), committedEntry, nil
}

// ApplyManualRankerNudge applies one CLI-requested decay-and-nudge cycle and
// records the resulting state transition in the same durable transaction.
func ApplyManualRankerNudge(ctx context.Context, root string, action string, files []string, note string, requestID string) (LearningLogEntry, map[string]float64, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return LearningLogEntry{}, nil, errors.New("workspace root is required for a manual ranker nudge")
	}
	action = normalizeLearningAction(action)
	if action != "boost" && action != "penalize" {
		return LearningLogEntry{}, nil, fmt.Errorf("manual ranker nudge action %q is unsupported", action)
	}
	files, _ = sanitizeLearningPaths(files)
	if len(files) == 0 {
		return LearningLogEntry{}, nil, errors.New("manual ranker nudge needs at least one valid file path")
	}

	store, err := openRankerStateStore(ctx, root)
	if err != nil {
		return LearningLogEntry{}, nil, err
	}
	defer store.Close()
	if err := store.recoverLocked(); err != nil {
		return LearningLogEntry{}, nil, err
	}
	before, err := store.loadWeightsLocked()
	if err != nil {
		return LearningLogEntry{}, nil, err
	}

	update := learnedWeightUpdate{}
	if action == "boost" {
		update.boost = files
	} else {
		update.penalty = files
	}
	after := applyLearnedWeightUpdate(before, update)
	entry, replayed, err := store.commitLocked(rankerStateChange{
		entry: LearningLogEntry{
			RequestID: requestID,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Source:    "cli",
			Action:    action,
			State:     LearningStateAccepted,
			Files:     files,
			Note:      note,
		},
		before:          before,
		after:           after,
		materialize:     true,
		captureSnapshot: true,
	})
	if err != nil {
		return entry, cloneWeights(after), err
	}
	if replayed {
		return entry, cloneWeights(before), nil
	}

	return entry, cloneWeights(after), nil
}

// ResetLearnedRankerWeights clears the learned materialization and records a
// rollback-capable reset event atomically with that state transition.
func ResetLearnedRankerWeights(ctx context.Context, root string, requestID string) (LearningLogEntry, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return LearningLogEntry{}, errors.New("workspace root is required to reset ranker weights")
	}

	store, err := openRankerStateStore(ctx, root)
	if err != nil {
		return LearningLogEntry{}, err
	}
	defer store.Close()
	if err := store.recoverLocked(); err != nil {
		return LearningLogEntry{}, err
	}
	before, err := store.loadWeightsLocked()
	if err != nil {
		return LearningLogEntry{}, err
	}

	entry, _, err := store.commitLocked(rankerStateChange{
		entry: LearningLogEntry{
			RequestID: requestID,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Source:    "cli",
			Action:    "reset",
			State:     LearningStateAccepted,
		},
		before:          before,
		after:           map[string]float64{},
		materialize:     true,
		captureSnapshot: true,
	})
	if err != nil {
		return entry, err
	}

	return entry, nil
}

func RankerLearningDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(RankerLearningEnv))) {
	case "0", "false", "off", "disabled", "disable", "no":
		return true
	default:
		return false
	}
}

func learningEvidenceRef(req VerifiedRankerLearningRequest, files []string, invalidPathCount int) *LearningEvidenceRef {
	evidence := req.Evidence

	evidence.Kind = sanitizeLearningEvidenceKind(evidence.Kind)

	if evidence.Kind == "" {
		evidence.Kind = LearningReasonVerifiedFeedback
	}

	evidence.Verified = evidence.Verified || req.Verified
	evidence.TaskID = textutil.FirstNonEmpty(sanitizeLearningCorrelationID("task", evidence.TaskID), sanitizeLearningCorrelationID("task", req.TaskID))
	evidence.CorpusID = textutil.FirstNonEmpty(sanitizeLearningCorrelationID("corpus", evidence.CorpusID), sanitizeLearningCorrelationID("corpus", req.CorpusID))
	evidence.ResultID = textutil.FirstNonEmpty(sanitizeLearningCorrelationID("result", evidence.ResultID), sanitizeLearningCorrelationID("result", req.ResultID))
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

	slices.Sort(out)

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
	unknownCount := float64(0)

	for key, value := range metrics {
		if value != value {
			continue
		}

		safeKey := sanitizeLearningMetricKey(key)
		if safeKey == "" {
			continue
		}

		if safeKey == "unknown" {
			if normalizeLearningLogToken(key) == "unknown" && value > 0 {
				unknownCount += value
			} else {
				unknownCount++
			}
			continue
		}

		out[safeKey] = value
	}

	if unknownCount > 0 {
		out["unknown"] = unknownCount
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
	entry.EventID = ""
	encoded, err := json.Marshal(entry)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("ranker-event\x00"), encoded...))

	return "ranker_" + hex.EncodeToString(sum[:8])
}

func canonicalLearningRequestID(requestID string, entry LearningLogEntry) string {
	if requestID = sanitizeLearningCorrelationID("request", requestID); requestID != "" {
		return requestID
	}
	sum := sha256.Sum256([]byte("ranker-request\x00" + learningRequestFingerprint(entry)))

	return "request_" + hex.EncodeToString(sum[:8])
}

func learningRequestFingerprint(entry LearningLogEntry) string {
	h := sha256.New()
	for _, value := range []string{
		entry.Source,
		entry.Action,
		entry.State,
		entry.Reason,
		strconv.FormatFloat(entry.Score, 'g', -1, 64),
		entry.TaskID,
		entry.CorpusID,
		entry.ResultID,
		entry.Note,
	} {
		h.Write([]byte(value))
		h.Write([]byte{0})
	}
	files := append([]string(nil), entry.Files...)
	slices.Sort(files)
	for _, file := range files {
		h.Write([]byte(file))
		h.Write([]byte{0})
	}
	for _, value := range []any{entry.Evidence, entry.Verification} {
		encoded, _ := json.Marshal(value)
		h.Write(encoded)
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))
}

func validateLearningLogEntrySemantics(entry LearningLogEntry) error {
	if entry.RequestID == "" {
		return errors.New("learning-log entry is missing its stable request id")
	}
	if entry.EventID == "" || entry.EventID != learningEventID(entry) {
		return errors.New("learning-log event id does not match its canonical record")
	}
	if entry.Source == "" || entry.Action == "" || entry.State == "" {
		return errors.New("learning-log entry is missing its source, action, or state")
	}
	if entry.Action == "restore" {
		if entry.State != LearningStateAccepted || entry.Source != "rollback" || entry.Reason != LearningReasonRollbackRestore ||
			entry.Evidence == nil || entry.Evidence.Kind != "learning_event_rollback" || !entry.Evidence.Verified ||
			entry.ResultID == "" || entry.Evidence.ResultID != entry.ResultID {
			return errors.New("ranker restore event is missing its rollback authority")
		}
	}
	if entry.Action == "reset" && (entry.State != LearningStateAccepted || entry.Source != "cli") {
		return errors.New("ranker reset event is missing its CLI authority")
	}

	hasBefore := entry.BeforeWeights != nil
	hasAfter := entry.AfterWeights != nil
	hasRollback := entry.Rollback != nil
	if hasBefore != hasAfter || hasBefore != hasRollback {
		return errors.New("learning-log weight snapshots must be present or absent together")
	}
	if !hasBefore {
		if learningEntryRequiresMaterialization(entry) {
			return errors.New("materializing learning-log entry is missing weight snapshots")
		}
		return nil
	}

	before := canonicalLearnedWeights(*entry.BeforeWeights)
	after := canonicalLearnedWeights(*entry.AfterWeights)
	rollback := canonicalLearnedWeights(entry.Rollback.Weights)
	if !weightsEqual(*entry.BeforeWeights, before) ||
		!weightsEqual(*entry.AfterWeights, after) ||
		!weightsEqual(entry.Rollback.Weights, rollback) {
		return errors.New("learning-log entry contains noncanonical weight snapshots")
	}
	if !weightsEqual(rollback, before) {
		return errors.New("learning-log rollback snapshot does not match before weights")
	}

	switch {
	case entry.State != LearningStateAccepted:
		if !weightsEqual(after, before) {
			return errors.New("rejected learning-log entry changes weights")
		}
	case entry.Action == "boost" || entry.Action == "penalize":
		if len(entry.Files) == 0 {
			return errors.New("ranker weight update has no canonical file targets")
		}
		update := learnedWeightUpdate{}
		if entry.Action == "boost" {
			update.boost = entry.Files
		} else {
			update.penalty = entry.Files
		}
		if !weightsEqual(after, applyLearnedWeightUpdate(before, update)) {
			return errors.New("learning-log weight transition does not match its action")
		}
	case entry.Action == "reset":
		if len(after) != 0 {
			return errors.New("ranker reset event has nonbaseline after weights")
		}
	case entry.Action == "no_op":
		if !weightsEqual(after, before) {
			return errors.New("ranker no-op event changes weights")
		}
	}

	return nil
}

func learningEntryRequiresMaterialization(entry LearningLogEntry) bool {
	if entry.State != LearningStateAccepted {
		return false
	}
	switch entry.Action {
	case "boost", "penalize", "reset", "restore":
		return true
	default:
		return false
	}
}

func learningEntryCapturesSnapshot(entry LearningLogEntry) bool {
	return entry.BeforeWeights != nil && entry.AfterWeights != nil && entry.Rollback != nil
}

func firstNonEmptyStrings(primary []string, fallback []string) []string {
	if len(primary) > 0 {
		return primary
	}

	return fallback
}

// hasProtectedEditPath returns true when any edit path is in a protected or
// generated directory (e.g. .idiom/) or fails path normalization, indicating
// it should not be used as verification evidence for ranker learning.
func hasProtectedEditPath(editPaths []string) bool {
	for _, p := range editPaths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		// Normalize the path first (strips ./, normalises \ to /, rejects
		// absolute paths, ../ escapes, and segment . or .. tokens).
		normalized := normalizeWeightKey(p)
		if normalized == "" {
			return true
		}

		// Reject paths within or exactly equal to the harness state directory.
		// Normalization has already stripped any ./ prefix so bypasses such as
		// ./.idiom/ranker/weights.json are caught here.
		if strings.HasPrefix(normalized, ".idiom/") || normalized == ".idiom" {
			return true
		}
	}

	return false
}

// hasEditPathFileOverlap returns true when at least one normalized edit path
// matches a sanitized file entry. Both sets are normalized via the standard
// weight-key pipeline so comparison is stable.
func hasEditPathFileOverlap(editPaths []string, files []string) bool {
	fileSet := make(map[string]struct{}, len(files))
	for _, f := range files {
		fileSet[f] = struct{}{}
	}

	for _, p := range editPaths {
		key := normalizeWeightKey(strings.TrimSpace(p))
		if key == "" {
			continue
		}

		if _, ok := fileSet[key]; ok {
			return true
		}
	}

	return false
}

// learningLogVerificationEvidence converts a VerificationEvidence pointer into
// a privacy-safe LearningLogVerificationEvidence for audit log persistence.
// A nil input produces nil output (no verification evidence to log).
// VerificationOutput is intentionally excluded — only structured gate
// predicates are persisted.
func learningLogVerificationEvidence(ve *VerificationEvidence) *LearningLogVerificationEvidence {
	if ve == nil {
		return nil
	}

	return &LearningLogVerificationEvidence{
		EditPaths:           append([]string(nil), ve.EditPaths...),
		VerificationCommand: ve.VerificationCommand,
		VerificationPassed:  ve.VerificationPassed,
	}
}

// --- merged from learned_weights.go ---

const (
	LearnedWeightBaseline = 1.0
	LearnedWeightDecay    = 0.95
	LearnedWeightMax      = 3.0
	LearnedWeightMin      = 0.30
	LearnedWeightBoost    = 0.15
	LearnedWeightPenalty  = 0.10
)

// ParsePersistedLearnedWeights validates the canonical on-disk representation
// written by rankerStateStore. It rejects keys or multipliers that the ranker
// owner would normalize, clamp, or discard so integrity audits do not report a
// different effective state from the runtime.
func ParsePersistedLearnedWeights(raw []byte) (map[string]float64, error) {
	var parsed map[string]float64
	if err := json.Unmarshal(raw, &parsed); err != nil || parsed == nil {
		return nil, errors.New("learned weights must be a JSON object of numeric multipliers")
	}
	if len(parsed) == 0 {
		return nil, errors.New("baseline learned weights must be represented by a missing file")
	}

	out := make(map[string]float64, len(parsed))
	for key, multiplier := range parsed {
		normalized := normalizeWeightKey(key)
		clamped := clampLearnedWeight(multiplier)
		if normalized == "" || normalized != key || clamped != multiplier || absDiff(clamped, LearnedWeightBaseline) < 1e-9 {
			return nil, errors.New("learned weights contain a noncanonical key or multiplier")
		}

		out[normalized] = clamped
	}
	canonical, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, errors.New("learned weights could not be canonically encoded")
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(raw, canonical) {
		return nil, errors.New("learned weights do not use the canonical persisted encoding")
	}

	return out, nil
}

// LoadLearnedWeights recovers any interrupted ranker transaction, then returns
// weights only when their canonical materialization matches the learning log.
// A missing weights file represents the baseline; malformed or divergent state
// fails closed.
func LoadLearnedWeights(ctx context.Context, root string) (map[string]float64, error) {
	store, err := openRankerStateStore(ctx, root)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	if err := store.recoverLocked(); err != nil {
		return nil, err
	}

	weights, _, _, err := store.loadConsistentRankerStateLocked()
	return weights, err
}

// learnedWeightUpdate is the private in-memory transformation used by ranker
// transactions. Persistence and audit ordering remain owned by rankerStateStore.
type learnedWeightUpdate struct {
	boost         []string
	penalty       []string
	boostAmount   float64
	penaltyAmount float64
}

func applyLearnedWeightUpdate(before map[string]float64, update learnedWeightUpdate) map[string]float64 {
	weights := cloneWeights(before)

	for k, v := range weights {
		weights[k] = clampLearnedWeight(LearnedWeightBaseline + (v-LearnedWeightBaseline)*LearnedWeightDecay)
	}

	boostAmount := update.boostAmount
	if boostAmount <= 0 {
		boostAmount = LearnedWeightBoost
	}

	penaltyAmount := update.penaltyAmount
	if penaltyAmount <= 0 {
		penaltyAmount = LearnedWeightPenalty
	}

	for _, p := range update.boost {
		key := normalizeWeightKey(p)
		if key == "" {
			continue
		}

		current := weights[key]
		if current == 0 {
			current = LearnedWeightBaseline
		}

		weights[key] = clampLearnedWeight(current + boostAmount)
	}

	for _, p := range update.penalty {
		key := normalizeWeightKey(p)
		if key == "" {
			continue
		}

		current := weights[key]
		if current == 0 {
			current = LearnedWeightBaseline
		}

		weights[key] = clampLearnedWeight(current - penaltyAmount)
	}

	return canonicalLearnedWeights(weights)
}

func clampLearnedWeight(v float64) float64 {
	if v != v { // NaN guard
		return LearnedWeightBaseline
	}

	if v > LearnedWeightMax {
		return LearnedWeightMax
	}

	if v < LearnedWeightMin {
		return LearnedWeightMin
	}

	return v
}

func normalizeWeightKey(p string) string {
	cleaned := strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	for strings.HasPrefix(cleaned, "./") {
		cleaned = strings.TrimPrefix(cleaned, "./")
	}

	if cleaned == "" || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return ""
	}

	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}

	return cleaned
}

func absDiff(a, b float64) float64 {
	if a > b {
		return a - b
	}

	return b - a
}
