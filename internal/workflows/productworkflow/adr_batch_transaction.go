package productworkflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"idiom/internal/platform/atomicfile"
)

const (
	adrBatchTransactionRelPath = ".idiom-adr-batch-transaction"
	adrBatchManifestFileName   = "manifest.json"
	adrBatchCandidateDirName   = "candidate"
	adrBatchPreviousDirName    = "previous"
	adrBatchManifestSchema     = "idiom.adr_batch_transaction.v1"
	adrBatchStatePrepared      = "prepared"
	adrBatchStateCommitted     = "committed"
)

// ErrStaleADRGeneration reports that an ADR write was prepared from a
// generation that is no longer current.
var ErrStaleADRGeneration = errors.New("stale ADR generation")

// ErrStaleADRWorkflowPhase reports that an ADR write was prepared for a
// lifecycle phase that is no longer current.
var ErrStaleADRWorkflowPhase = errors.New("stale ADR workflow phase")

// ADRGenerationToken identifies the exact ADR tree observed by a loader.
// It is intentionally opaque: callers can carry a loader-issued token into a
// persistence request but cannot manufacture a valid token themselves.
type ADRGenerationToken struct {
	generation   workflowArtifactGeneration
	indexPresent bool
}

type adrBatchArtifact struct {
	displayPath string
	relPath     string
	body        []byte
	perm        os.FileMode
}

type adrBatchManifest struct {
	SchemaVersion   string `json:"schema_version"`
	State           string `json:"state"`
	PreviousExists  bool   `json:"previous_exists"`
	PreviousDigest  string `json:"previous_digest,omitempty"`
	CandidateDigest string `json:"candidate_digest"`
}

type adrBatchPaths struct {
	transaction string
	manifest    string
	candidate   string
	previous    string
	adrs        string
}

type productWorkflowMutationLease struct {
	root       *os.Root
	effortRoot string
	paths      adrBatchPaths
	unlock     func()
	closeOnce  sync.Once
}

// openProductWorkflowMutationLease serializes authoritative product-workflow
// mutation and recovers an interrupted ADR directory swap before exposing the
// protected artifact set.
func openProductWorkflowMutationLease(effortRoot string) (*productWorkflowMutationLease, error) {
	return openProductWorkflowMutationLeaseWithPendingMigration(effortRoot, false)
}

