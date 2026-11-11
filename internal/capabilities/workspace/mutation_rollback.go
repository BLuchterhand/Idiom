package workspace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/safepath"
)

var errMutationRollbackConsumed = errors.New("workspace mutation rollback was already completed")

type MutationRollbackStatus string

const (
	MutationRollbackComplete           MutationRollbackStatus = "complete"
	MutationRollbackRejectedStale      MutationRollbackStatus = "rejected_stale"
	MutationRollbackPartial            MutationRollbackStatus = "partial"
	MutationRollbackFailedBeforeCommit MutationRollbackStatus = "failed_before_commit"
)

type MutationRollbackEffectKind string

const (
	MutationRollbackRestoredFile            MutationRollbackEffectKind = "restored_file"
	MutationRollbackRemovedCreatedFile      MutationRollbackEffectKind = "removed_created_file"
	MutationRollbackRemovedCreatedDirectory MutationRollbackEffectKind = "removed_created_directory"
	MutationRollbackRevertedAppend          MutationRollbackEffectKind = "reverted_append"
)

type MutationRollbackEffect struct {
	CanonicalID string                     `json:"canonical_id"`
	Kind        MutationRollbackEffectKind `json:"kind"`
}

// MutationRollbackResult is the privacy-safe effect and progress projection
// for one rollback attempt. It contains no roots, bodies, inode metadata, or
// digests and is safe to persist in the host audit stream.
type MutationRollbackResult struct {
	Status       MutationRollbackStatus   `json:"status"`
	Effects      []MutationRollbackEffect `json:"effects,omitempty"`
	PendingPaths []string                 `json:"pending_paths,omitempty"`
}

type mutationRollbackCheckpoint struct {
	Stage       mutationRollbackCheckpointStage
	CanonicalID string
	EffectCount int
}

type mutationRollbackCheckpointStage string

const (
	mutationRollbackBeforeQuarantine mutationRollbackCheckpointStage = "before_quarantine"
	mutationRollbackAfterQuarantine  mutationRollbackCheckpointStage = "after_quarantine"
	mutationRollbackAfterEffect      mutationRollbackCheckpointStage = "after_effect"
)

type mutationRollbackCheckpointFunc func(mutationRollbackCheckpoint) error
type mutationRollbackCheckpointContextKey struct{}

func mutationRollbackCheckpointFromContext(ctx context.Context) mutationRollbackCheckpointFunc {
	if ctx == nil {
		return nil
	}
	checkpoint, _ := ctx.Value(mutationRollbackCheckpointContextKey{}).(mutationRollbackCheckpointFunc)
	return checkpoint
}

// MutationRollback is an invocation-scoped, resumable, one-use capability for
// restoring exact rooted transaction preimages. Its filesystem authority and
// private evidence never leave workspace.
type MutationRollback struct {
	state *mutationRollbackState
}

type mutationRollbackLifecycle uint8

const (
	mutationRollbackReady mutationRollbackLifecycle = iota
	mutationRollbackApplying
	mutationRollbackComplete
)

type mutationRollbackState struct {
	mu           sync.Mutex
	lifecycle    mutationRollbackLifecycle
	owner        *PathAccess
	rootPath     string
	rootIdentity os.FileInfo
	invocation   executionSessionIdentity
	entries      []mutationRollbackEntry
	effects      []MutationRollbackEffect
}

type mutationRollbackEntry struct {
	canonicalID        string
	kind               MutationCommitKind
	preimage           workspaceMutationPreimage
	postimage          mutationRollbackPostimage
	appendStart        int64
	appendLength       int64
	appendDigest       [sha256.Size]byte
	appendExact        bool
	state              mutationRollbackEntryState
	effectRecorded     bool
	desiredPostimage   mutationRollbackPostimage
	desiredSet         bool
	restoredPostimage  mutationRollbackPostimage
	quarantineName     string
	quarantineExpected bool
}

type mutationRollbackEntryState uint8

const (
	mutationRollbackEntryPending mutationRollbackEntryState = iota
	mutationRollbackEntryCommittedUnreconciled
	mutationRollbackEntryRestored
)

type mutationRollbackPostimage struct {
	exists         bool
	directory      bool
	identity       os.FileInfo
	parentIdentity os.FileInfo
	mode           os.FileMode
	digest         [sha256.Size]byte
}

