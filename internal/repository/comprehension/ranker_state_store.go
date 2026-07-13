package comprehension

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"idiom/internal/platform/atomicfile"
)

const (
	rankerStateDirectoryPath            = ".idiom/ranker"
	rankerStatePendingPath              = ".idiom/ranker/pending.json"
	rankerStateWeightsPath              = ".idiom/ranker/weights.json"
	rankerStateLearningLogPath          = ".idiom/ranker/learning_log.jsonl"
	rankerStateJournalVersion           = 1
	rankerStateMaxMetadataBytes         = 1 << 20
	rankerStateMaxLearningLogBytes      = 16 << 20
	rankerStateMaxLearningLogEntryBytes = 256 << 10
	rankerStateMaxLearningLogEntries    = 100_000

	rankerStateHookAfterPending                = "after_pending_sync"
	rankerStateHookAfterLogCommit              = "after_log_commit"
	rankerStateHookAfterMaterialize            = "after_weights_materialize"
	rankerStateHookBeforePending               = "before_pending_publish"
	rankerStateHookBeforeLogAppend             = "before_log_append"
	rankerStateHookBeforeLogSync               = "before_log_sync"
	rankerStateHookBeforeLogDirectorySync      = "before_log_directory_sync"
	rankerStateHookBeforeRecoveryDirectorySync = "before_recovery_directory_sync"
	rankerStateHookBeforeLogTruncate           = "before_log_truncate"
	rankerStateHookBeforeWeights               = "before_weights_publish"
	rankerStateHookBeforeWeightDelete          = "before_weights_delete"
	rankerStateHookBeforePendingDelete         = "before_pending_delete"
	rankerStateHookProcessLockBlocked          = "process_lock_blocked"
)

type rankerStateStore struct {
	ctx                     context.Context
	canonicalRoot           string
	root                    *os.Root
	rootIdentity            os.FileInfo
	rankerDirectoryIdentity os.FileInfo
	lock                    *rankerStateLock
	closeOnce               sync.Once
}

type rankerStateChange struct {
	entry           LearningLogEntry
	before          map[string]float64
	after           map[string]float64
	materialize     bool
	captureSnapshot bool
}

// RankerStateSnapshot is one lock-consistent view of learned weights and their
// canonical audit history. Generation changes whenever canonical state content
// changes.
type RankerStateSnapshot struct {
	Generation           string `json:"generation,omitempty"`
	WeightsMaterialized  bool   `json:"weights_materialized"`
	LearningLogPublished bool   `json:"learning_log_published"`
	// Weights and LearningLog are ephemeral owner-isolated copies. They are
	// excluded from JSON; mutating them never updates the persisted authority.
	Weights     map[string]float64 `json:"-"`
	LearningLog []LearningLogEntry `json:"-"`
}

// LearnedWeights returns an isolated copy of the ephemeral path-keyed weights.
// Callers that need a durable or serializable view should use AuditSummary.
func (snapshot RankerStateSnapshot) LearnedWeights() map[string]float64 {
	return cloneWeights(snapshot.Weights)
}

// LearningEntries returns an isolated copy of the ephemeral canonical history.
// Callers that need a durable or serializable view should use AuditSummary.
func (snapshot RankerStateSnapshot) LearningEntries() []LearningLogEntry {
	return cloneLearningLogEntries(snapshot.LearningLog)
}

// RankerStatePostCommitError reports that the canonical learning event crossed
// the synced-log commit point even though later materialization or cleanup did
// not complete. Callers must not blindly repeat it with a new request identity.
type RankerStatePostCommitError struct {
	RequestID string
	EventID   string
	Err       error
}

// RankerStateAmbiguousCommitError reports that the complete canonical event
// reached the learning log but its durability sync did not complete. Recovery
// owns the final disposition, so callers must retry with the same RequestID.
type RankerStateAmbiguousCommitError struct {
	RequestID string
	EventID   string
	Err       error
}

func (err *RankerStateAmbiguousCommitError) Error() string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("ranker request %s published as %s with ambiguous durability; retry the same request identity: %v", err.RequestID, err.EventID, err.Err)
}

func (err *RankerStateAmbiguousCommitError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func (err *RankerStateAmbiguousCommitError) RecoveryRequired() bool {
	return err != nil
}

type rankerLogPublicationPhase uint8

const (
	rankerLogPublicationNone rankerLogPublicationPhase = iota
	rankerLogPublicationPartial
	rankerLogPublicationComplete
	rankerLogPublicationFileSynced
	rankerLogPublicationDurable
)

func (err *RankerStatePostCommitError) Error() string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("ranker request %s committed as %s: %v", err.RequestID, err.EventID, err.Err)
}

func (err *RankerStatePostCommitError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Err
}

func (err *RankerStatePostCommitError) Committed() bool {
	return err != nil
}

type rankerPendingTransaction struct {
	Version         int                `json:"version"`
	Event           LearningLogEntry   `json:"event"`
	BeforeWeights   map[string]float64 `json:"before_weights"`
	AfterWeights    map[string]float64 `json:"after_weights"`
	Materialize     bool               `json:"materialize"`
	CaptureSnapshot bool               `json:"capture_snapshot"`
	LogOffset       int64              `json:"log_offset"`
	LogPrefixSHA256 string             `json:"log_prefix_sha256"`
}

type rankerPendingTransactionWire struct {
	Version         *int                `json:"version"`
	Event           *LearningLogEntry   `json:"event"`
	BeforeWeights   *map[string]float64 `json:"before_weights"`
	AfterWeights    *map[string]float64 `json:"after_weights"`
	Materialize     *bool               `json:"materialize"`
	CaptureSnapshot *bool               `json:"capture_snapshot"`
	LogOffset       *int64              `json:"log_offset"`
	LogPrefixSHA256 *string             `json:"log_prefix_sha256"`
}