func openProductWorkflowMutationLeaseWithPendingMigration(
	effortRoot string,
	allowPendingMigration bool,
) (*productWorkflowMutationLease, error) {
	absRoot, err := filepath.Abs(effortRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve product workflow effort root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	root, paths, unlock, err := acquireADRBatchTransaction(effortRoot)
	if err != nil {
		return nil, err
	}
	if !allowPendingMigration {
		if err := recoverOrRejectProductStatusMigrationMarkerLocked(root, absRoot); err != nil {
			unlock()

			return nil, fmt.Errorf("product status migration fence: %w", err)
		}
	}
	lease := &productWorkflowMutationLease{
		root:       root,
		effortRoot: absRoot,
		paths:      paths,
		unlock:     unlock,
	}
	if err := recoverADRBatchTransactionLocked(root, paths); err != nil {
		lease.Close()

		return nil, err
	}
	if err := recoverProductWorkflowTransitionLocked(lease); err != nil {
		lease.Close()

		return nil, fmt.Errorf("recover product workflow transition: %w", err)
	}

	return lease, nil
}

func (lease *productWorkflowMutationLease) Close() {
	if lease == nil || lease.unlock == nil {
		return
	}
	lease.closeOnce.Do(lease.unlock)
}

// ValidatePathIdentity confirms that the canonical effort path still names the
// root inode protected by this lease. Authoritative writes call it immediately
// before mutation so a renamed effort cannot be committed under a stale path.
func (lease *productWorkflowMutationLease) ValidatePathIdentity() error {
	if lease == nil {
		return errors.New("product workflow mutation lease is not open")
	}

	return validateEffortRootPathIdentity(lease.root, lease.effortRoot, "product workflow effort root")
}

func (lease *productWorkflowMutationLease) ADRGeneration() (ADRGenerationToken, error) {
	if lease == nil || lease.root == nil {
		return ADRGenerationToken{}, fmt.Errorf("product workflow mutation lease is not open")
	}
	generation, _, err := inspectADRBatchGeneration(lease.root, lease.paths)

	return generation, err
}

func acquireADRBatchTransaction(effortRoot string) (*os.Root, adrBatchPaths, func(), error) {
	lock, err := acquireEffortFileLock(effortRoot, productWorkflowMutationLockRelPath, "product workflow mutation")
	if err != nil {
		return nil, adrBatchPaths{}, nil, err
	}
	root := lock.root
	paths := adrBatchPaths{
		transaction: adrBatchTransactionRelPath,
		manifest:    filepath.Join(adrBatchTransactionRelPath, adrBatchManifestFileName),
		candidate:   filepath.Join(adrBatchTransactionRelPath, adrBatchCandidateDirName),
		previous:    filepath.Join(adrBatchTransactionRelPath, adrBatchPreviousDirName),
		adrs:        "adrs",
	}

	return root, paths, lock.Close, nil
}

// commitADRBatch stages a complete candidate ADR tree, durably records its
// prepared state, and publishes it with a recoverable directory swap. Recovery
// rolls prepared transactions back and retains only committed transactions.
func commitADRBatch(
	lease *productWorkflowMutationLease,
	expectedGeneration ADRGenerationToken,
	expectedPhase CreationPhase,
	tombstones []string,
	artifacts []adrBatchArtifact,
) error {
	if err := expectedGeneration.validate(); err != nil {
		return err
	}
	if len(artifacts) == 0 {
		return nil
	}
	if lease == nil || lease.root == nil {
		return errors.New("product workflow mutation lease is not open")
	}
	root := lease.root
	paths := lease.paths
	_, currentPhase, err := inspectWorkflowAuthority(root)
	if err != nil {
		return fmt.Errorf("load ADR workflow phase under commit lock: %w", err)
	}
	if currentPhase != expectedPhase {
		return fmt.Errorf("%w: expected %s, found %s", ErrStaleADRWorkflowPhase, expectedPhase, currentPhase)
	}
	currentGeneration, currentADRInfo, err := inspectADRBatchGeneration(root, paths)
	if err != nil {
		return fmt.Errorf("inspect current ADR generation: %w", err)
	}
	if !expectedGeneration.equal(currentGeneration) {
		return fmt.Errorf(
			"%w: expected %s generation %q at workflow revision %d/%s/%s, found %s generation %q at workflow revision %d/%s/%s",
			ErrStaleADRGeneration,
			expectedGeneration.generation.state,
			expectedGeneration.generation.digest,
			expectedGeneration.generation.stateRevision,
			expectedGeneration.generation.lastEventID,
			expectedGeneration.generation.phase,
			currentGeneration.generation.state,
			currentGeneration.generation.digest,
			currentGeneration.generation.stateRevision,
			currentGeneration.generation.lastEventID,
			currentGeneration.generation.phase,
		)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("validate ADR commit effort root: %w", err)
	}
	if err := root.Mkdir(paths.transaction, 0o700); err != nil {
		return fmt.Errorf("create ADR batch transaction directory: %w", err)
	}
	prepared := false
	defer func() {
		if !prepared {
			_ = removeADRBatchTransaction(root, paths)
		}
	}()
	if err := atomicfile.SyncDirRoot(root, "."); err != nil {
		return fmt.Errorf("sync effort root after creating ADR batch transaction: %w", err)
	}
	if err := root.Mkdir(paths.candidate, 0o700); err != nil {
		return fmt.Errorf("create ADR batch candidate directory: %w", err)
	}

	manifest := adrBatchManifest{SchemaVersion: adrBatchManifestSchema, State: adrBatchStatePrepared}
	if currentGeneration.generation.state == workflowArtifactGenerationPresent {
		manifest.PreviousExists = true
		manifest.PreviousDigest = currentGeneration.generation.digest
		if err := copyADRBatchTree(root, paths.adrs, paths.candidate); err != nil {
			return fmt.Errorf("stage current ADR tree: %w", err)
		}
		if err := chmodADRBatchDirectory(root, paths.candidate, currentADRInfo.Mode().Perm()); err != nil {
			return fmt.Errorf("preserve staged ADR root permissions: %w", err)
		}
	} else if err := chmodADRBatchDirectory(root, paths.candidate, 0o755); err != nil {
		return fmt.Errorf("set staged ADR root permissions: %w", err)
	}
	for _, tombstone := range tombstones {
		candidatePath, pathErr := adrBatchCandidateArtifactPath(paths, tombstone)
		if pathErr != nil {
			return fmt.Errorf("remove superseded ADR path %s: %w", tombstone, pathErr)
		}
		info, statErr := root.Lstat(candidatePath)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return fmt.Errorf("inspect superseded ADR path %s: %w", tombstone, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("superseded ADR path %s must be a regular file and must not be a symlink", tombstone)
		}
		if err := root.Remove(candidatePath); err != nil {
			return fmt.Errorf("remove superseded ADR path %s: %w", tombstone, err)
		}
	}

	for _, artifact := range artifacts {
		candidatePath, pathErr := adrBatchCandidateArtifactPath(paths, artifact.relPath)
		if pathErr != nil {
			return fmt.Errorf("stage %s: %w", artifact.displayPath, pathErr)
		}
		if err := ensureEffortArtifactParents(root, candidatePath); err != nil {
			return fmt.Errorf("stage %s parent: %w", artifact.displayPath, err)
		}
		if info, statErr := root.Lstat(candidatePath); statErr == nil {
			if info.Mode()&os.ModeSymlink != 0 || info.IsDir() {
				return fmt.Errorf("stage %s: destination must be a regular file", artifact.displayPath)
			}
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("inspect staged %s: %w", artifact.displayPath, statErr)
		}
		if err := atomicfile.WriteFileRoot(root, candidatePath, artifact.body, artifact.perm); err != nil {
			return fmt.Errorf("stage %s: %w", artifact.displayPath, err)
		}
	}
	if err := syncADRBatchTree(root, paths.candidate); err != nil {
		return fmt.Errorf("sync ADR batch candidate: %w", err)
	}
	manifest.CandidateDigest, err = digestADRBatchTree(root, paths.candidate)
	if err != nil {
		return fmt.Errorf("digest ADR batch candidate: %w", err)
	}
	if err := writeADRBatchManifest(root, paths.manifest, manifest); err != nil {
		return fmt.Errorf("prepare ADR batch journal: %w", err)
	}
	if err := lease.ValidatePathIdentity(); err != nil {
		return fmt.Errorf("validate ADR publish effort root: %w", err)
	}
	prepared = true

	return publishADRBatch(root, paths, manifest)
}

func (generation ADRGenerationToken) validate() error {
	if err := generation.generation.validate("ADR"); err != nil {
		return err
	}
	if generation.generation.state == workflowArtifactGenerationAbsent && generation.indexPresent {
		return errors.New("absent ADR generation cannot contain adrs/index.json")
	}

	return nil
}

func (generation ADRGenerationToken) equal(other ADRGenerationToken) bool {
	return generation.generation.equal(other.generation) && generation.indexPresent == other.indexPresent
}

func inspectADRBatchGeneration(
	root *os.Root,
	paths adrBatchPaths,
) (ADRGenerationToken, os.FileInfo, error) {
	workflowState, phase, err := inspectWorkflowAuthority(root)
	if err != nil {
		return ADRGenerationToken{}, nil, err
	}
	info, err := root.Lstat(paths.adrs)
	if os.IsNotExist(err) {
		return ADRGenerationToken{generation: newWorkflowArtifactGeneration(
			workflowArtifactGenerationAbsent,
			"",
			workflowState,
			phase,
		)}, nil, nil
	}
	if err != nil {
		return ADRGenerationToken{}, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ADRGenerationToken{}, nil, fmt.Errorf("ADR root must be a directory and must not be a symlink")
	}
	_, indexPresent, err := readWorkflowArtifactSnapshot(root, filepath.Join(paths.adrs, "index.json"))
	if err != nil {
		return ADRGenerationToken{}, nil, fmt.Errorf("inspect ADR index generation: %w", err)
	}
	digest, err := digestADRBatchTree(root, paths.adrs)
	if err != nil {
		return ADRGenerationToken{}, nil, err
	}

	return ADRGenerationToken{
		generation: newWorkflowArtifactGeneration(
			workflowArtifactGenerationPresent,
			digest,
			workflowState,
			phase,
		),
		indexPresent: indexPresent,
	}, info, nil
}

func publishADRBatch(root *os.Root, paths adrBatchPaths, manifest adrBatchManifest) error {
	preparedManifest := manifest
	previousMoved := false
	candidatePublished := false
	rollback := func(publishErr error) error {
		rollbackErr := rollbackADRBatchPublish(root, paths, manifest, previousMoved, candidatePublished)
		if rollbackErr != nil {
			return fmt.Errorf("%w (ADR batch rollback failed: %v)", publishErr, rollbackErr)
		}

		return publishErr
	}

	if manifest.PreviousExists {
		if err := root.Rename(paths.adrs, paths.previous); err != nil {
			return rollback(fmt.Errorf("move current ADR tree to rollback storage: %w", err))
		}
		previousMoved = true
		if err := syncADRBatchRollbackDestination(root, paths); err != nil {
			return rollback(fmt.Errorf("sync ADR rollback rename: %w", err))
		}
	}
	if err := root.Rename(paths.candidate, paths.adrs); err != nil {
		return rollback(fmt.Errorf("publish ADR batch candidate: %w", err))
	}
	candidatePublished = true
	if err := syncADRBatchPublishedDestination(root, paths); err != nil {
		return rollback(fmt.Errorf("sync published ADR batch: %w", err))
	}
	if err := verifyADRBatchTree(root, paths.adrs, manifest.CandidateDigest); err != nil {
		return rollback(fmt.Errorf("verify published ADR batch: %w", err))
	}

	manifest.State = adrBatchStateCommitted
	manifestWriteErr := writeADRBatchManifest(root, paths.manifest, manifest)
	if manifestWriteErr != nil && !artifactWriteCommitted(manifestWriteErr) {
		return rollback(fmt.Errorf("write committed ADR batch manifest: %w", manifestWriteErr))
	}
	manifestProofErr := verifyExactADRBatchManifest(root, paths.manifest, manifest)
	if manifestProofErr == nil && manifestWriteErr != nil {
		manifestProofErr = atomicfile.SyncDirRoot(root, filepath.Dir(paths.manifest))
	}
	if manifestProofErr != nil {
		publishErr := fmt.Errorf("committed ADR batch manifest could not be verified durably: %v", manifestProofErr)
		if restoreErr := restorePreparedADRBatchManifest(root, paths.manifest, preparedManifest); restoreErr != nil {
			return fmt.Errorf("%w (restore prepared ADR batch manifest for rollback failed: %v)", publishErr, restoreErr)
		}

		return rollback(publishErr)
	}
	if err := verifyADRBatchTree(root, paths.adrs, manifest.CandidateDigest); err != nil {
		publishErr := fmt.Errorf("published ADR batch changed before commit proof: %v", err)
		if restoreErr := restorePreparedADRBatchManifest(root, paths.manifest, preparedManifest); restoreErr != nil {
			return fmt.Errorf("%w (restore prepared ADR batch manifest for rollback failed: %v)", publishErr, restoreErr)
		}

		return rollback(publishErr)
	}
	if err := removeADRBatchTransaction(root, paths); err != nil {
		return &atomicfile.PostCommitError{
			Op:  "ADR batch committed but transaction cleanup failed",
			Err: err,
		}
	}
	if manifestWriteErr != nil {
		return &atomicfile.PostCommitError{
			Op:  "ADR batch committed after manifest durability recovery",
			Err: manifestWriteErr,
		}
	}

	return nil
}

func rollbackADRBatchPublish(
	root *os.Root,
	paths adrBatchPaths,
	manifest adrBatchManifest,
	previousMoved bool,
	candidatePublished bool,
) error {
	if candidatePublished {
		if err := root.Rename(paths.adrs, paths.candidate); err != nil {
			return fmt.Errorf("move candidate out of published path: %w", err)
		}
	}
	if previousMoved {
		if err := root.Rename(paths.previous, paths.adrs); err != nil {
			return fmt.Errorf("restore previous ADR tree: %w", err)
		}
	}
	if err := syncADRBatchPublishedDestination(root, paths); err != nil {
		return err
	}
	if manifest.PreviousExists {
		if err := verifyADRBatchTree(root, paths.adrs, manifest.PreviousDigest); err != nil {
			return fmt.Errorf("verify restored ADR tree: %w", err)
		}
	} else {
		exists, err := adrBatchDirectoryExists(root, paths.adrs)
		if err != nil {
			return fmt.Errorf("inspect rolled-back ADR tree: %w", err)
		}
		if exists {
			return fmt.Errorf("rollback did not restore the absent ADR tree")
		}
	}

	return removeADRBatchTransaction(root, paths)
}

func recoverADRBatchTransactionLocked(root *os.Root, paths adrBatchPaths) error {
	transactionInfo, err := root.Lstat(paths.transaction)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect ADR batch transaction: %w", err)
	}
	if transactionInfo.Mode()&os.ModeSymlink != 0 || !transactionInfo.IsDir() {
		return fmt.Errorf("ADR batch transaction path must be a directory and must not be a symlink")
	}

	manifest, err := readADRBatchManifest(root, paths.manifest)
	if os.IsNotExist(err) {
		// Publishing starts only after the prepared manifest is durable.
		return removeADRBatchTransaction(root, paths)
	}
	if err != nil {
		return fmt.Errorf("read ADR batch transaction journal: %w", err)
	}
	if manifest.State == adrBatchStateCommitted {
		if err := verifyADRBatchTree(root, paths.adrs, manifest.CandidateDigest); err != nil {
			return fmt.Errorf("committed ADR batch is not readable: %w", err)
		}
		candidateExists, err := adrBatchDirectoryExists(root, paths.candidate)
		if err != nil {
			return err
		}
		if candidateExists {
			return fmt.Errorf("committed ADR batch still has a staged candidate tree")
		}

		return removeADRBatchTransaction(root, paths)
	}

	return recoverPreparedADRBatch(root, paths, manifest)
}