// Rollback returns the capability attached to this receipt. A committed
// mutation whose exact rollback evidence could not be captured returns an
// error instead of silently presenting an unavailable undo operation.
func (r *MutationCommitReceipt) Rollback() (*MutationRollback, error) {
	if r == nil {
		return nil, nil
	}
	if r.rollbackErr != nil {
		return nil, r.rollbackErr
	}
	if r.rollback == nil {
		if len(r.Commits) != 0 {
			return nil, fmt.Errorf("workspace mutation rollback evidence is unavailable")
		}
		return nil, nil
	}

	return &MutationRollback{state: r.rollback}, nil
}

// Paths returns only canonical workspace-relative identities for display.
func (r *MutationRollback) Paths() []string {
	if r == nil || r.state == nil {
		return nil
	}

	r.state.mu.Lock()
	defer r.state.mu.Unlock()
	return rollbackEntryPaths(r.state.entries, false)
}

func (r MutationRollback) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Paths []string `json:"paths,omitempty"`
	}{Paths: (&r).Paths()})
}

// RollbackMutation validates all pending postimages and prior rollback
// effects before applying another reverse effect. A filesystem failure after
// progress returns a typed partial result and leaves the capability resumable.
// Cancellation is honored until the first effect commits; after that the
// finite reverse transaction completes as far as the filesystem permits.
func (m *Manager) RollbackMutation(ctx context.Context, rollback *MutationRollback) (result MutationRollbackResult, resultErr error) {
	if m == nil {
		return MutationRollbackResult{Status: MutationRollbackFailedBeforeCommit}, fmt.Errorf("workspace manager is not configured")
	}
	if rollback == nil || rollback.state == nil {
		return MutationRollbackResult{Status: MutationRollbackFailedBeforeCommit}, fmt.Errorf("workspace mutation rollback is absent")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	state := rollback.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.lifecycle == mutationRollbackComplete {
		return state.result(MutationRollbackComplete), errMutationRollbackConsumed
	}
	if state.owner != &m.pathAccess {
		return state.result(MutationRollbackFailedBeforeCommit), fmt.Errorf("workspace mutation rollback belongs to a different manager")
	}
	identity, bound := executionSessionIdentityFromContext(ctx)
	if !bound || strings.TrimSpace(identity.sessionID) == "" || identity != state.invocation {
		return state.result(MutationRollbackFailedBeforeCommit), fmt.Errorf("workspace mutation rollback invocation identity does not match")
	}
	if err := ctx.Err(); err != nil {
		return state.result(MutationRollbackFailedBeforeCommit), err
	}
	if err := m.pathAccess.mutationGate.Lock(ctx, nil); err != nil {
		return state.result(MutationRollbackFailedBeforeCommit), err
	}
	defer m.pathAccess.mutationGate.Unlock()

	root, err := openMutationRollbackRoot(state)
	if err != nil {
		return state.result(MutationRollbackFailedBeforeCommit), err
	}
	defer root.Close()
	if err := reconcileCommittedRollbackEntries(root, state); err != nil {
		return state.result(statusForRollbackFailure(state)), err
	}
	if err := validateMutationRollbackState(root, state); err != nil {
		return state.result(MutationRollbackRejectedStale), err
	}

	committedThisAttempt := false
	defer func() {
		if !committedThisAttempt {
			return
		}
		if m.pathAccess.mutationEpoch != nil {
			m.pathAccess.mutationEpoch.Add(1)
		}
		m.resetLanguageTools()
	}()

	operationCtx := ctx
	checkpoint := mutationRollbackCheckpointFromContext(ctx)
	for index := len(state.entries) - 1; index >= 0; index-- {
		entry := &state.entries[index]
		if entry.state == mutationRollbackEntryRestored {
			continue
		}
		if !committedThisAttempt {
			if err := operationCtx.Err(); err != nil {
				return state.result(statusForRollbackFailure(state)), err
			}
		}
		effect, err := restoreMutationRollbackEntry(operationCtx, root, entry)
		if err != nil {
			if rollbackEffectCommitted(err) {
				markRollbackEffectCommitted(root, state, entry, effect)
				committedThisAttempt = true
			}
			state.lifecycle = mutationRollbackApplying
			return state.result(statusForRollbackFailure(state)), err
		}
		entry.state = mutationRollbackEntryCommittedUnreconciled
		if !entry.effectRecorded {
			state.effects = append(state.effects, effect)
			entry.effectRecorded = true
		}
		entry.restoredPostimage, err = captureRestoredMutationRollbackPostimage(root, *entry)
		if err != nil {
			state.lifecycle = mutationRollbackApplying
			committedThisAttempt = true
			return state.result(MutationRollbackPartial), fmt.Errorf("capture restored rollback state for %q", entry.canonicalID)
		}
		entry.state = mutationRollbackEntryRestored
		state.lifecycle = mutationRollbackApplying
		committedThisAttempt = true
		if checkpoint != nil {
			if err := checkpoint(mutationRollbackCheckpoint{Stage: mutationRollbackAfterEffect, CanonicalID: entry.canonicalID, EffectCount: len(state.effects)}); err != nil {
				return state.result(MutationRollbackPartial), err
			}
		}
		operationCtx = context.WithoutCancel(ctx)
	}

	state.lifecycle = mutationRollbackComplete
	return state.result(MutationRollbackComplete), nil
}

func rollbackEffectCommitted(err error) bool {
	var postCommit *atomicfile.PostCommitError
	return errors.As(err, &postCommit) && postCommit.Committed()
}

func markRollbackEffectCommitted(root *os.Root, state *mutationRollbackState, entry *mutationRollbackEntry, effect MutationRollbackEffect) {
	entry.state = mutationRollbackEntryCommittedUnreconciled
	if !entry.effectRecorded {
		state.effects = append(state.effects, effect)
		entry.effectRecorded = true
	}
	if restored, err := captureRestoredMutationRollbackPostimage(root, *entry); err == nil {
		entry.restoredPostimage = restored
		entry.state = mutationRollbackEntryRestored
	}
}

func statusForRollbackFailure(state *mutationRollbackState) MutationRollbackStatus {
	if state != nil && len(state.effects) != 0 {
		return MutationRollbackPartial
	}
	return MutationRollbackFailedBeforeCommit
}

func (s *mutationRollbackState) result(status MutationRollbackStatus) MutationRollbackResult {
	if s == nil {
		return MutationRollbackResult{Status: status}
	}
	return MutationRollbackResult{
		Status:       status,
		Effects:      append([]MutationRollbackEffect(nil), s.effects...),
		PendingPaths: rollbackEntryPaths(s.entries, true),
	}
}

func rollbackEntryPaths(entries []mutationRollbackEntry, pendingOnly bool) []string {
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if pendingOnly && entry.state == mutationRollbackEntryRestored {
			continue
		}
		if entry.canonicalID != "" && !slices.Contains(paths, entry.canonicalID) {
			paths = append(paths, entry.canonicalID)
		}
	}
	return paths
}