var rankerStateTestHooks = struct {
	sync.RWMutex
	byRoot map[string]func(string) error
}{byRoot: make(map[string]func(string) error)}

func openRankerStateStore(ctx context.Context, workspaceRoot string) (*rankerStateStore, error) {
	if ctx == nil {
		return nil, errors.New("ranker state context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	canonicalRoot, err := canonicalRankerStateRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	pathIdentity, err := os.Stat(canonicalRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect canonical ranker workspace root: %w", err)
	}

	root, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return nil, fmt.Errorf("open ranker workspace root: %w", err)
	}
	openedIdentity, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("inspect open ranker workspace root: %w", err)
	}
	if !pathIdentity.IsDir() || !openedIdentity.IsDir() || !os.SameFile(pathIdentity, openedIdentity) {
		_ = root.Close()
		return nil, errors.New("ranker workspace root identity changed while opening it")
	}
	store := &rankerStateStore{
		ctx:           ctx,
		canonicalRoot: canonicalRoot,
		root:          root,
		rootIdentity:  openedIdentity,
	}
	rankerDirectoryIdentity, err := createAndInspectRankerStateDirectory(root)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	store.rankerDirectoryIdentity = rankerDirectoryIdentity
	if err := store.syncRankerStateDirectoriesLocked(); err != nil {
		_ = root.Close()
		return nil, err
	}

	lock, err := acquireRankerStateLock(ctx, root, rankerStateRootLockKey(canonicalRoot), func() error {
		return runRankerStateTestHook(canonicalRoot, rankerStateHookProcessLockBlocked)
	})
	if err != nil {
		_ = root.Close()
		return nil, err
	}

	store.lock = lock
	if err := store.checkContextLocked(); err != nil {
		store.Close()
		return nil, err
	}
	if err := store.validateRootIdentityLocked(); err != nil {
		store.Close()
		return nil, err
	}

	return store, nil
}

func createAndInspectRankerStateDirectory(root *os.Root) (os.FileInfo, error) {
	for _, directory := range []string{".idiom", rankerStateDirectoryPath} {
		if err := root.Mkdir(directory, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create ranker state directory %q: %w", directory, err)
		}
		info, err := root.Lstat(directory)
		if err != nil {
			return nil, fmt.Errorf("inspect ranker state directory %q: %w", directory, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, fmt.Errorf("ranker state directory %q must be a non-symlink directory", directory)
		}
	}

	info, err := root.Lstat(rankerStateDirectoryPath)
	if err != nil {
		return nil, fmt.Errorf("inspect ranker state directory identity: %w", err)
	}
	return info, nil
}

func canonicalRankerStateRoot(workspaceRoot string) (string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return "", errors.New("workspace root is required for ranker state")
	}

	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve ranker workspace root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve canonical ranker workspace root: %w", err)
	}
	info, err := os.Stat(canonicalRoot)
	if err != nil {
		return "", fmt.Errorf("inspect ranker workspace root: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("ranker workspace root must be a directory")
	}
	return canonicalRoot, nil
}

func rankerStateRootLockKey(canonicalRoot string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(canonicalRoot)
	}

	return canonicalRoot
}

func (store *rankerStateStore) Close() {
	if store == nil {
		return
	}
	store.closeOnce.Do(func() {
		store.lock.Close()
		_ = store.root.Close()
	})
}

// LoadRankerStateSnapshot recovers interrupted state, then loads weights and
// learning history while holding one in-process and cross-process lock.
func LoadRankerStateSnapshot(ctx context.Context, root string) (RankerStateSnapshot, error) {
	store, err := openRankerStateStore(ctx, root)
	if err != nil {
		return RankerStateSnapshot{}, err
	}
	defer store.Close()
	if err := store.recoverLocked(); err != nil {
		return RankerStateSnapshot{}, err
	}
	weights, rawLog, entries, err := store.loadConsistentRankerStateLocked()
	if err != nil {
		return RankerStateSnapshot{}, err
	}
	return RankerStateSnapshot{
		Generation:           rankerStateGeneration(weights, rawLog),
		WeightsMaterialized:  len(weights) > 0,
		LearningLogPublished: len(rawLog) > 0,
		Weights:              cloneWeights(weights),
		LearningLog:          cloneLearningLogEntries(entries),
	}, nil
}

func cloneLearningLogEntries(entries []LearningLogEntry) []LearningLogEntry {
	if entries == nil {
		return nil
	}
	out := make([]LearningLogEntry, len(entries))
	for index := range entries {
		out[index] = cloneLearningLogEntry(entries[index])
	}
	return out
}

func cloneLearningLogEntry(entry LearningLogEntry) LearningLogEntry {
	entry.Files = append([]string(nil), entry.Files...)
	if entry.Evidence != nil {
		evidence := *entry.Evidence
		evidence.Files = append([]string(nil), evidence.Files...)
		evidence.Metrics = cloneFloat64Map(evidence.Metrics)
		entry.Evidence = &evidence
	}
	if entry.BeforeWeights != nil {
		weights := cloneWeights(*entry.BeforeWeights)
		entry.BeforeWeights = &weights
	}
	if entry.AfterWeights != nil {
		weights := cloneWeights(*entry.AfterWeights)
		entry.AfterWeights = &weights
	}
	if entry.Rollback != nil {
		rollback := *entry.Rollback
		rollback.Weights = cloneWeights(rollback.Weights)
		entry.Rollback = &rollback
	}
	if entry.Verification != nil {
		verification := *entry.Verification
		verification.EditPaths = append([]string(nil), verification.EditPaths...)
		entry.Verification = &verification
	}
	return entry
}