func recoverPreparedADRBatch(root *os.Root, paths adrBatchPaths, manifest adrBatchManifest) error {
	adrsExists, err := adrBatchDirectoryExists(root, paths.adrs)
	if err != nil {
		return err
	}
	_, err = adrBatchDirectoryExists(root, paths.candidate)
	if err != nil {
		return err
	}
	previousExists, err := adrBatchDirectoryExists(root, paths.previous)
	if err != nil {
		return err
	}

	if manifest.PreviousExists {
		if adrsExists {
			currentDigest, err := digestADRBatchTree(root, paths.adrs)
			if err != nil {
				return fmt.Errorf("digest prepared ADR transaction current tree: %w", err)
			}
			switch currentDigest {
			case manifest.PreviousDigest:
				// A destination-first recovery rename may be replayed with
				// both names present. The effort-root copy is already the
				// desired generation, so only make that name durable.
				if err := atomicfile.SyncDirRoot(root, "."); err != nil {
					return fmt.Errorf("sync recovered ADR tree: %w", err)
				}

				return removeADRBatchTransaction(root, paths)
			case manifest.CandidateDigest:
				if !previousExists {
					return fmt.Errorf("prepared ADR transaction lost rollback tree")
				}
				if err := root.RemoveAll(paths.adrs); err != nil {
					return fmt.Errorf("remove published ADR candidate during recovery: %w", err)
				}
				if err := atomicfile.SyncDirRoot(root, "."); err != nil {
					return fmt.Errorf("sync removal of published ADR candidate: %w", err)
				}
				adrsExists = false
			default:
				return fmt.Errorf("prepared ADR transaction current tree matches neither journaled generation")
			}
		}
		if !adrsExists {
			if !previousExists {
				return fmt.Errorf("prepared ADR transaction cannot locate a recoverable previous tree")
			}
			if err := verifyADRBatchTree(root, paths.previous, manifest.PreviousDigest); err != nil {
				return fmt.Errorf("verify ADR rollback tree: %w", err)
			}
			if err := root.Rename(paths.previous, paths.adrs); err != nil {
				return fmt.Errorf("recover previous ADR tree: %w", err)
			}
		}
		if err := syncADRBatchPublishedDestination(root, paths); err != nil {
			return err
		}
		if err := verifyADRBatchTree(root, paths.adrs, manifest.PreviousDigest); err != nil {
			return fmt.Errorf("verify recovered previous ADR tree: %w", err)
		}
	} else {
		if previousExists {
			return fmt.Errorf("prepared ADR transaction unexpectedly contains rollback storage")
		}
		if adrsExists {
			if err := verifyADRBatchTree(root, paths.adrs, manifest.CandidateDigest); err != nil {
				return fmt.Errorf("prepared initial ADR transaction has an unknown current tree: %w", err)
			}
			if err := root.RemoveAll(paths.adrs); err != nil {
				return fmt.Errorf("remove prepared initial ADR candidate: %w", err)
			}
			if err := atomicfile.SyncDirRoot(root, "."); err != nil {
				return fmt.Errorf("sync removal of prepared initial ADR candidate: %w", err)
			}
		}
	}

	return removeADRBatchTransaction(root, paths)
}