func (m *workspaceMutation) buildMutationRollbackState(commits []MutationCommit) (*mutationRollbackState, error) {
	if m == nil || m.access == nil || m.root == nil || m.authorization == nil || len(commits) == 0 {
		return nil, nil
	}
	if m.rollbackErr != nil {
		return nil, fmt.Errorf("workspace mutation rollback segment evidence is unavailable")
	}
	if !m.authorization.invocation.explicit || strings.TrimSpace(m.authorization.invocation.sessionID) == "" {
		return nil, fmt.Errorf("workspace mutation rollback requires an explicit invocation identity")
	}
	if len(m.commits) != len(commits) {
		return nil, fmt.Errorf("workspace mutation rollback commit evidence is incomplete")
	}
	state := &mutationRollbackState{
		owner:        m.access,
		rootPath:     m.rootPath,
		rootIdentity: m.rootIdentity,
		invocation:   m.authorization.invocation,
		entries:      make([]mutationRollbackEntry, 0, len(commits)),
	}
	for index, commit := range commits {
		privateCommit := m.commits[index]
		entry := mutationRollbackEntry{
			canonicalID:  commit.Identity.CanonicalID,
			kind:         commit.Kind,
			preimage:     cloneWorkspaceMutationPreimage(m.initialByPath[commit.Identity.CanonicalID]),
			appendStart:  privateCommit.appendStart,
			appendLength: privateCommit.appendLength,
			appendDigest: privateCommit.appendDigest,
			appendExact:  privateCommit.appendExact,
		}
		if (entry.kind == MutationCommitCreatedDirectory || !entry.preimage.exists) && !workspaceMutationNoReplaceSupported {
			return nil, fmt.Errorf("workspace mutation rollback removal requires atomic no-replace path publication")
		}
		if entry.kind == MutationCommitAppendedFile && entry.preimage.exists && !entry.appendExact {
			return nil, fmt.Errorf("workspace mutation rollback append evidence is unavailable for %q", entry.canonicalID)
		}
		if !privateCommit.postimageExact {
			return nil, fmt.Errorf("workspace mutation rollback commit evidence is unavailable for %q", entry.canonicalID)
		}
		postimage, err := captureMutationRollbackPostimage(m.root, entry.canonicalID, entry.kind)
		if err != nil {
			return nil, fmt.Errorf("capture workspace mutation rollback postimage for %q", entry.canonicalID)
		}
		if entry.kind == MutationCommitCreatedDirectory {
			if postimage.identity == nil || privateCommit.postimage.identity == nil || !os.SameFile(postimage.identity, privateCommit.postimage.identity) ||
				postimage.parentIdentity == nil || privateCommit.postimage.parentIdentity == nil || !os.SameFile(postimage.parentIdentity, privateCommit.postimage.parentIdentity) ||
				postimage.mode != privateCommit.postimage.mode {
				return nil, fmt.Errorf("workspace mutation rollback directory postimage changed before receipt capture for %q", entry.canonicalID)
			}
		} else if !sameMutationRollbackPostimage(postimage, privateCommit.postimage) {
			return nil, fmt.Errorf("workspace mutation rollback postimage changed before receipt capture for %q", entry.canonicalID)
		}
		entry.postimage = postimage
		state.entries = append(state.entries, entry)
	}

	return state, nil
}

