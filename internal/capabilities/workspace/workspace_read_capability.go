package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"idiom/internal/platform/safepath"
	"idiom/internal/platform/textutil"
)

var errWorkspaceReadLimitExceeded = errors.New("workspace read byte limit exceeded")

type workspaceReadCheckpointStage string

const workspaceReadBeforeOpen workspaceReadCheckpointStage = "before_open"
const workspaceReadDuringLimitedRead workspaceReadCheckpointStage = "during_limited_read"

type workspaceReadCheckpoint struct {
	Stage workspaceReadCheckpointStage
	Path  string
}

type workspaceReadCheckpointFunc func(workspaceReadCheckpoint)

type workspaceReadCheckpointContextKey struct{}

func workspaceReadCheckpointFromContext(ctx context.Context) workspaceReadCheckpointFunc {
	if ctx == nil {
		return nil
	}

	checkpoint, _ := ctx.Value(workspaceReadCheckpointContextKey{}).(workspaceReadCheckpointFunc)
	return checkpoint
}

type workspaceReadAuthorityKind string

const (
	workspaceReadAuthorityWorkspace workspaceReadAuthorityKind = "workspace"
	workspaceReadAuthorityGrant     workspaceReadAuthorityKind = "system_grant"
	workspaceReadAuthorityTrusted   workspaceReadAuthorityKind = "trusted_path"
)

type workspaceReadAuthorization struct {
	candidate         string
	canonical         string
	targetIdentity    os.FileInfo
	authorityKind     workspaceReadAuthorityKind
	authorityPath     string
	authorityIdentity os.FileInfo
	scope             SystemSearchAccessScope
}

// workspaceReadCapability keeps authorization and an opened filesystem object
// alive for the same operation. Callers consume files through the bound handle
// and directories through the bound os.Root, then Validate before projecting
// any result.
type workspaceReadCapability struct {
	access        *PathAccess
	identity      executionSessionIdentity
	requestPath   string
	workdir       string
	trusted       bool
	authorization workspaceReadAuthorization
	file          *os.File
	root          *os.Root
	opened        os.FileInfo
	closeOnce     sync.Once
}

func (a *PathAccess) openReadCapability(ctx context.Context, identity executionSessionIdentity, path string, workdir string) (*workspaceReadCapability, error) {
	return a.openReadCapabilityWithAuthority(ctx, identity, path, workdir, false)
}

// openTrustedReadCapability is reserved for the existing user-owned global
// conversation-log paths. It binds the exact selected identity but does not
// broaden normal workspace or system-search grant policy.
func (a *PathAccess) openTrustedReadCapability(ctx context.Context, path string) (*workspaceReadCapability, error) {
	return a.openReadCapabilityWithAuthority(ctx, executionSessionIdentity{}, path, "", true)
}