func adrBatchCandidateArtifactPath(paths adrBatchPaths, artifactPath string) (string, error) {
	cleanPath, ok := cleanEffortRelativePath(artifactPath)
	if !ok {
		return "", fmt.Errorf("artifact path must be effort-relative and must not contain path traversal")
	}
	adrRelative, err := filepath.Rel(paths.adrs, cleanPath)
	if err != nil || adrRelative == "." || adrRelative == ".." || strings.HasPrefix(adrRelative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("artifact path %q is outside the ADR tree", artifactPath)
	}

	return filepath.Join(paths.candidate, adrRelative), nil
}

func copyADRBatchTree(root *os.Root, sourceRoot, destinationRoot string) error {
	directory, err := root.Open(sourceRoot)
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		sourcePath := filepath.Join(sourceRoot, entry.Name())
		destinationPath := filepath.Join(destinationRoot, entry.Name())
		info, err := root.Lstat(sourcePath)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ADR tree path %s must not be a symlink", sourcePath)
		}
		switch {
		case info.IsDir():
			if err := root.Mkdir(destinationPath, info.Mode().Perm()|0o700); err != nil {
				return err
			}
			if err := copyADRBatchTree(root, sourcePath, destinationPath); err != nil {
				return err
			}
			if err := chmodADRBatchDirectory(root, destinationPath, info.Mode().Perm()); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			body, err := root.ReadFile(sourcePath)
			if err != nil {
				return err
			}
			if err := atomicfile.WriteFileRoot(root, destinationPath, body, info.Mode().Perm()); err != nil {
				return err
			}
		default:
			return fmt.Errorf("ADR tree path %s must be a regular file or directory", sourcePath)
		}
	}

	return nil
}