func cloneWorkspaceMutationPreimage(value workspaceMutationPreimage) workspaceMutationPreimage {
	value.body = append([]byte(nil), value.body...)
	return value
}

func captureMutationRollbackPostimage(root *os.Root, canonicalID string, kind MutationCommitKind) (mutationRollbackPostimage, error) {
	parent, base, err := openMutationRollbackParent(root, canonicalID)
	if err != nil {
		return mutationRollbackPostimage{}, err
	}
	defer parent.Close()
	parentIdentity, err := parent.Stat(".")
	if err != nil {
		return mutationRollbackPostimage{}, err
	}
	postimage := mutationRollbackPostimage{parentIdentity: parentIdentity}
	if kind == MutationCommitRemovedFile {
		if _, err := parent.Lstat(base); !os.IsNotExist(err) {
			if err == nil {
				err = fmt.Errorf("removed rollback path still exists")
			}
			return mutationRollbackPostimage{}, err
		}
		return postimage, nil
	}
	info, err := parent.Lstat(base)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return mutationRollbackPostimage{}, fmt.Errorf("rollback postimage is missing or unsafe")
	}
	postimage.exists = true
	postimage.identity = info
	postimage.mode = info.Mode()
	if kind == MutationCommitCreatedDirectory {
		if !info.IsDir() {
			return mutationRollbackPostimage{}, fmt.Errorf("rollback postimage is not a directory")
		}
		child, err := parent.OpenRoot(base)
		if err != nil {
			return mutationRollbackPostimage{}, err
		}
		postimage.digest, err = digestMutationRollbackDirectoryRoot(child)
		closeErr := child.Close()
		if err != nil {
			return mutationRollbackPostimage{}, err
		}
		if closeErr != nil {
			return mutationRollbackPostimage{}, closeErr
		}
		postimage.directory = true
		return postimage, nil
	}
	preimage, err := readWorkspaceMutationPreimage(parent, base)
	if err != nil || !preimage.exists {
		return mutationRollbackPostimage{}, errors.Join(err, fmt.Errorf("rollback file postimage is unavailable"))
	}
	postimage.digest = preimage.digest
	return postimage, nil
}