func cloneFloat64Map(values map[string]float64) map[string]float64 {
	if values == nil {
		return nil
	}
	out := make(map[string]float64, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func rankerStateGeneration(weights map[string]float64, rawLog []byte) string {
	if len(weights) == 0 && len(rawLog) == 0 {
		return ""
	}
	encodedWeights, err := json.Marshal(canonicalLearnedWeights(weights))
	if err != nil {
		return ""
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte("ranker_state\x00"))
	_, _ = digest.Write(encodedWeights)
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(rawLog)

	return hex.EncodeToString(digest.Sum(nil)[:16])
}

func (store *rankerStateStore) recoverLocked() error {
	if err := store.checkContextLocked(); err != nil {
		return err
	}
	if err := store.validateRootIdentityLocked(); err != nil {
		return err
	}
	rawPending, err := store.readRegularFileLocked(rankerStatePendingPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read pending ranker transaction: %w", err)
	}

	pending, err := parseRankerPendingTransaction(rawPending)
	if err != nil {
		return fmt.Errorf("parse pending ranker transaction: %w", err)
	}
	if err := store.checkContextLocked(); err != nil {
		return err
	}
	line, err := encodeCanonicalLearningLogEntry(pending.Event)
	if err != nil {
		return fmt.Errorf("encode pending ranker event: %w", err)
	}

	logBytes, err := store.readLearningLogBytesLocked()
	if err != nil {
		return err
	}
	if pending.LogOffset > int64(len(logBytes)) {
		return errors.New("pending ranker transaction log prefix is shorter than recorded offset")
	}
	prefix := logBytes[:pending.LogOffset]
	prefixHash := sha256.Sum256(prefix)
	if hex.EncodeToString(prefixHash[:]) != pending.LogPrefixSHA256 {
		return errors.New("pending ranker transaction log prefix does not match its journal")
	}
	prefixEntries, err := parseCanonicalLearningLog(prefix)
	if err != nil {
		return fmt.Errorf("validate pending ranker transaction log prefix: %w", err)
	}
	if err := store.checkContextLocked(); err != nil {
		return err
	}
	prefixWeights, err := canonicalLearningLogFinalWeights(prefixEntries)
	if err != nil {
		return fmt.Errorf("validate pending ranker transaction log state: %w", err)
	}
	if !weightsEqual(prefixWeights, pending.BeforeWeights) {
		return errors.New("pending ranker transaction before weights do not match its canonical log prefix")
	}
	suffix := logBytes[pending.LogOffset:]
	exactEvent := bytes.Equal(suffix, line)
	if exactEvent {
		if err := store.syncLearningLogLocked(logBytes); err != nil {
			return rankerAmbiguousCommitError(pending.Event, fmt.Errorf("re-establish ranker learning event durability: %w", err))
		}
	}
	current, err := store.loadWeightsLocked()
	if err != nil {
		if exactEvent {
			return rankerCommittedError(pending.Event, fmt.Errorf("validate committed ranker weights before recovery: %w", err))
		}
		return fmt.Errorf("validate ranker weights before recovery: %w", err)
	}
	switch {
	case exactEvent:
		if !weightsEqual(current, pending.BeforeWeights) && !weightsEqual(current, pending.AfterWeights) {
			return rankerCommittedError(pending.Event, errors.New("committed ranker weights differ from both journal snapshots"))
		}
		if pending.Materialize {
			if !weightsEqual(current, pending.AfterWeights) {
				err = store.writeWeightsLocked(pending.AfterWeights)
			}
			if err != nil {
				return rankerCommittedError(pending.Event, fmt.Errorf("roll forward committed ranker weights: %w", err))
			}
		}
	case len(suffix) == 0 || (len(suffix) < len(line) && bytes.Equal(suffix, line[:len(suffix)])):
		if len(suffix) > 0 {
			if err := store.truncateLearningLogLocked(pending.LogOffset); err != nil {
				return fmt.Errorf("truncate partial ranker log event: %w", err)
			}
		}
		if !weightsEqual(current, pending.BeforeWeights) && !weightsEqual(current, pending.AfterWeights) {
			return errors.New("uncommitted ranker weights differ from both journal snapshots")
		}
		if pending.Materialize {
			if !weightsEqual(current, pending.BeforeWeights) {
				err = store.writeWeightsLocked(pending.BeforeWeights)
			}
			if err != nil {
				return fmt.Errorf("restore uncommitted ranker weights: %w", err)
			}
		}
	default:
		return errors.New("pending ranker transaction log suffix is neither its exact event nor a partial event")
	}

	if err := store.clearPendingLocked(); err != nil {
		if bytes.Equal(suffix, line) {
			return rankerCommittedError(pending.Event, fmt.Errorf("clear recovered ranker transaction: %w", err))
		}
		return fmt.Errorf("clear recovered ranker transaction: %w", err)
	}

	return nil
}

func (store *rankerStateStore) commitLocked(change rankerStateChange) (LearningLogEntry, bool, error) {
	before := canonicalLearnedWeights(change.before)
	after := canonicalLearnedWeights(change.after)
	entry := change.entry
	if change.captureSnapshot {
		beforeCopy := cloneWeights(before)
		afterCopy := cloneWeights(after)
		entry.BeforeWeights = &beforeCopy
		entry.AfterWeights = &afterCopy
		entry.Rollback = &LearningRollbackData{Weights: cloneWeights(before)}
	}

	canonicalEntry, line, err := canonicalLearningDecision(entry)
	if err != nil {
		return LearningLogEntry{}, false, err
	}
	logBytes, err := store.readLearningLogBytesLocked()
	if err != nil {
		return LearningLogEntry{}, false, err
	}
	entries, err := parseCanonicalLearningLog(logBytes)
	if err != nil {
		return LearningLogEntry{}, false, fmt.Errorf("validate ranker learning log before append: %w", err)
	}
	if err := store.checkContextLocked(); err != nil {
		return LearningLogEntry{}, false, err
	}
	logWeights, err := canonicalLearningLogFinalWeights(entries)
	if err != nil {
		return LearningLogEntry{}, false, fmt.Errorf("validate ranker learning state before append: %w", err)
	}
	if !weightsEqual(logWeights, before) {
		return LearningLogEntry{}, false, errors.New("ranker weights do not match the canonical learning log before append")
	}
	for _, existing := range entries {
		if existing.RequestID != canonicalEntry.RequestID {
			continue
		}
		if learningRequestFingerprint(existing) != learningRequestFingerprint(canonicalEntry) {
			return LearningLogEntry{}, false, fmt.Errorf("ranker request id %q was already used for different canonical content", canonicalEntry.RequestID)
		}
		return existing, true, nil
	}

	prefixHash := sha256.Sum256(logBytes)
	pending := rankerPendingTransaction{
		Version:         rankerStateJournalVersion,
		Event:           canonicalEntry,
		BeforeWeights:   before,
		AfterWeights:    after,
		Materialize:     change.materialize,
		CaptureSnapshot: change.captureSnapshot,
		LogOffset:       int64(len(logBytes)),
		LogPrefixSHA256: hex.EncodeToString(prefixHash[:]),
	}
	if err := store.writePendingLocked(pending); err != nil {
		return canonicalEntry, false, fmt.Errorf("persist pending ranker transaction: %w", rankerStatePreCommitCause(err))
	}
	if err := store.runTestHook(rankerStateHookAfterPending); err != nil {
		return canonicalEntry, false, err
	}
	publication, err := store.appendLearningLogLocked(pending.LogOffset, line)
	if err != nil {
		switch publication {
		case rankerLogPublicationComplete, rankerLogPublicationFileSynced:
			return canonicalEntry, false, rankerAmbiguousCommitError(canonicalEntry, fmt.Errorf("finish ranker learning event publication: %w", err))
		case rankerLogPublicationDurable:
			return canonicalEntry, false, rankerCommittedError(canonicalEntry, fmt.Errorf("finish committed ranker learning event: %w", err))
		}
		return canonicalEntry, false, fmt.Errorf("commit ranker learning event: %w", err)
	}
	if err := store.runTestHook(rankerStateHookAfterLogCommit); err != nil {
		return canonicalEntry, false, rankerCommittedError(canonicalEntry, err)
	}
	if change.materialize {
		if err := store.writeWeightsLocked(after); err != nil {
			return canonicalEntry, false, rankerCommittedError(canonicalEntry, fmt.Errorf("materialize committed ranker weights: %w", err))
		}
	}
	if err := store.runTestHook(rankerStateHookAfterMaterialize); err != nil {
		return canonicalEntry, false, rankerCommittedError(canonicalEntry, err)
	}
	if err := store.clearPendingLocked(); err != nil {
		return canonicalEntry, false, rankerCommittedError(canonicalEntry, fmt.Errorf("clear committed ranker transaction: %w", err))
	}

	return canonicalEntry, false, nil
}

func rankerCommittedError(entry LearningLogEntry, err error) error {
	return &RankerStatePostCommitError{RequestID: entry.RequestID, EventID: entry.EventID, Err: err}
}

func rankerAmbiguousCommitError(entry LearningLogEntry, err error) error {
	return &RankerStateAmbiguousCommitError{RequestID: entry.RequestID, EventID: entry.EventID, Err: err}
}

func rankerStatePreCommitCause(err error) error {
	var postCommit *atomicfile.PostCommitError
	if errors.As(err, &postCommit) && postCommit != nil && postCommit.Err != nil {
		return postCommit.Err
	}
	return err
}

func (store *rankerStateStore) loadWeightsLocked() (map[string]float64, error) {
	raw, err := store.readRegularFileLocked(rankerStateWeightsPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]float64{}, nil
		}
		return nil, err
	}

	parsed, err := ParsePersistedLearnedWeights(raw)
	if err != nil {
		return nil, fmt.Errorf("parse persisted learned weights: %w", err)
	}

	return parsed, nil
}