func digestADRBatchTree(root *os.Root, treePath string) (string, error) {
	hash := sha256.New()
	rootInfo, err := root.Lstat(treePath)
	if err != nil {
		return "", err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return "", fmt.Errorf("ADR tree must be a directory and must not be a symlink")
	}
	_, _ = fmt.Fprintf(hash, "root\x00%04o\x00", rootInfo.Mode().Perm())
	err = fs.WalkDir(root.FS(), filepath.ToSlash(treePath), func(fsPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fsPath == filepath.ToSlash(treePath) {
			return nil
		}
		path := filepath.FromSlash(fsPath)
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ADR tree path %s must not be a symlink", path)
		}
		relPath, err := filepath.Rel(treePath, path)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			_, _ = fmt.Fprintf(hash, "dir\x00%s\x00%04o\x00", filepath.ToSlash(relPath), info.Mode().Perm())
			return nil
		case info.Mode().IsRegular():
			_, _ = fmt.Fprintf(hash, "file\x00%s\x00%04o\x00%d\x00", filepath.ToSlash(relPath), info.Mode().Perm(), info.Size())
			file, err := root.Open(path)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			_, _ = hash.Write([]byte{0})

			return nil
		default:
			return fmt.Errorf("ADR tree path %s must be a regular file or directory", path)
		}
	})
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyADRBatchTree(root *os.Root, treePath, expectedDigest string) error {
	actualDigest, err := digestADRBatchTree(root, treePath)
	if err != nil {
		return err
	}
	if actualDigest != expectedDigest {
		return fmt.Errorf("ADR tree digest %s does not match prepared digest %s", actualDigest, expectedDigest)
	}

	return nil
}