func (a *PathAccess) openReadCapabilityWithAuthority(ctx context.Context, identity executionSessionIdentity, path string, workdir string, trusted bool) (*workspaceReadCapability, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	authorization, err := a.authorizeReadTarget(identity, path, workdir, trusted)
	if err != nil {
		return nil, err
	}
	if checkpoint := workspaceReadCheckpointFromContext(ctx); checkpoint != nil {
		checkpoint(workspaceReadCheckpoint{Stage: workspaceReadBeforeOpen, Path: authorization.candidate})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	current, err := a.authorizeReadTarget(identity, path, workdir, trusted)
	if err != nil {
		return nil, err
	}
	if !sameWorkspaceReadAuthorization(authorization, current) {
		return nil, fmt.Errorf("read target or its authority changed before open")
	}

	capability := &workspaceReadCapability{
		access:        a,
		identity:      identity,
		requestPath:   path,
		workdir:       workdir,
		trusted:       trusted,
		authorization: current,
	}
	if current.targetIdentity.IsDir() {
		if os.SameFile(current.targetIdentity, current.authorityIdentity) {
			capability.root, err = safepath.OpenRootPath(current.canonical, "read target")
		} else {
			capability.root, err = safepath.OpenRootUnderRoot(current.authorityPath, current.canonical, "read target", safepath.Options{})
		}
		if err == nil {
			capability.opened, err = capability.root.Stat(".")
		}
	} else {
		rootPath := current.authorityPath
		if !current.authorityIdentity.IsDir() {
			rootPath = filepath.Dir(current.authorityPath)
		}
		capability.file, err = safepath.OpenFileUnderRoot(rootPath, current.canonical, "read target", safepath.Options{})
		if err == nil {
			capability.opened, err = capability.file.Stat()
		}
	}
	if err != nil {
		capability.Close()
		return nil, err
	}
	if !os.SameFile(current.targetIdentity, capability.opened) {
		capability.Close()
		return nil, fmt.Errorf("read target changed while opening")
	}
	if err := capability.Validate(); err != nil {
		capability.Close()
		return nil, err
	}

	return capability, nil
}

func (a *PathAccess) authorizeReadTarget(identity executionSessionIdentity, path string, workdir string, trusted bool) (workspaceReadAuthorization, error) {
	if !a.Enabled() {
		return workspaceReadAuthorization{}, fmt.Errorf("workspace root is not configured")
	}

	candidate, err := a.readCandidatePath(path, workdir)
	if err != nil {
		return workspaceReadAuthorization{}, err
	}
	canonical, err := safepath.CanonicalExistingPath(candidate)
	if err != nil {
		return workspaceReadAuthorization{}, err
	}
	canonical = safepath.CleanPath(canonical)
	targetIdentity, err := inspectFilesystemPathIdentity(canonical)
	if err != nil {
		return workspaceReadAuthorization{}, err
	}

	authorization := workspaceReadAuthorization{
		candidate:      candidate,
		canonical:      canonical,
		targetIdentity: targetIdentity,
	}
	if trusted {
		authorization.authorityKind = workspaceReadAuthorityTrusted
		authorization.authorityPath = canonical
		authorization.authorityIdentity = targetIdentity
		return authorization, nil
	}

	workspaceRoot, err := safepath.CanonicalExistingPath(a.cfg.Root)
	if err == nil {
		workspaceRoot = safepath.CleanPath(workspaceRoot)
		if _, within := workspaceRelativePath(workspaceRoot, canonical); within {
			rootIdentity, identityErr := inspectFilesystemPathIdentity(workspaceRoot)
			if identityErr != nil {
				return workspaceReadAuthorization{}, identityErr
			}

			authorization.authorityKind = workspaceReadAuthorityWorkspace
			authorization.authorityPath = workspaceRoot
			authorization.authorityIdentity = rootIdentity
			return authorization, nil
		}
	}

	for _, grant := range a.ApprovedSystemSearchGrantsSnapshot(identity) {
		grantPath := safepath.CleanPath(grant.Path)
		grantCanonical, evalErr := safepath.CanonicalExistingPath(grantPath)
		if evalErr != nil {
			continue
		}
		grantCanonical = safepath.CleanPath(grantCanonical)
		grantIdentity, identityErr := inspectFilesystemPathIdentity(grantCanonical)
		if identityErr != nil || grant.Identity == nil || !os.SameFile(grant.Identity, grantIdentity) {
			continue
		}
		scope := normalizeSystemSearchAccessScope(grant.Scope)
		if !systemSearchGrantCoversPath(grantCanonical, canonical, scope) {
			continue
		}

		authorization.authorityKind = workspaceReadAuthorityGrant
		authorization.authorityPath = grantCanonical
		authorization.authorityIdentity = grantIdentity
		authorization.scope = scope
		return authorization, nil
	}

	return workspaceReadAuthorization{}, fmt.Errorf("path %q escapes workspace root and has not been approved for system search", path)
}

func (a *PathAccess) readCandidatePath(path string, workdir string) (string, error) {
	path = safepath.CleanPath(strings.TrimSpace(path))
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	if filepath.IsAbs(path) {
		return safepath.CleanPath(filepath.Clean(path)), nil
	}
	base := safepath.CleanPath(strings.TrimSpace(a.cfg.CWD))
	if base == "." || base == "" {
		base = safepath.CleanPath(a.cfg.Root)
	}
	if cleanedWorkdir := safepath.CleanPath(strings.TrimSpace(workdir)); cleanedWorkdir != "" && cleanedWorkdir != "." {
		if filepath.IsAbs(cleanedWorkdir) {
			base = safepath.CleanPath(filepath.Clean(cleanedWorkdir))
		} else {
			base = safepath.CleanPath(filepath.Join(base, cleanedWorkdir))
		}
	}

	return safepath.CleanPath(filepath.Clean(filepath.Join(base, path))), nil
}

func sameWorkspaceReadAuthorization(expected workspaceReadAuthorization, current workspaceReadAuthorization) bool {
	return expected.authorityKind == current.authorityKind &&
		expected.scope == current.scope &&
		expected.targetIdentity != nil && current.targetIdentity != nil &&
		expected.authorityIdentity != nil && current.authorityIdentity != nil &&
		os.SameFile(expected.targetIdentity, current.targetIdentity) &&
		os.SameFile(expected.authorityIdentity, current.authorityIdentity)
}

func (c *workspaceReadCapability) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		if c.file != nil {
			_ = c.file.Close()
		}
		if c.root != nil {
			_ = c.root.Close()
		}
	})
}

