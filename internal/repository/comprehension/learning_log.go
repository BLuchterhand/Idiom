package comprehension

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"idiom/internal/platform/util"
)

// LearningLogEntry is one record in the per-workspace ranker learning log.
// The log lets every trigger site (eval oracle, CLI, future verified-edit
// hook) leave an auditable trail without coupling them to each other.
type LearningLogEntry struct {
	EventID       string                `json:"event_id,omitempty"`
	Timestamp     string                `json:"timestamp"`
	Source        string                `json:"source"`
	Action        string                `json:"action"` // boost | penalize | reset | restore | no_op
	State         string                `json:"state,omitempty"`
	Reason        string                `json:"reason,omitempty"`
	Score         float64               `json:"score,omitempty"`
	Files         []string              `json:"files,omitempty"`
	TaskID        string                `json:"task_id,omitempty"`
	CorpusID      string                `json:"corpus_id,omitempty"`
	ResultID      string                `json:"result_id,omitempty"`
	Evidence      *LearningEvidenceRef  `json:"evidence,omitempty"`
	BeforeWeights *map[string]float64   `json:"before_weights,omitempty"`
	AfterWeights  *map[string]float64   `json:"after_weights,omitempty"`
	Rollback      *LearningRollbackData               `json:"rollback,omitempty"`
	Verification  *LearningLogVerificationEvidence     `json:"verification,omitempty"`
	Note          string                                `json:"note,omitempty"`
}

// LearningEvidenceRef records sanitized identifiers and numeric metrics that
// justified a learning decision. It intentionally excludes raw prompts,
// transcript text, file bodies, and secret-bearing values.
type LearningEvidenceRef struct {
	Kind             string             `json:"kind,omitempty"`
	Verified         bool               `json:"verified"`
	TaskID           string             `json:"task_id,omitempty"`
	CorpusID         string             `json:"corpus_id,omitempty"`
	ResultID         string             `json:"result_id,omitempty"`
	Files            []string           `json:"files,omitempty"`
	Metrics          map[string]float64 `json:"metrics,omitempty"`
	InvalidPathCount int                `json:"invalid_path_count,omitempty"`
}

// LearningRollbackData carries enough state to restore ranker weights to their
// pre-event value without consulting raw task inputs.
type LearningRollbackData struct {
	Weights map[string]float64 `json:"weights"`
}

// LearningLogVerificationEvidence is a privacy-safe subset of VerificationEvidence
// persisted in the learning log audit trail. It excludes VerificationOutput (which
// may contain raw command output) and keeps only the structured gate predicates:
// which paths were edited, what command verified them, and whether it passed.
// This makes gate decisions reconstructable from the audit log without exposing
// unredacted tool output, file bodies, or secrets.
type LearningLogVerificationEvidence struct {
	// EditPaths are the file paths that were edited and verified.
	EditPaths []string `json:"edit_paths"`
	// VerificationCommand is the command run to verify the edits.
	VerificationCommand string `json:"verification_command"`
	// VerificationPassed is true when the verification command succeeded.
	VerificationPassed bool `json:"verification_passed"`
}

const (
	learningLogFileName = "learning_log.jsonl"

	// LearningLogNotePresent is the only persisted value for a supplied
	// freeform note. The note body may contain prompts, file bodies, or secrets.
	LearningLogNotePresent = "note_present"
)

var learningLogSensitivePathTokens = map[string]struct{}{
	"accesskey":     {},
	"access_key":    {},
	"apikey":        {},
	"api_key":       {},
	"authorization": {},
	"credential":    {},
	"credentials":   {},
	"passwd":        {},
	"password":      {},
	"privatekey":    {},
	"private_key":   {},
	"secret":        {},
	"token":         {},
}

var learningLogSensitivePathCompoundLeads = map[string]struct{}{
	"access":        {},
	"api":           {},
	"auth":          {},
	"authorization": {},
	"credential":    {},
	"credentials":   {},
	"private":       {},
	"secret":        {},
	"token":         {},
}

var learningLogSensitivePathCompoundTails = map[string]struct{}{
	"credential":  {},
	"credentials": {},
	"key":         {},
	"password":    {},
	"passwd":      {},
	"secret":      {},
	"token":       {},
}