func (store *rankerStateStore) loadConsistentRankerStateLocked() (map[string]float64, []byte, []LearningLogEntry, error) {
	weights, err := store.loadWeightsLocked()
	if err != nil {
		return nil, nil, nil, err
	}
	rawLog, err := store.readLearningLogBytesLocked()
	if err != nil {
		return nil, nil, nil, err
	}
	entries, err := parseCanonicalLearningLog(rawLog)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := store.checkContextLocked(); err != nil {
		return nil, nil, nil, err
	}
	expected, err := canonicalLearningLogFinalWeights(entries)
	if err != nil {
		return nil, nil, nil, err
	}
	if !weightsEqual(weights, expected) {
		return nil, nil, nil, errors.New("persisted ranker weights do not match the canonical learning log")
	}

	return weights, rawLog, entries, nil
}

func (store *rankerStateStore) writeWeightsLocked(weights map[string]float64) error {
	weights = canonicalLearnedWeights(weights)
	if len(weights) == 0 {
		if err := store.validateRegularFileIfPresentLocked(rankerStateWeightsPath); err != nil {
			return err
		}
		if err := store.beforeRankerPublicationLocked(rankerStateHookBeforeWeightDelete); err != nil {
			return err
		}
		if err := store.root.Remove(rankerStateWeightsPath); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if err := store.validateRootIdentityLocked(); err != nil {
			return err
		}
		return store.syncRankerStateDirectoriesLocked()
	}

	encoded, err := json.MarshalIndent(weights, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	if err := atomicfile.WriteFileRootChecked(store.root, rankerStateWeightsPath, encoded, 0o644, func() error {
		return store.beforeRankerPublicationLocked(rankerStateHookBeforeWeights)
	}); err != nil {
		return err
	}

	return store.validateRootIdentityLocked()
}

func (store *rankerStateStore) writePendingLocked(pending rankerPendingTransaction) error {
	encoded, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if _, err := parseRankerPendingTransaction(encoded); err != nil {
		return fmt.Errorf("validate pending ranker transaction before publication: %w", err)
	}

	if err := atomicfile.WriteFileRootChecked(store.root, rankerStatePendingPath, encoded, 0o600, func() error {
		return store.beforeRankerPublicationLocked(rankerStateHookBeforePending)
	}); err != nil {
		return err
	}

	return store.validateRootIdentityLocked()
}

func (store *rankerStateStore) clearPendingLocked() error {
	if err := store.validateRegularFileIfPresentLocked(rankerStatePendingPath); err != nil {
		return err
	}
	if err := store.beforeRankerPublicationLocked(rankerStateHookBeforePendingDelete); err != nil {
		return err
	}
	if err := store.root.Remove(rankerStatePendingPath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := store.validateRootIdentityLocked(); err != nil {
		return err
	}

	return store.syncRankerStateDirectoriesLocked()
}

func (store *rankerStateStore) readLearningLogBytesLocked() ([]byte, error) {
	raw, err := store.readRegularFileLocked(rankerStateLearningLogPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read ranker learning log: %w", err)
	}

	return raw, nil
}

func (store *rankerStateStore) appendLearningLogLocked(offset int64, line []byte) (rankerLogPublicationPhase, error) {
	if err := store.validateRegularFileIfPresentLocked(rankerStateLearningLogPath); err != nil {
		return rankerLogPublicationNone, err
	}
	if err := store.beforeRankerPublicationLocked(rankerStateHookBeforeLogAppend); err != nil {
		return rankerLogPublicationNone, err
	}
	file, err := store.root.OpenFile(rankerStateLearningLogPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return rankerLogPublicationNone, err
	}
	closeFile := true
	defer func() {
		if closeFile {
			_ = file.Close()
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return rankerLogPublicationNone, err
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return rankerLogPublicationNone, err
	}
	if info.Size() != offset {
		return rankerLogPublicationNone, fmt.Errorf("ranker learning log size changed before append: got %d, want %d", info.Size(), offset)
	}
	if err := store.validateRootIdentityLocked(); err != nil {
		return rankerLogPublicationNone, err
	}
	written, err := writeFull(file, line)
	if err != nil {
		if written == len(line) {
			return rankerLogPublicationComplete, err
		}
		if written > 0 {
			return rankerLogPublicationPartial, err
		}
		return rankerLogPublicationNone, err
	}
	if err := store.checkContextLocked(); err != nil {
		return rankerLogPublicationComplete, err
	}
	if err := store.runTestHook(rankerStateHookBeforeLogSync); err != nil {
		return rankerLogPublicationComplete, err
	}
	if err := store.checkContextLocked(); err != nil {
		return rankerLogPublicationComplete, err
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return rankerLogPublicationComplete, err
	}
	if err := store.checkContextLocked(); err != nil {
		return rankerLogPublicationComplete, err
	}
	if err := file.Sync(); err != nil {
		return rankerLogPublicationComplete, fmt.Errorf("sync ranker learning log: %w", err)
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return rankerLogPublicationFileSynced, err
	}
	if err := store.validateRootIdentityLocked(); err != nil {
		return rankerLogPublicationFileSynced, err
	}
	if err := file.Close(); err != nil {
		closeFile = false
		return rankerLogPublicationFileSynced, fmt.Errorf("close ranker learning log: %w", err)
	}
	closeFile = false

	if err := store.runTestHook(rankerStateHookBeforeLogDirectorySync); err != nil {
		return rankerLogPublicationFileSynced, err
	}
	if err := store.checkContextLocked(); err != nil {
		return rankerLogPublicationFileSynced, err
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return rankerLogPublicationFileSynced, err
	}
	if err := store.syncRankerStateDirectoriesLocked(); err != nil {
		return rankerLogPublicationFileSynced, err
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return rankerLogPublicationDurable, err
	}

	return rankerLogPublicationDurable, nil
}

func (store *rankerStateStore) syncLearningLogLocked(expected []byte) error {
	if err := store.validateRegularFileIfPresentLocked(rankerStateLearningLogPath); err != nil {
		return err
	}
	file, err := store.root.OpenFile(rankerStateLearningLogPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	closeFile := true
	defer func() {
		if closeFile {
			_ = file.Close()
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return err
	}
	if info.Size() != int64(len(expected)) {
		return fmt.Errorf("ranker learning log size changed before durability recovery: got %d, want %d", info.Size(), len(expected))
	}
	current, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("read ranker learning log before durability recovery: %w", err)
	}
	if !bytes.Equal(current, expected) {
		return errors.New("ranker learning log changed before durability recovery")
	}
	if err := store.checkContextLocked(); err != nil {
		return err
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return err
	}
	if err := store.validateRootIdentityLocked(); err != nil {
		return err
	}
	if err := store.checkContextLocked(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync recovered ranker learning log: %w", err)
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return err
	}
	if err := store.validateRootIdentityLocked(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		closeFile = false
		return fmt.Errorf("close recovered ranker learning log: %w", err)
	}
	closeFile = false
	if err := store.runTestHook(rankerStateHookBeforeRecoveryDirectorySync); err != nil {
		return err
	}
	if err := store.checkContextLocked(); err != nil {
		return err
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return err
	}
	if err := store.syncRankerStateDirectoriesLocked(); err != nil {
		return fmt.Errorf("sync recovered ranker learning log directory chain: %w", err)
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, info); err != nil {
		return err
	}
	return store.validateRootIdentityLocked()
}

func (store *rankerStateStore) truncateLearningLogLocked(size int64) error {
	if err := store.validateRegularFileIfPresentLocked(rankerStateLearningLogPath); err != nil {
		return err
	}
	file, err := store.root.OpenFile(rankerStateLearningLogPath, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	closeFile := true
	defer func() {
		if closeFile {
			_ = file.Close()
		}
	}()
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, opened); err != nil {
		return err
	}
	if err := store.beforeRankerPublicationLocked(rankerStateHookBeforeLogTruncate); err != nil {
		return err
	}

	if err := file.Truncate(size); err != nil {
		return err
	}
	if err := store.checkContextLocked(); err != nil {
		return err
	}
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, opened); err != nil {
		return err
	}
	if err := store.checkContextLocked(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync truncated ranker learning log: %w", err)
	}
	if err := file.Close(); err != nil {
		closeFile = false
		return fmt.Errorf("close truncated ranker learning log: %w", err)
	}
	closeFile = false
	if err := store.validateOpenedRegularFileLocked(rankerStateLearningLogPath, opened); err != nil {
		return err
	}
	if err := store.validateRootIdentityLocked(); err != nil {
		return err
	}

	return store.syncRankerStateDirectoriesLocked()
}

func (store *rankerStateStore) readRegularFileLocked(path string) ([]byte, error) {
	if err := store.checkContextLocked(); err != nil {
		return nil, err
	}
	if err := store.validateRootIdentityLocked(); err != nil {
		return nil, err
	}
	before, err := store.root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("ranker state path %q must be a regular file", filepath.ToSlash(path))
	}
	limit := int64(rankerStateMaxMetadataBytes)
	if path == rankerStateLearningLogPath {
		limit = rankerStateMaxLearningLogBytes
	}
	if before.Size() > limit {
		return nil, fmt.Errorf("ranker state path %q exceeds its %d-byte limit", filepath.ToSlash(path), limit)
	}

	file, err := store.root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, fmt.Errorf("ranker state path %q identity changed while opening it", filepath.ToSlash(path))
	}
	if err := store.validateOpenedRegularFileLocked(path, opened); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("ranker state path %q exceeds its %d-byte limit", filepath.ToSlash(path), limit)
	}
	if err := store.checkContextLocked(); err != nil {
		return nil, err
	}
	if err := store.validateOpenedRegularFileLocked(path, opened); err != nil {
		return nil, err
	}

	return raw, store.validateRootIdentityLocked()
}