func (c *workspaceReadCapability) Path() string {
	if c == nil {
		return ""
	}
	return c.authorization.candidate
}

func (c *workspaceReadCapability) Info() os.FileInfo {
	if c == nil {
		return nil
	}
	return c.opened
}

func (c *workspaceReadCapability) SelectedDirectoryOnly() bool {
	return c != nil && c.opened != nil && c.opened.IsDir() && c.authorization.authorityKind == workspaceReadAuthorityGrant && c.authorization.scope == SystemSearchAccessScopeSelected
}

func (c *workspaceReadCapability) Validate() error {
	if c == nil || c.access == nil || c.opened == nil {
		return fmt.Errorf("read capability is not open")
	}

	current, err := c.access.authorizeReadTarget(c.identity, c.requestPath, c.workdir, c.trusted)
	if err != nil {
		return err
	}
	if !sameWorkspaceReadAuthorization(c.authorization, current) || !os.SameFile(c.opened, current.targetIdentity) {
		return fmt.Errorf("read target or its authority changed while in use")
	}
	if c.root != nil {
		opened, err := c.root.Stat(".")
		if err != nil {
			return err
		}
		if !os.SameFile(c.opened, opened) {
			return fmt.Errorf("opened read directory identity changed")
		}
	}
	if c.file != nil {
		opened, err := c.file.Stat()
		if err != nil {
			return err
		}
		if !os.SameFile(c.opened, opened) {
			return fmt.Errorf("opened read file identity changed")
		}
	}

	return nil
}

func (c *workspaceReadCapability) FS() (fs.FS, error) {
	if c == nil || c.root == nil {
		return nil, fmt.Errorf("read capability target is not a directory")
	}
	return c.root.FS(), nil
}