func captureRestoredMutationRollbackPostimage(root *os.Root, entry mutationRollbackEntry) (mutationRollbackPostimage, error) {
	if entry.quarantineName != "" {
		return mutationRollbackPostimage{}, fmt.Errorf("rollback quarantine is not reconciled")
	}
	if entry.desiredSet {
		current, err := captureMutationRollbackPostimage(root, entry.canonicalID, rollbackCaptureKind(entry.desiredPostimage))
		if err != nil || !sameMutationRollbackPostimage(current, entry.desiredPostimage) {
			return mutationRollbackPostimage{}, fmt.Errorf("restored rollback path does not match the committed desired state")
		}
		return current, nil
	}
	if entry.preimage.exists {
		current, err := captureMutationRollbackPostimage(root, entry.canonicalID, MutationCommitReplacedFile)
		if err != nil || current.digest != entry.preimage.digest || rollbackRestorableMode(current.mode) != rollbackRestorableMode(entry.preimage.info.Mode()) ||
			current.parentIdentity == nil || entry.postimage.parentIdentity == nil || !os.SameFile(current.parentIdentity, entry.postimage.parentIdentity) {
			return mutationRollbackPostimage{}, fmt.Errorf("restored rollback file does not match its transaction preimage")
		}
		return current, nil
	}
	current, err := captureMutationRollbackPostimage(root, entry.canonicalID, MutationCommitRemovedFile)
	if err != nil || current.parentIdentity == nil || entry.postimage.parentIdentity == nil || !os.SameFile(current.parentIdentity, entry.postimage.parentIdentity) {
		return mutationRollbackPostimage{}, fmt.Errorf("restored rollback absence does not match its committed parent")
	}
	return current, nil
}

func reconcileCommittedRollbackEntries(root *os.Root, state *mutationRollbackState) error {
	for index := range state.entries {
		entry := &state.entries[index]
		if entry.state != mutationRollbackEntryCommittedUnreconciled {
			continue
		}
		if entry.quarantineName != "" {
			if err := reconcileMutationRollbackQuarantine(root, entry); err != nil {
				return fmt.Errorf("workspace mutation rollback path %q has an unreconciled quarantine", entry.canonicalID)
			}
		}
		restored, err := captureRestoredMutationRollbackPostimage(root, *entry)
		if err != nil {
			return fmt.Errorf("workspace mutation rollback path %q has an unreconciled committed effect", entry.canonicalID)
		}
		entry.restoredPostimage = restored
		entry.state = mutationRollbackEntryRestored
	}
	return nil
}

func validateMutationRollbackState(root *os.Root, state *mutationRollbackState) error {
	for _, entry := range state.entries {
		expected := entry.postimage
		if entry.state == mutationRollbackEntryRestored {
			expected = entry.restoredPostimage
		}
		if state.lifecycle == mutationRollbackApplying && entry.state == mutationRollbackEntryPending && entry.kind == MutationCommitCreatedDirectory {
			if err := validateMutationRollbackDirectoryIdentity(root, entry.canonicalID, expected); err != nil {
				return err
			}
			continue
		}
		if err := validateMutationRollbackPostimage(root, entry.canonicalID, expected); err != nil {
			return err
		}
	}
	return nil
}

func validateMutationRollbackDirectoryIdentity(root *os.Root, canonicalID string, expected mutationRollbackPostimage) error {
	parent, base, err := openMutationRollbackParent(root, canonicalID)
	if err != nil {
		return fmt.Errorf("workspace mutation rollback directory %q is stale", canonicalID)
	}
	defer parent.Close()
	parentIdentity, err := parent.Stat(".")
	if err != nil || expected.parentIdentity == nil || !os.SameFile(parentIdentity, expected.parentIdentity) {
		return fmt.Errorf("workspace mutation rollback directory %q parent changed", canonicalID)
	}
	info, err := parent.Lstat(base)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || expected.identity == nil || !os.SameFile(info, expected.identity) || info.Mode() != expected.mode {
		return fmt.Errorf("workspace mutation rollback directory %q identity changed", canonicalID)
	}
	return nil
}

func validateMutationRollbackPostimage(root *os.Root, canonicalID string, expected mutationRollbackPostimage) error {
	current, err := captureMutationRollbackPostimage(root, canonicalID, rollbackCaptureKind(expected))
	if err != nil {
		return fmt.Errorf("workspace mutation rollback path %q is stale", canonicalID)
	}
	if !sameMutationRollbackPostimage(current, expected) {
		return fmt.Errorf("workspace mutation rollback path %q changed after commit", canonicalID)
	}
	return nil
}