func (store *rankerStateStore) validateRegularFileIfPresentLocked(path string) error {
	info, err := store.root.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("ranker state path %q must be a regular file", filepath.ToSlash(path))
	}

	return nil
}

func (store *rankerStateStore) validateRootIdentityLocked() error {
	if store == nil || store.root == nil || store.rootIdentity == nil || store.rankerDirectoryIdentity == nil {
		return errors.New("ranker state authority identity is unavailable")
	}
	pathIdentity, err := os.Stat(store.canonicalRoot)
	if err != nil {
		return fmt.Errorf("inspect ranker workspace root path identity: %w", err)
	}
	openedIdentity, err := store.root.Stat(".")
	if err != nil {
		return fmt.Errorf("inspect open ranker workspace root identity: %w", err)
	}
	if !pathIdentity.IsDir() || !openedIdentity.IsDir() ||
		!os.SameFile(pathIdentity, store.rootIdentity) || !os.SameFile(openedIdentity, store.rootIdentity) {
		return errors.New("ranker workspace root identity changed while state authority was held")
	}
	idiomDirectory, err := store.root.Lstat(".idiom")
	if err != nil {
		return fmt.Errorf("inspect ranker state parent directory identity: %w", err)
	}
	if idiomDirectory.Mode()&os.ModeSymlink != 0 || !idiomDirectory.IsDir() {
		return errors.New("ranker state parent directory must remain a non-symlink directory")
	}
	rankerDirectory, err := store.root.Lstat(rankerStateDirectoryPath)
	if err != nil {
		return fmt.Errorf("inspect ranker state directory identity: %w", err)
	}
	if rankerDirectory.Mode()&os.ModeSymlink != 0 || !rankerDirectory.IsDir() || !os.SameFile(rankerDirectory, store.rankerDirectoryIdentity) {
		return errors.New("ranker state directory identity changed while state authority was held")
	}

	return nil
}