func writeADRBatchManifest(root *os.Root, path string, manifest adrBatchManifest) error {
	body, err := marshalADRBatchManifest(manifest)
	if err != nil {
		return err
	}

	return atomicfile.WriteFileRoot(root, path, body, 0o600)
}

func marshalADRBatchManifest(manifest adrBatchManifest) ([]byte, error) {
	if err := validateADRBatchManifest(manifest); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}

	return append(body, '\n'), nil
}

func verifyExactADRBatchManifest(root *os.Root, path string, expected adrBatchManifest) error {
	expectedBody, err := marshalADRBatchManifest(expected)
	if err != nil {
		return err
	}
	actualBody, err := readEffortArtifactRoot(root, path)
	if err != nil {
		return err
	}
	if !bytes.Equal(actualBody, expectedBody) {
		return fmt.Errorf("ADR batch manifest does not match the expected %q state", expected.State)
	}
	if _, err := readADRBatchManifest(root, path); err != nil {
		return err
	}

	return nil
}

func restorePreparedADRBatchManifest(root *os.Root, path string, prepared adrBatchManifest) error {
	prepared.State = adrBatchStatePrepared
	writeErr := writeADRBatchManifest(root, path, prepared)
	if writeErr != nil && !artifactWriteCommitted(writeErr) {
		return writeErr
	}
	if err := verifyExactADRBatchManifest(root, path, prepared); err != nil {
		return err
	}
	if writeErr != nil {
		if err := atomicfile.SyncDirRoot(root, filepath.Dir(path)); err != nil {
			return fmt.Errorf("sync restored ADR batch manifest directory: %w", err)
		}
	}

	return nil
}