func (c *workspaceReadCapability) ReadDir(relative string) ([]os.DirEntry, error) {
	if c == nil || c.root == nil {
		return nil, fmt.Errorf("read capability target is not a directory")
	}
	entries, err := fs.ReadDir(c.root.FS(), filepath.ToSlash(cleanReadCapabilityRelative(relative)))
	if err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *workspaceReadCapability) ReadAll() ([]byte, error) {
	if c == nil || c.file == nil {
		return nil, fmt.Errorf("read capability target is not a file")
	}
	if _, err := c.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(c.file)
	if err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *workspaceReadCapability) ReadAllLimit(ctx context.Context, maxBytes int) ([]byte, error) {
	if c == nil || c.file == nil {
		return nil, fmt.Errorf("read capability target is not a file")
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("read byte limit must be non-negative")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := c.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	capacity := maxBytes
	if capacity > 32*1024 {
		capacity = 32 * 1024
	}
	body := make([]byte, 0, capacity)
	bufferSize := maxBytes + 1
	if bufferSize > 32*1024 {
		bufferSize = 32 * 1024
	}
	if bufferSize < 1 {
		bufferSize = 1
	}
	buffer := make([]byte, bufferSize)
	checkpointReached := false
	for len(body) <= maxBytes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := maxBytes + 1 - len(body)
		chunk := buffer
		if remaining < len(chunk) {
			chunk = chunk[:remaining]
		}
		read, err := c.file.Read(chunk)
		if read > 0 {
			body = append(body, chunk[:read]...)
			if !checkpointReached {
				checkpointReached = true
				if checkpoint := workspaceReadCheckpointFromContext(ctx); checkpoint != nil {
					checkpoint(workspaceReadCheckpoint{Stage: workspaceReadDuringLimitedRead, Path: c.authorization.candidate})
				}
			}
			if len(body) > maxBytes {
				return nil, errWorkspaceReadLimitExceeded
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if read == 0 {
			return nil, io.ErrNoProgress
		}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return body, nil
}

type workspaceReadLineWindow struct {
	startLine      int
	endLine        int
	totalLines     int
	clamped        bool
	body           []byte
	revisionSHA256 [sha256.Size]byte
	windowSHA256   [sha256.Size]byte
}

type workspaceReadFileScan struct {
	totalLines     int
	sizeBytes      int64
	body           []byte
	revisionSHA256 [sha256.Size]byte
	windowSHA256   [sha256.Size]byte
}

// ReadLineWindow scans the complete opened revision without retaining it. A
// first pass hashes and counts lines; a second pass retains only the clamped
// requested window while recomputing the revision hash. Both passes are
// context-aware and the opened identity/version must remain stable throughout.
func (c *workspaceReadCapability) ReadLineWindow(ctx context.Context, requestedStart int, requestedEnd int, maxWindowBytes int, maxWindowLines int) (workspaceReadLineWindow, error) {
	return c.readLineWindow(ctx, requestedStart, requestedEnd, maxWindowBytes, maxWindowLines, true)
}

// InspectLineWindow recomputes revision and window digests without retaining
// source bytes. It is used for freshness checks whose caller already owns the
// exact attachment body.
func (c *workspaceReadCapability) InspectLineWindow(ctx context.Context, requestedStart int, requestedEnd int) (workspaceReadLineWindow, error) {
	return c.readLineWindow(ctx, requestedStart, requestedEnd, 0, 0, false)
}

func (c *workspaceReadCapability) readLineWindow(ctx context.Context, requestedStart int, requestedEnd int, maxWindowBytes int, maxWindowLines int, retainWindow bool) (workspaceReadLineWindow, error) {
	if c == nil || c.file == nil {
		return workspaceReadLineWindow{}, fmt.Errorf("read capability target is not a file")
	}
	if maxWindowBytes < 0 {
		return workspaceReadLineWindow{}, fmt.Errorf("read window byte limit must be non-negative")
	}
	if maxWindowLines < 0 {
		return workspaceReadLineWindow{}, fmt.Errorf("read window line limit must be non-negative")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	before, err := c.file.Stat()
	if err != nil {
		return workspaceReadLineWindow{}, err
	}
	first, err := c.scanReadFile(ctx, 0, 0, 0, false)
	if err != nil {
		return workspaceReadLineWindow{}, err
	}
	middle, err := c.file.Stat()
	if err != nil {
		return workspaceReadLineWindow{}, err
	}
	if !sameOpenedReadFileVersion(before, middle) {
		return workspaceReadLineWindow{}, fmt.Errorf("opened read file changed during revision scan")
	}

	start := requestedStart
	end := requestedEnd
	clampedToBounds := false
	if first.totalLines == 0 {
		start, end = 0, 0
	} else {
		if start <= 0 {
			start = 1
		}
		if end <= 0 {
			end = first.totalLines
		}
		start, end, clampedToBounds = textutil.ClampRequestedLineRange(first.totalLines, start, end, requestedStart, requestedEnd)
		if start > end {
			return workspaceReadLineWindow{}, fmt.Errorf("start_line must be <= end_line")
		}
		if maxWindowLines > 0 && end-start+1 > maxWindowLines {
			end = start + maxWindowLines - 1
			clampedToBounds = true
		}
	}
	clamped := clampedToBounds ||
		(start != requestedStart && requestedStart > 0) ||
		(end != requestedEnd && requestedEnd > 0)

	second, err := c.scanReadFile(ctx, start, end, maxWindowBytes, retainWindow)
	if err != nil {
		return workspaceReadLineWindow{}, err
	}
	after, err := c.file.Stat()
	if err != nil {
		return workspaceReadLineWindow{}, err
	}
	if !sameOpenedReadFileVersion(middle, after) ||
		first.sizeBytes != second.sizeBytes ||
		first.totalLines != second.totalLines ||
		first.revisionSHA256 != second.revisionSHA256 {
		return workspaceReadLineWindow{}, fmt.Errorf("opened read file changed between revision and window scans")
	}
	if err := c.Validate(); err != nil {
		return workspaceReadLineWindow{}, err
	}
	body := second.body
	if len(body) > 0 && body[len(body)-1] == '\n' {
		body = body[:len(body)-1]
	}
	if retainWindow && len(body) > maxWindowBytes {
		return workspaceReadLineWindow{}, errWorkspaceReadLimitExceeded
	}
	return workspaceReadLineWindow{
		startLine: start, endLine: end, totalLines: first.totalLines, clamped: clamped,
		body: append([]byte(nil), body...), revisionSHA256: first.revisionSHA256, windowSHA256: second.windowSHA256,
	}, nil
}

func (c *workspaceReadCapability) scanReadFile(ctx context.Context, captureStart int, captureEnd int, maxCaptureBytes int, retainCapture bool) (workspaceReadFileScan, error) {
	if _, err := c.file.Seek(0, io.SeekStart); err != nil {
		return workspaceReadFileScan{}, err
	}
	hash := sha256.New()
	windowHash := sha256.New()
	buffer := make([]byte, 32*1024)
	scan := workspaceReadFileScan{}
	currentLine := 1
	lastWasNewline := false
	checkpointReached := false
	windowDigestBuffer := make([]byte, 0, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return workspaceReadFileScan{}, err
		}
		read, readErr := c.file.Read(buffer)
		if read > 0 {
			chunk := buffer[:read]
			_, _ = hash.Write(chunk)
			scan.sizeBytes += int64(read)
			if !checkpointReached {
				checkpointReached = true
				if checkpoint := workspaceReadCheckpointFromContext(ctx); checkpoint != nil {
					checkpoint(workspaceReadCheckpoint{Stage: workspaceReadDuringLimitedRead, Path: c.authorization.candidate})
				}
			}
			for _, value := range chunk {
				if captureStart > 0 && currentLine >= captureStart && currentLine <= captureEnd {
					windowDigestBuffer = append(windowDigestBuffer, value)
					if len(windowDigestBuffer) == cap(windowDigestBuffer) {
						_, _ = windowHash.Write(windowDigestBuffer[:len(windowDigestBuffer)-1])
						last := windowDigestBuffer[len(windowDigestBuffer)-1]
						windowDigestBuffer = append(windowDigestBuffer[:0], last)
					}
					if retainCapture {
						scan.body = append(scan.body, value)
						if len(scan.body) > maxCaptureBytes+1 {
							return workspaceReadFileScan{}, errWorkspaceReadLimitExceeded
						}
					}
				}
				lastWasNewline = value == '\n'
				if lastWasNewline {
					scan.totalLines++
					currentLine++
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return workspaceReadFileScan{}, readErr
		}
		if read == 0 {
			return workspaceReadFileScan{}, io.ErrNoProgress
		}
	}
	if scan.sizeBytes > 0 && !lastWasNewline {
		scan.totalLines++
	}
	if len(windowDigestBuffer) > 0 && windowDigestBuffer[len(windowDigestBuffer)-1] == '\n' {
		windowDigestBuffer = windowDigestBuffer[:len(windowDigestBuffer)-1]
	}
	_, _ = windowHash.Write(windowDigestBuffer)
	copy(scan.revisionSHA256[:], hash.Sum(nil))
	copy(scan.windowSHA256[:], windowHash.Sum(nil))
	return scan, nil
}

func sameOpenedReadFileVersion(left os.FileInfo, right os.FileInfo) bool {
	return left != nil && right != nil &&
		os.SameFile(left, right) &&
		left.Size() == right.Size() &&
		left.Mode() == right.Mode() &&
		left.ModTime().Equal(right.ModTime())
}

func (c *workspaceReadCapability) ReadFile(relative string) ([]byte, os.FileInfo, error) {
	if c == nil || c.root == nil {
		return nil, nil, fmt.Errorf("read capability target is not a directory")
	}
	relative = cleanReadCapabilityRelative(relative)
	file, err := safepath.OpenFileFromRoot(c.root, c.authorization.canonical, relative, "read target", safepath.Options{
		RejectSymlinkTarget: true,
	})
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, openErr := file.Stat()
	if openErr != nil {
		return nil, nil, openErr
	}
	if !opened.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("read target %q must be a regular file", filepath.ToSlash(relative))
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, nil, err
	}

	return data, opened, nil
}

func cleanReadCapabilityRelative(relative string) string {
	relative = filepath.Clean(strings.TrimSpace(relative))
	if relative == "" {
		return "."
	}
	return relative
}