func (store *rankerStateStore) checkContextLocked() error {
	if store == nil || store.ctx == nil {
		return errors.New("ranker state context is unavailable")
	}
	return store.ctx.Err()
}

func (store *rankerStateStore) beforeRankerPublicationLocked(stage string) error {
	if err := store.runTestHook(stage); err != nil {
		return err
	}
	if err := store.checkContextLocked(); err != nil {
		return err
	}

	return store.validateRootIdentityLocked()
}

func (store *rankerStateStore) validateOpenedRegularFileLocked(path string, opened os.FileInfo) error {
	if opened == nil || !opened.Mode().IsRegular() {
		return fmt.Errorf("open ranker state path %q must be a regular file", filepath.ToSlash(path))
	}
	current, err := store.root.Lstat(path)
	if err != nil {
		return err
	}
	if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return fmt.Errorf("ranker state path %q identity changed while opening it", filepath.ToSlash(path))
	}

	return nil
}

func parseRankerPendingTransaction(raw []byte) (rankerPendingTransaction, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var wire rankerPendingTransactionWire
	if err := decoder.Decode(&wire); err != nil {
		return rankerPendingTransaction{}, errors.New("invalid ranker transaction journal")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return rankerPendingTransaction{}, errors.New("invalid ranker transaction journal")
	}
	if wire.Version == nil || wire.Event == nil || wire.BeforeWeights == nil || wire.AfterWeights == nil ||
		wire.Materialize == nil || wire.CaptureSnapshot == nil || wire.LogOffset == nil || wire.LogPrefixSHA256 == nil {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal is missing a required field")
	}
	pending := rankerPendingTransaction{
		Version:         *wire.Version,
		Event:           *wire.Event,
		BeforeWeights:   *wire.BeforeWeights,
		AfterWeights:    *wire.AfterWeights,
		Materialize:     *wire.Materialize,
		CaptureSnapshot: *wire.CaptureSnapshot,
		LogOffset:       *wire.LogOffset,
		LogPrefixSHA256: *wire.LogPrefixSHA256,
	}
	canonicalJournal, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return rankerPendingTransaction{}, errors.New("invalid ranker transaction journal")
	}
	canonicalJournal = append(canonicalJournal, '\n')
	if !bytes.Equal(raw, canonicalJournal) {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal is not canonically encoded")
	}
	if pending.Version != rankerStateJournalVersion {
		return rankerPendingTransaction{}, fmt.Errorf("unsupported ranker transaction journal version %d", pending.Version)
	}
	if pending.LogOffset < 0 {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal has a negative log offset")
	}
	if len(pending.LogPrefixSHA256) != sha256.Size*2 || strings.ToLower(pending.LogPrefixSHA256) != pending.LogPrefixSHA256 {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal has an invalid log prefix digest")
	}
	if _, err := hex.DecodeString(pending.LogPrefixSHA256); err != nil {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal has an invalid log prefix digest")
	}
	canonicalEvent := sanitizeLearningLogEntry(pending.Event)
	parsedEvent, parsedErr := json.Marshal(pending.Event)
	canonicalEventJSON, canonicalErr := json.Marshal(canonicalEvent)
	if parsedErr != nil || canonicalErr != nil || !bytes.Equal(parsedEvent, canonicalEventJSON) {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal has a noncanonical event")
	}
	if err := validateLearningLogEntrySemantics(canonicalEvent); err != nil {
		return rankerPendingTransaction{}, fmt.Errorf("ranker transaction journal event is invalid: %w", err)
	}
	if !weightsEqual(pending.BeforeWeights, canonicalLearnedWeights(pending.BeforeWeights)) ||
		!weightsEqual(pending.AfterWeights, canonicalLearnedWeights(pending.AfterWeights)) {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal has noncanonical weights")
	}
	expectedCapture := learningEntryCapturesSnapshot(canonicalEvent)
	if pending.CaptureSnapshot != expectedCapture {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal snapshot phase does not match its event")
	}
	expectedMaterialize := learningEntryRequiresMaterialization(canonicalEvent)
	if pending.Materialize != expectedMaterialize {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal materialization phase does not match its event")
	}
	if pending.CaptureSnapshot && !learningEventSnapshotMatches(canonicalEvent, pending.BeforeWeights, pending.AfterWeights) {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal event snapshot does not match its weights")
	}
	if !pending.CaptureSnapshot && !weightsEqual(pending.BeforeWeights, pending.AfterWeights) {
		return rankerPendingTransaction{}, errors.New("ranker transaction journal changes weights without an event snapshot")
	}

	return pending, nil
}