func readADRBatchManifest(root *os.Root, path string) (adrBatchManifest, error) {
	info, err := root.Lstat(path)
	if err != nil {
		return adrBatchManifest{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return adrBatchManifest{}, fmt.Errorf("ADR batch manifest must be a regular file and must not be a symlink")
	}
	body, err := root.ReadFile(path)
	if err != nil {
		return adrBatchManifest{}, err
	}
	var manifest adrBatchManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return adrBatchManifest{}, err
	}
	if err := validateADRBatchManifest(manifest); err != nil {
		return adrBatchManifest{}, err
	}

	return manifest, nil
}

func validateADRBatchManifest(manifest adrBatchManifest) error {
	if manifest.SchemaVersion != adrBatchManifestSchema {
		return fmt.Errorf("unsupported ADR batch manifest schema %q", manifest.SchemaVersion)
	}
	if manifest.State != adrBatchStatePrepared && manifest.State != adrBatchStateCommitted {
		return fmt.Errorf("unsupported ADR batch manifest state %q", manifest.State)
	}
	if manifest.PreviousExists {
		if !validWorkflowArtifactDigest(manifest.PreviousDigest) {
			return fmt.Errorf("ADR batch manifest has invalid previous digest")
		}
	} else if manifest.PreviousDigest != "" {
		return fmt.Errorf("ADR batch manifest records a previous digest without a previous tree")
	}
	if !validWorkflowArtifactDigest(manifest.CandidateDigest) {
		return fmt.Errorf("ADR batch manifest has invalid candidate digest")
	}

	return nil
}