func learningLogPath(root string) string {
	return filepath.Join(root, ".idiom", learnedWeightsDirSuffix, learningLogFileName)
}

// AppendLearningLog writes one entry to .idiom/ranker/learning_log.jsonl. The
// shared log is an audit surface, so every entry is sanitized before it is
// persisted. Known token fields are allowlisted enums, correlation identifiers
// are bounded hashes, path/weight fields are reduced to bounded repo-relative
// evidence, freeform notes are reduced to LearningLogNotePresent, and caller
// timestamps must parse as RFC3339/RFC3339Nano or they are regenerated.
func AppendLearningLog(root string, entry LearningLogEntry) error {
	if strings.TrimSpace(entry.Source) == "" {
		return errors.New("learning log entry needs a non-empty source")
	}
	if strings.TrimSpace(entry.Action) == "" {
		return errors.New("learning log entry needs a non-empty action")
	}
	entry = sanitizeLearningLogEntry(entry)
	path := learningLogPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return nil
}

func sanitizeLearningLogEntry(entry LearningLogEntry) LearningLogEntry {
	suppliedEventID := entry.EventID
	entry.EventID = ""
	entry.Timestamp = sanitizeLearningLogTimestamp(entry.Timestamp, time.Now().UTC())
	entry.Source = sanitizeLearningSource(entry.Source)
	entry.Action = normalizeLearningLogAction(entry.Action)
	entry.State = normalizeLearningState(entry.State)
	entry.Reason = sanitizeLearningReason(entry.Reason)
	entry.Files = sanitizeLearningLogPathList(entry.Files)
	entry.TaskID = sanitizeLearningCorrelationID("task", entry.TaskID)
	entry.CorpusID = sanitizeLearningCorrelationID("corpus", entry.CorpusID)
	entry.ResultID = sanitizeLearningCorrelationID("result", entry.ResultID)
	if strings.TrimSpace(entry.Note) != "" {
		entry.Note = LearningLogNotePresent
	}
	if entry.Evidence != nil {
		evidence := sanitizeLearningEvidenceRef(*entry.Evidence)
		entry.Evidence = &evidence
	}
	if entry.BeforeWeights != nil {
		before := sanitizeLearningLogWeights(*entry.BeforeWeights)
		entry.BeforeWeights = &before
	}
	if entry.AfterWeights != nil {
		after := sanitizeLearningLogWeights(*entry.AfterWeights)
		entry.AfterWeights = &after
	}
	if entry.Rollback != nil {
		entry.Rollback = &LearningRollbackData{Weights: sanitizeLearningLogWeights(entry.Rollback.Weights)}
	}
	if entry.Verification != nil {
		v := sanitizeLearningLogVerificationEvidence(*entry.Verification)
		entry.Verification = &v
	}
	entry.EventID = sanitizeLearningEventID(suppliedEventID, entry)
	return entry
}

func sanitizeLearningLogTimestamp(value string, fallback time.Time) string {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return fallback.UTC().Format(time.RFC3339Nano)
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func normalizeLearningLogAction(action string) string {
	switch normalizeLearningAction(action) {
	case "boost":
		return "boost"
	case "penalize":
		return "penalize"
	case "no_op":
		return "no_op"
	}
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "reset":
		return "reset"
	case "restore":
		return "restore"
	default:
		return "no_op"
	}
}

func normalizeLearningState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case LearningStateAccepted:
		return LearningStateAccepted
	case LearningStateRejected:
		return LearningStateRejected
	default:
		return ""
	}
}

func sanitizeLearningEvidenceRef(evidence LearningEvidenceRef) LearningEvidenceRef {
	evidence.Kind = sanitizeLearningEvidenceKind(evidence.Kind)
	evidence.TaskID = sanitizeLearningCorrelationID("task", evidence.TaskID)
	evidence.CorpusID = sanitizeLearningCorrelationID("corpus", evidence.CorpusID)
	evidence.ResultID = sanitizeLearningCorrelationID("result", evidence.ResultID)
	evidence.Files = sanitizeLearningLogPathList(evidence.Files)
	evidence.Metrics = sanitizeLearningMetrics(evidence.Metrics)
	if evidence.InvalidPathCount < 0 {
		evidence.InvalidPathCount = 0
	}
	return evidence
}