func canonicalLearningDecision(entry LearningLogEntry) (LearningLogEntry, []byte, error) {
	if strings.TrimSpace(entry.Source) == "" {
		return LearningLogEntry{}, nil, errors.New("learning log entry needs a non-empty source")
	}
	if strings.TrimSpace(entry.Action) == "" {
		return LearningLogEntry{}, nil, errors.New("learning log entry needs a non-empty action")
	}

	entry = sanitizeLearningLogEntry(entry)
	entry.RequestID = canonicalLearningRequestID(entry.RequestID, entry)
	entry.EventID = learningEventID(entry)
	if err := validateLearningLogEntrySemantics(entry); err != nil {
		return LearningLogEntry{}, nil, err
	}
	line, err := encodeCanonicalLearningLogEntry(entry)
	if err != nil {
		return LearningLogEntry{}, nil, err
	}

	return entry, line, nil
}

func encodeCanonicalLearningLogEntry(entry LearningLogEntry) ([]byte, error) {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}

	return append(encoded, '\n'), nil
}

func parseCanonicalLearningLog(raw []byte) ([]LearningLogEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[len(raw)-1] != '\n' {
		return nil, errors.New("ranker learning log does not end at an event boundary")
	}

	lines := bytes.Split(raw[:len(raw)-1], []byte{'\n'})
	if len(lines) > rankerStateMaxLearningLogEntries {
		return nil, fmt.Errorf("ranker learning log exceeds its %d-event limit", rankerStateMaxLearningLogEntries)
	}
	entries := make([]LearningLogEntry, 0, len(lines))
	requestEvents := make(map[string]string, len(lines))
	for _, line := range lines {
		if len(line) == 0 {
			return nil, errors.New("ranker learning log contains an empty event")
		}
		if len(line) > rankerStateMaxLearningLogEntryBytes {
			return nil, fmt.Errorf("ranker learning log event exceeds its %d-byte limit", rankerStateMaxLearningLogEntryBytes)
		}
		entry, err := parseCanonicalLearningLogEntry(line)
		if err != nil {
			return nil, err
		}
		if entry.RequestID != "" {
			if _, exists := requestEvents[entry.RequestID]; exists {
				return nil, fmt.Errorf("ranker learning log repeats request id %q", entry.RequestID)
			}
			requestEvents[entry.RequestID] = entry.EventID
		}
		entries = append(entries, entry)
	}
	if _, err := canonicalLearningLogFinalWeights(entries); err != nil {
		return nil, err
	}

	return entries, nil
}