func rollbackCaptureKind(expected mutationRollbackPostimage) MutationCommitKind {
	if !expected.exists {
		return MutationCommitRemovedFile
	}
	if expected.directory {
		return MutationCommitCreatedDirectory
	}
	return MutationCommitReplacedFile
}

func sameMutationRollbackPostimage(current, expected mutationRollbackPostimage) bool {
	if current.exists != expected.exists || current.directory != expected.directory {
		return false
	}
	if current.parentIdentity == nil || expected.parentIdentity == nil || !os.SameFile(current.parentIdentity, expected.parentIdentity) {
		return false
	}
	if !current.exists {
		return true
	}
	return current.identity != nil && expected.identity != nil && os.SameFile(current.identity, expected.identity) &&
		current.mode == expected.mode && current.digest == expected.digest
}

func restoreMutationRollbackEntry(ctx context.Context, root *os.Root, entry *mutationRollbackEntry) (MutationRollbackEffect, error) {
	if entry == nil {
		return MutationRollbackEffect{}, fmt.Errorf("workspace mutation rollback entry is absent")
	}
	if entry.kind == MutationCommitAppendedFile && entry.preimage.exists {
		effect := MutationRollbackEffect{CanonicalID: entry.canonicalID, Kind: MutationRollbackRevertedAppend}
		return effect, revertMutationRollbackAppend(ctx, root, entry)
	}
	if entry.kind == MutationCommitCreatedDirectory {
		effect := MutationRollbackEffect{CanonicalID: entry.canonicalID, Kind: MutationRollbackRemovedCreatedDirectory}
		return effect, quarantineRemoveMutationRollbackPath(ctx, root, entry)
	}
	if !entry.preimage.exists {
		effect := MutationRollbackEffect{CanonicalID: entry.canonicalID, Kind: MutationRollbackRemovedCreatedFile}
		return effect, quarantineRemoveMutationRollbackPath(ctx, root, entry)
	}

	parent, base, err := openMutationRollbackParent(root, entry.canonicalID)
	if err != nil {
		return MutationRollbackEffect{}, err
	}
	defer parent.Close()
	mode := rollbackRestorableMode(entry.preimage.info.Mode())
	err = atomicfile.WriteFileRootCheckedExactMode(parent, base, entry.preimage.body, mode, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return validateMutationRollbackPostimage(root, entry.canonicalID, entry.postimage)
	})
	return MutationRollbackEffect{CanonicalID: entry.canonicalID, Kind: MutationRollbackRestoredFile}, err
}