// sanitizeLearningLogVerificationEvidence produces a privacy-safe log copy of
// verification evidence. EditPaths are sanitized through the standard path
// pipeline. VerificationCommand is bounded to 256 characters and stripped of
// control characters. VerificationOutput is intentionally excluded.
func sanitizeLearningLogVerificationEvidence(ve LearningLogVerificationEvidence) LearningLogVerificationEvidence {
	ve.EditPaths = sanitizeLearningLogPathList(ve.EditPaths)
	ve.VerificationCommand = sanitizeLearningLogVerificationCommand(ve.VerificationCommand)
	return ve
}

func sanitizeLearningLogVerificationCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	// Strip control characters; keep printable ASCII and common UTF-8.
	cmd = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return -1
		}
		return r
	}, cmd)
	if len(cmd) > 256 {
		cmd = cmd[:256]
	}
	return cmd
}

func sanitizeLearningLogPathList(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		key := sanitizeLearningLogPath(path)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	slices.Sort(out)
	return out
}

func sanitizeLearningLogPath(path string) string {
	key := normalizeWeightKey(path)
	if key == "" || !isBoundedLearningLogPath(key) {
		return ""
	}
	return key
}

func isBoundedLearningLogPath(path string) bool {
	if len(path) > 512 || strings.ContainsAny(path, "\x00\r\n\t") {
		return false
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 128 {
			return false
		}
		if !isSafeLearningLogPathSegment(part) {
			return false
		}
		if isSensitiveLearningLogPathComponent(part) {
			return false
		}
	}
	name := strings.ToLower(parts[len(parts)-1])
	if _, ok := learningLogKnownPathBasenames[name]; ok {
		return true
	}
	_, ok := learningLogAllowedFileExtensions[strings.ToLower(filepath.Ext(name))]
	return ok
}

func isSafeLearningLogPathSegment(segment string) bool {
	for i := 0; i < len(segment); i++ {
		c := segment[i]
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= 'A' && c <= 'Z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		switch c {
		case '.', '_', '-', '+', '@':
			continue
		default:
			return false
		}
	}
	return true
}

func isSensitiveLearningLogPathComponent(component string) bool {
	clean := strings.TrimSpace(component)
	candidates := []string{clean}
	if ext := filepath.Ext(clean); ext != "" {
		candidates = append(candidates, strings.TrimSuffix(candidates[0], ext))
	}
	for _, candidate := range candidates {
		if isSensitiveLearningLogPathToken(candidate) {
			return true
		}
	}
	return false
}

func isSensitiveLearningLogPathToken(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), ".")
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	switch {
	case strings.HasPrefix(lower, "ghp_"),
		strings.HasPrefix(lower, "gho_"),
		strings.HasPrefix(lower, "ghu_"),
		strings.HasPrefix(lower, "ghs_"),
		strings.HasPrefix(lower, "ghr_"),
		strings.HasPrefix(lower, "github_pat_"),
		strings.HasPrefix(lower, "sk_live_"),
		strings.HasPrefix(lower, "sk_test_"),
		strings.HasPrefix(lower, "sk_proj_"):
		return true
	}
	if _, ok := learningLogSensitivePathTokens[lower]; ok {
		return true
	}
	parts := learningLogPathTokenParts(value)
	if hasSensitiveLearningLogPathCompound(parts) {
		return true
	}
	for _, part := range parts {
		if _, ok := learningLogSensitivePathTokens[part]; ok {
			return true
		}
	}
	return false
}

func learningLogPathTokenParts(value string) []string {
	fields := strings.FieldsFunc(value, isLearningLogPathTokenSeparator)
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		for _, part := range splitLearningLogCamelToken(field) {
			part = strings.ToLower(strings.TrimSpace(part))
			if part == "" {
				continue
			}
			parts = append(parts, part)
		}
	}
	return parts
}

func splitLearningLogCamelToken(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var parts []string
	start := 0
	for i := 1; i < len(value); i++ {
		if isLearningLogCamelBoundary(value, i) {
			parts = append(parts, value[start:i])
			start = i
		}
	}
	parts = append(parts, value[start:])
	return parts
}

