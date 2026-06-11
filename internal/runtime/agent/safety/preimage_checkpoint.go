package safety

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PreimageCheckpointStore captures file contents before a workspace write.
// GitCheckpoint remains the run-level audit snapshot; this store is the
// per-path rewind seam that write execution can call before mutation.
type PreimageCheckpointStore struct {
	Root      string
	snapshots map[string]preimageSnapshot
}

type preimageSnapshot struct {
	Exists bool
	Data   []byte
	Mode   os.FileMode
}

func NewPreimageCheckpointStore(root string) *PreimageCheckpointStore {
	return &PreimageCheckpointStore{Root: root, snapshots: make(map[string]preimageSnapshot)}
}

func (s *PreimageCheckpointStore) Snapshot(path string) error {
	if s == nil {
		return fmt.Errorf("preimage checkpoint store is nil")
	}
	normalized, full, err := s.resolve(path)
	if err != nil {
		return err
	}
	if _, ok := s.snapshots[normalized]; ok {
		return nil
	}
	info, statErr := os.Stat(full)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			s.snapshots[normalized] = preimageSnapshot{Exists: false}
			return nil
		}
		return statErr
	}
	if info.IsDir() {
		return fmt.Errorf("preimage checkpoint only supports files: %s", normalized)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	s.snapshots[normalized] = preimageSnapshot{Exists: true, Data: append([]byte(nil), data...), Mode: info.Mode()}
	return nil
}

func (s *PreimageCheckpointStore) Restore(path string) error {
	if s == nil {
		return fmt.Errorf("preimage checkpoint store is nil")
	}
	normalized, full, err := s.resolve(path)
	if err != nil {
		return err
	}
	snapshot, ok := s.snapshots[normalized]
	if !ok {
		return fmt.Errorf("no preimage checkpoint for %s", normalized)
	}
	if !snapshot.Exists {
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, snapshot.Data, snapshot.Mode)
}

func (s *PreimageCheckpointStore) Has(path string) bool {
	if s == nil {
		return false
	}
	normalized, _, err := s.resolve(path)
	if err != nil {
		return false
	}
	_, ok := s.snapshots[normalized]
	return ok
}

func (s *PreimageCheckpointStore) resolve(path string) (string, string, error) {
	root := strings.TrimSpace(s.Root)
	if root == "" {
		return "", "", fmt.Errorf("preimage checkpoint root is required")
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return "", "", fmt.Errorf("preimage checkpoint path is required")
	}
	if filepath.IsAbs(path) {
		return "", "", fmt.Errorf("preimage checkpoint path must be workspace-relative: %s", path)
	}
	normalized := filepath.ToSlash(path)
	if strings.HasPrefix(normalized, "../") || normalized == ".." {
		return "", "", fmt.Errorf("preimage checkpoint path escapes workspace: %s", path)
	}
	full := filepath.Join(root, filepath.FromSlash(normalized))
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(rootAbs, fullAbs)
	if err != nil {
		return "", "", err
	}
	if rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return "", "", fmt.Errorf("preimage checkpoint path escapes workspace: %s", path)
	}
	return normalized, fullAbs, nil
}