func revertMutationRollbackAppend(ctx context.Context, root *os.Root, entry *mutationRollbackEntry) error {
	if entry == nil || entry.appendStart < 0 || entry.appendLength <= 0 {
		return fmt.Errorf("workspace mutation rollback append evidence is invalid")
	}
	if err := validateMutationRollbackPostimage(root, entry.canonicalID, entry.postimage); err != nil {
		return err
	}
	parent, base, err := openMutationRollbackParent(root, entry.canonicalID)
	if err != nil {
		return err
	}
	defer parent.Close()
	file, err := parent.OpenFile(base, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || entry.postimage.identity == nil || !os.SameFile(info, entry.postimage.identity) || info.Mode() != entry.postimage.mode {
		return fmt.Errorf("workspace mutation rollback append identity changed")
	}
	if info.Size() != entry.appendStart+entry.appendLength {
		return fmt.Errorf("workspace mutation rollback append is no longer the exact tail")
	}
	hash := sha256.New()
	if _, err := io.CopyN(hash, io.NewSectionReader(file, entry.appendStart, entry.appendLength), entry.appendLength); err != nil {
		return err
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	if digest != entry.appendDigest {
		return fmt.Errorf("workspace mutation rollback append segment changed")
	}
	prefixHash := sha256.New()
	if entry.appendStart > 0 {
		if _, err := io.CopyN(prefixHash, io.NewSectionReader(file, 0, entry.appendStart), entry.appendStart); err != nil {
			return err
		}
	}
	entry.desiredPostimage = entry.postimage
	copy(entry.desiredPostimage.digest[:], prefixHash.Sum(nil))
	entry.desiredSet = true
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Truncate(entry.appendStart); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return &atomicfile.PostCommitError{Op: "rollback append committed but file sync failed", Err: err}
	}
	if err := atomicfile.SyncDirRoot(parent, "."); err != nil {
		return &atomicfile.PostCommitError{Op: "rollback append committed but parent sync failed", Err: err}
	}
	return nil
}

func quarantineRemoveMutationRollbackPath(ctx context.Context, root *os.Root, entry *mutationRollbackEntry) error {
	if entry == nil {
		return fmt.Errorf("workspace mutation rollback entry is absent")
	}
	expected := entry.postimage
	if entry.kind == MutationCommitCreatedDirectory {
		// Descendant effects are reversed before their implicitly created
		// directory. The directory must retain its committed identity and mode,
		// but its expected final content at this boundary is empty.
		expected.digest = sha256.Sum256(nil)
	}
	if err := validateMutationRollbackPostimage(root, entry.canonicalID, expected); err != nil {
		return err
	}
	parent, base, err := openMutationRollbackParent(root, entry.canonicalID)
	if err != nil {
		return err
	}
	defer parent.Close()
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	quarantine := fmt.Sprintf(".idiom-rollback-%x", nonce[:])
	if checkpoint := mutationRollbackCheckpointFromContext(ctx); checkpoint != nil {
		if err := checkpoint(mutationRollbackCheckpoint{Stage: mutationRollbackBeforeQuarantine, CanonicalID: entry.canonicalID}); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := parent.Rename(base, quarantine); err != nil {
		return err
	}
	entry.quarantineName = quarantine
	moved, err := captureMutationRollbackPostimage(parent, quarantine, rollbackCaptureKind(entry.postimage))
	if err != nil {
		return &atomicfile.PostCommitError{Op: "rollback target quarantined but validation failed", Err: err}
	}
	// The quarantine parent is the original target parent, so compare the
	// moved object's identity/mode/digest independently of the nested parent.
	if moved.exists != expected.exists || moved.directory != expected.directory ||
		moved.identity == nil || expected.identity == nil || !os.SameFile(moved.identity, expected.identity) ||
		moved.mode != expected.mode || moved.digest != expected.digest {
		if restoreErr := restoreUnexpectedMutationRollbackQuarantine(parent, base, entry); restoreErr != nil {
			return &atomicfile.PostCommitError{Op: "rollback moved an unexpected target and could not restore it", Err: restoreErr}
		}
		return fmt.Errorf("workspace mutation rollback target changed during quarantine")
	}
	entry.quarantineExpected = true
	if checkpoint := mutationRollbackCheckpointFromContext(ctx); checkpoint != nil {
		if err := checkpoint(mutationRollbackCheckpoint{Stage: mutationRollbackAfterQuarantine, CanonicalID: entry.canonicalID}); err != nil {
			return &atomicfile.PostCommitError{Op: "rollback target quarantined", Err: err}
		}
	}
	if err := parent.Remove(quarantine); err != nil {
		return &atomicfile.PostCommitError{Op: "rollback target quarantined but removal failed", Err: err}
	}
	entry.quarantineName = ""
	entry.quarantineExpected = false
	if err := atomicfile.SyncDirRoot(parent, "."); err != nil {
		return &atomicfile.PostCommitError{Op: "rollback quarantine committed but parent sync failed", Err: err}
	}
	return nil
}

func reconcileMutationRollbackQuarantine(root *os.Root, entry *mutationRollbackEntry) error {
	parent, base, err := openMutationRollbackParent(root, entry.canonicalID)
	if err != nil {
		return err
	}
	defer parent.Close()
	if !entry.quarantineExpected {
		return restoreUnexpectedMutationRollbackQuarantine(parent, base, entry)
	}
	expected := entry.postimage
	if entry.kind == MutationCommitCreatedDirectory {
		expected.digest = sha256.Sum256(nil)
	}
	moved, err := captureMutationRollbackPostimage(parent, entry.quarantineName, rollbackCaptureKind(expected))
	if err != nil || moved.identity == nil || expected.identity == nil || !os.SameFile(moved.identity, expected.identity) || moved.mode != expected.mode || moved.digest != expected.digest {
		return fmt.Errorf("rollback quarantine identity changed")
	}
	if err := parent.Remove(entry.quarantineName); err != nil {
		return err
	}
	entry.quarantineName = ""
	entry.quarantineExpected = false
	return atomicfile.SyncDirRoot(parent, ".")
}

func restoreUnexpectedMutationRollbackQuarantine(parent *os.Root, base string, entry *mutationRollbackEntry) error {
	if _, err := parent.Lstat(base); !os.IsNotExist(err) {
		if err == nil {
			err = fmt.Errorf("rollback target reappeared")
		}
		return err
	}
	if err := publishWorkspaceMutationPath(parent, entry.quarantineName, base); err != nil {
		return err
	}
	entry.quarantineName = ""
	entry.quarantineExpected = false
	entry.state = mutationRollbackEntryPending
	return nil
}

func rollbackRestorableMode(mode os.FileMode) os.FileMode {
	return mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

func openMutationRollbackRoot(state *mutationRollbackState) (*os.Root, error) {
	pathInfo, err := os.Lstat(state.rootPath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.IsDir() || state.rootIdentity == nil || !os.SameFile(pathInfo, state.rootIdentity) {
		return nil, fmt.Errorf("workspace mutation rollback root identity changed")
	}
	root, err := safepath.OpenRootPath(state.rootPath, "workspace mutation rollback root")
	if err != nil {
		return nil, fmt.Errorf("workspace mutation rollback root is unavailable")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(opened, state.rootIdentity) {
		root.Close()
		return nil, fmt.Errorf("workspace mutation rollback opened a different root")
	}
	return root, nil
}

func openMutationRollbackParent(root *os.Root, canonicalID string) (*os.Root, string, error) {
	relative := filepath.Clean(filepath.FromSlash(strings.TrimSpace(canonicalID)))
	if relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		return nil, "", fmt.Errorf("invalid rollback path %q", canonicalID)
	}
	parent, err := openMutationRollbackDirectory(root, filepath.Dir(relative))
	if err != nil {
		return nil, "", err
	}
	return parent, filepath.Base(relative), nil
}

func openMutationRollbackDirectory(root *os.Root, relative string) (*os.Root, error) {
	current, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	if relative == "." || relative == "" {
		return current, nil
	}
	for _, component := range workspaceMutationPathComponents(relative) {
		info, err := current.Lstat(component)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			current.Close()
			return nil, fmt.Errorf("rollback path traverses a changed or unsafe directory")
		}
		next, err := current.OpenRoot(component)
		if err != nil {
			current.Close()
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			next.Close()
			current.Close()
			return nil, fmt.Errorf("rollback directory identity changed while opening")
		}
		current.Close()
		current = next
	}
	return current, nil
}

func digestMutationRollbackDirectoryRoot(root *os.Root) ([sha256.Size]byte, error) {
	file, err := root.Open(".")
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	entries, readErr := file.ReadDir(-1)
	closeErr := file.Close()
	if readErr != nil {
		return [sha256.Size]byte{}, readErr
	}
	if closeErr != nil {
		return [sha256.Size]byte{}, closeErr
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	hash := sha256.New()
	for _, entry := range entries {
		info, err := root.Lstat(entry.Name())
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return [sha256.Size]byte{}, fmt.Errorf("rollback directory contains an unsafe entry")
		}
		_, _ = io.WriteString(hash, entry.Name())
		_, _ = hash.Write([]byte{0})
		if info.IsDir() {
			child, err := root.OpenRoot(entry.Name())
			if err != nil {
				return [sha256.Size]byte{}, err
			}
			digest, digestErr := digestMutationRollbackDirectoryRoot(child)
			closeErr := child.Close()
			if digestErr != nil {
				return [sha256.Size]byte{}, digestErr
			}
			if closeErr != nil {
				return [sha256.Size]byte{}, closeErr
			}
			_, _ = hash.Write([]byte{'d'})
			_, _ = hash.Write(digest[:])
			continue
		}
		if !info.Mode().IsRegular() {
			return [sha256.Size]byte{}, fmt.Errorf("rollback directory contains a non-regular entry")
		}
		preimage, err := readWorkspaceMutationPreimage(root, entry.Name())
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		_, _ = hash.Write([]byte{'f'})
		_, _ = hash.Write(preimage.digest[:])
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}