func isLearningLogCamelBoundary(value string, index int) bool {
	if index <= 0 || index >= len(value) || !isASCIILearningLogUpper(value[index]) {
		return false
	}
	prev := value[index-1]
	if isASCIILearningLogLower(prev) || isASCIILearningLogDigit(prev) {
		return true
	}
	nextIndex := index + 1
	return isASCIILearningLogUpper(prev) &&
		nextIndex < len(value) &&
		isASCIILearningLogLower(value[nextIndex])
}

func isASCIILearningLogLower(c byte) bool {
	return c >= 'a' && c <= 'z'
}

func isASCIILearningLogUpper(c byte) bool {
	return c >= 'A' && c <= 'Z'
}

func isASCIILearningLogDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func hasSensitiveLearningLogPathCompound(parts []string) bool {
	if isCollapsedSensitiveLearningLogPathCompound(strings.Join(parts, "")) {
		return true
	}
	for i := 0; i+1 < len(parts); i++ {
		if isSensitiveLearningLogPathCompoundPair(parts[i], parts[i+1]) {
			return true
		}
	}
	return false
}

func isSensitiveLearningLogPathCompoundPair(left string, right string) bool {
	if _, ok := learningLogSensitivePathCompoundLeads[left]; !ok {
		return false
	}
	_, ok := learningLogSensitivePathCompoundTails[right]
	return ok
}

func isCollapsedSensitiveLearningLogPathCompound(value string) bool {
	if value == "" {
		return false
	}
	for left := range learningLogSensitivePathCompoundLeads {
		for right := range learningLogSensitivePathCompoundTails {
			if value == left+right {
				return true
			}
		}
	}
	return false
}

func isLearningLogPathTokenSeparator(r rune) bool {
	switch r {
	case '.', '_', '-', '+', '@', ':':
		return true
	default:
		return false
	}
}

func sanitizeLearningLogWeights(weights map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(weights))
	for key, value := range weights {
		if value != value {
			continue
		}
		clean := sanitizeLearningLogWeightKey(key)
		if clean == "" {
			continue
		}
		out[clean] = value
	}
	return out
}

func sanitizeLearningLogWeightKey(key string) string {
	if path := sanitizeLearningLogPath(key); path != "" {
		return path
	}
	feature := normalizeLearningLogToken(key)
	if _, ok := learningLogAllowedWeightFeatureKeys[feature]; ok {
		return feature
	}
	return ""
}

func sanitizeLearningEventID(value string, entry LearningLogEntry) string {
	clean := strings.ToLower(strings.TrimSpace(value))
	if clean == "" {
		return ""
	}
	if learningGeneratedEventIDPattern.MatchString(clean) {
		return clean
	}
	return learningEventID(entry)
}

func sanitizeLearningSource(value string) string {
	clean := normalizeLearningLogToken(value)
	if _, ok := learningLogAllowedSources[clean]; ok {
		return clean
	}
	return "unknown"
}

func sanitizeLearningReason(value string) string {
	clean := normalizeLearningLogToken(value)
	if clean == "" {
		return ""
	}
	if _, ok := learningLogAllowedReasons[clean]; ok {
		return clean
	}
	return "unknown"
}

func sanitizeLearningEvidenceKind(value string) string {
	clean := normalizeLearningLogToken(value)
	if clean == "" {
		return ""
	}
	if _, ok := learningLogAllowedEvidenceKinds[clean]; ok {
		return clean
	}
	return "unknown"
}

func sanitizeLearningMetricKey(value string) string {
	clean := normalizeLearningLogToken(value)
	if clean == "" {
		return ""
	}
	if _, ok := learningLogAllowedMetricKeys[clean]; ok {
		return clean
	}
	return "unknown"
}

func sanitizeLearningCorrelationID(prefix string, value string) string {
	clean := strings.ToLower(strings.TrimSpace(value))
	if clean == "" {
		return ""
	}
	if learningHashedIDPatterns[prefix].MatchString(clean) {
		return clean
	}
	return hashedLearningLogID(prefix, value)
}

func hashedLearningLogID(prefix string, value string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + strings.TrimSpace(value)))
	return prefix + "_" + hex.EncodeToString(sum[:8])
}

func normalizeLearningLogToken(value string) string {
	clean := strings.TrimSpace(strings.ToLower(value))
	clean = strings.NewReplacer(" ", "_", "/", "_").Replace(clean)
	if !learningSafeTokenPattern.MatchString(clean) {
		return ""
	}
	return clean
}