func canonicalLearningLogFinalWeights(entries []LearningLogEntry) (map[string]float64, error) {
	current := map[string]float64{}
	byEventID := make(map[string]LearningLogEntry, len(entries))
	for index, entry := range entries {
		if _, exists := byEventID[entry.EventID]; exists {
			return nil, fmt.Errorf("ranker learning log repeats event id %q", entry.EventID)
		}
		if learningEntryCapturesSnapshot(entry) {
			if !weightsEqual(*entry.BeforeWeights, current) {
				return nil, fmt.Errorf("ranker learning log event %d before weights do not match prior materialized state", index)
			}
			if entry.Action == "restore" && entry.State == LearningStateAccepted {
				target, ok := byEventID[entry.ResultID]
				if !ok {
					return nil, fmt.Errorf("ranker restore event %d references unknown prior event %q", index, entry.ResultID)
				}
				expected, ok := rollbackWeightsForEntry(target)
				if !ok || !weightsEqual(*entry.AfterWeights, canonicalLearnedWeights(expected)) {
					return nil, fmt.Errorf("ranker restore event %d does not match its referenced rollback state", index)
				}
			}
			if learningEntryRequiresMaterialization(entry) {
				current = cloneWeights(*entry.AfterWeights)
			}
		}
		byEventID[entry.EventID] = entry
	}

	return current, nil
}

func parseCanonicalLearningLogEntry(raw []byte) (LearningLogEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()

	var entry LearningLogEntry
	if err := decoder.Decode(&entry); err != nil {
		return LearningLogEntry{}, errors.New("invalid persisted learning-log entry")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return LearningLogEntry{}, errors.New("invalid persisted learning-log entry")
	}

	canonical := sanitizeLearningLogEntry(entry)
	parsedJSON, parsedErr := json.Marshal(entry)
	canonicalJSON, canonicalErr := json.Marshal(canonical)
	if parsedErr != nil || canonicalErr != nil || !bytes.Equal(parsedJSON, canonicalJSON) {
		return LearningLogEntry{}, errors.New("noncanonical persisted learning-log entry")
	}
	if !bytes.Equal(raw, canonicalJSON) {
		return LearningLogEntry{}, errors.New("noncanonical persisted learning-log encoding")
	}
	if err := validateLearningLogEntrySemantics(canonical); err != nil {
		return LearningLogEntry{}, fmt.Errorf("invalid persisted learning-log semantics: %w", err)
	}

	return canonical, nil
}

func learningEventSnapshotMatches(entry LearningLogEntry, before, after map[string]float64) bool {
	if entry.BeforeWeights == nil || entry.AfterWeights == nil || entry.Rollback == nil {
		return false
	}
	expectedBefore := sanitizeLearningLogWeights(before)
	expectedAfter := sanitizeLearningLogWeights(after)

	return weightsEqual(*entry.BeforeWeights, expectedBefore) &&
		weightsEqual(*entry.AfterWeights, expectedAfter) &&
		weightsEqual(entry.Rollback.Weights, expectedBefore)
}

func canonicalLearnedWeights(weights map[string]float64) map[string]float64 {
	cleaned := make(map[string]float64, len(weights))
	for key, value := range weights {
		normalized := normalizeWeightKey(key)
		if normalized == "" {
			continue
		}
		clamped := clampLearnedWeight(value)
		if absDiff(clamped, LearnedWeightBaseline) < 1e-9 {
			continue
		}
		cleaned[normalized] = clamped
	}

	return cleaned
}

func weightsEqual(left, right map[string]float64) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}

	return true
}

func writeFull(writer io.Writer, body []byte) (int, error) {
	total := 0
	for len(body) > 0 {
		written, err := writer.Write(body)
		total += written
		if err != nil {
			return total, err
		}
		if written <= 0 || written > len(body) {
			return total, io.ErrShortWrite
		}
		body = body[written:]
	}

	return total, nil
}

func (store *rankerStateStore) syncRankerStateDirectoriesLocked() error {
	for _, directory := range []string{rankerStateDirectoryPath, ".idiom", "."} {
		if err := store.checkContextLocked(); err != nil {
			return err
		}
		if err := store.validateRootIdentityLocked(); err != nil {
			return err
		}
		if err := atomicfile.SyncDirRoot(store.root, directory); err != nil {
			return fmt.Errorf("sync ranker state directory %q: %w", directory, err)
		}
	}

	return nil
}

func (store *rankerStateStore) runTestHook(stage string) error {
	return runRankerStateTestHook(store.canonicalRoot, stage)
}

func runRankerStateTestHook(canonicalRoot string, stage string) error {
	rankerStateTestHooks.RLock()
	hook := rankerStateTestHooks.byRoot[canonicalRoot]
	rankerStateTestHooks.RUnlock()
	if hook == nil {
		return nil
	}

	return hook(stage)
}