func adrBatchDirectoryExists(root *os.Root, path string) (bool, error) {
	info, err := root.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("ADR batch path %s must be a directory and must not be a symlink", path)
	}

	return true, nil
}

func chmodADRBatchDirectory(root *os.Root, path string, perm os.FileMode) error {
	before, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
		return fmt.Errorf("ADR batch path %s must be a directory and must not be a symlink", path)
	}

	directory, err := root.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()

	opened, openErr := directory.Stat()
	after, afterErr := root.Lstat(path)
	if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		return fmt.Errorf("ADR batch directory %s changed while opening", path)
	}
	if err := directory.Chmod(perm.Perm()); err != nil {
		return err
	}
	finalInfo, err := root.Lstat(path)
	if err != nil || !os.SameFile(opened, finalInfo) {
		return fmt.Errorf("ADR batch directory %s changed while setting permissions", path)
	}

	return nil
}

func syncADRBatchTree(root *os.Root, treePath string) error {
	var directories []string
	err := fs.WalkDir(root.FS(), filepath.ToSlash(treePath), func(fsPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("ADR batch staging path %s must not be a symlink", fsPath)
		}
		if entry.IsDir() {
			directories = append(directories, filepath.FromSlash(fsPath))
		}

		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, directory := range directories {
		if err := atomicfile.SyncDirRoot(root, directory); err != nil {
			return err
		}
	}

	return nil
}

// syncADRBatchRollbackDestination makes the rollback name durable before the
// source name can be durably removed from the effort root.
func syncADRBatchRollbackDestination(root *os.Root, paths adrBatchPaths) error {
	if err := atomicfile.SyncDirRoot(root, paths.transaction); err != nil {
		return fmt.Errorf("sync ADR transaction directory: %w", err)
	}
	if err := atomicfile.SyncDirRoot(root, "."); err != nil {
		return fmt.Errorf("sync effort root: %w", err)
	}

	return nil
}

// syncADRBatchPublishedDestination makes the effort-root destination durable
// before the transaction source unlink is flushed. Recovery and rollback use
// the same ordering so the previous generation is never durably unlinked
// before its restored name is durable.
func syncADRBatchPublishedDestination(root *os.Root, paths adrBatchPaths) error {
	if err := atomicfile.SyncDirRoot(root, "."); err != nil {
		return fmt.Errorf("sync effort root: %w", err)
	}
	if err := atomicfile.SyncDirRoot(root, paths.transaction); err != nil {
		return fmt.Errorf("sync ADR transaction directory: %w", err)
	}

	return nil
}

func removeADRBatchTransaction(root *os.Root, paths adrBatchPaths) error {
	info, err := root.Lstat(paths.transaction)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("ADR batch transaction path must be a directory and must not be a symlink")
	}
	if err := root.RemoveAll(paths.transaction); err != nil {
		return err
	}

	return atomicfile.SyncDirRoot(root, ".")
}