// ReadLearningLog returns every entry in the log, oldest first, dropping
// malformed lines. Missing log returns an empty slice with no error.
func ReadLearningLog(root string) ([]LearningLogEntry, error) {
	path := learningLogPath(root)
	var out []LearningLogEntry
	err := util.ScanFile(path, func(raw json.RawMessage) error {
		var entry LearningLogEntry
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil
		}
		out = append(out, entry)
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return out, err
	}
	return out, nil
}

// LearningLogSummary aggregates the log into the shape the harness audit
// surfaces. It is deliberately narrow: counts, the most recent entry per
// source, and the distinct sources observed so the audit can see which
// trigger sites are wired versus dormant.
type LearningLogSummary struct {
	TotalEntries         int                `json:"total_entries"`
	TotalEvents          int                `json:"total_events"`
	BoostCount           int                `json:"boost_count"`
	PenalizeCount        int                `json:"penalize_count"`
	NoOpCount            int                `json:"no_op_count"`
	ResetCount           int                `json:"reset_count"`
	RestoreCount         int                `json:"restore_count"`
	AcceptedCount        int                `json:"accepted_count"`
	RejectedCount        int                `json:"rejected_count"`
	BySource             map[string]int     `json:"by_source,omitempty"`
	StateCounts          map[string]int     `json:"state_counts,omitempty"`
	ReasonCounts         map[string]int     `json:"reason_counts,omitempty"`
	AcceptedReasonCounts map[string]int     `json:"accepted_reason_counts,omitempty"`
	RejectedReasonCounts map[string]int     `json:"rejected_reason_counts,omitempty"`
	LatestPerSource      map[string]string  `json:"latest_per_source,omitempty"`
	LatestEntry          *LearningLogEntry  `json:"latest_entry,omitempty"`
	Recent               []LearningLogEntry `json:"recent,omitempty"`
}

// SummarizeLearningLog folds the log into a small summary for downstream
// reporting. The "recent" slice keeps the last 10 entries newest-first.
func SummarizeLearningLog(entries []LearningLogEntry) LearningLogSummary {
	summary := LearningLogSummary{
		BySource:             make(map[string]int),
		StateCounts:          make(map[string]int),
		ReasonCounts:         make(map[string]int),
		AcceptedReasonCounts: make(map[string]int),
		RejectedReasonCounts: make(map[string]int),
		LatestPerSource:      make(map[string]string),
	}
	for i := range entries {
		entry := entries[i]
		summary.TotalEntries++
		if entry.EventID != "" || entry.State != "" {
			summary.TotalEvents++
		}
		switch entry.Action {
		case "boost":
			summary.BoostCount++
		case "penalize":
			summary.PenalizeCount++
		case "reset":
			summary.ResetCount++
		case "restore":
			summary.RestoreCount++
		default:
			summary.NoOpCount++
		}
		state := strings.TrimSpace(entry.State)
		reason := strings.TrimSpace(entry.Reason)
		if state != "" {
			summary.StateCounts[state]++
			switch state {
			case "accepted":
				summary.AcceptedCount++
				if reason != "" {
					summary.AcceptedReasonCounts[reason]++
				}
			case "rejected":
				summary.RejectedCount++
				if reason != "" {
					summary.RejectedReasonCounts[reason]++
				}
			}
		}
		if reason != "" {
			summary.ReasonCounts[reason]++
		}
		if entry.Source != "" {
			summary.BySource[entry.Source]++
			if existing, ok := summary.LatestPerSource[entry.Source]; !ok || entry.Timestamp > existing {
				summary.LatestPerSource[entry.Source] = entry.Timestamp
			}
		}
		if summary.LatestEntry == nil || entry.Timestamp > summary.LatestEntry.Timestamp {
			copy := entry
			summary.LatestEntry = &copy
		}
	}
	// Newest-first recent slice; cap at 10.
	sorted := append([]LearningLogEntry(nil), entries...)
	slices.SortFunc(sorted, func(a, b LearningLogEntry) int {
		return -cmp.Compare(a.Timestamp, b.Timestamp)
	})
	if len(sorted) > 10 {
		sorted = sorted[:10]
	}
	summary.Recent = sorted
	return summary
}
