package adapters

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"idiom/internal/repository/planartifactstore"
	"idiom/internal/workflows/planexecute"
)

func FindLatestPlanArtifact(root string, notBefore time.Time, sessionID string) (string, error) {
	dir, _, err := planartifactstore.StoreDir(root)
	if err != nil {
		return "", fmt.Errorf("plan dir: %w", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w: plan dir %q", planexecute.ErrPlanArtifactNotFound, dir)
		}

		return "", fmt.Errorf("plan dir %q: %w", dir, err)
	}

	type candidate struct {
		path  string
		mtime time.Time
	}

	sessionID = strings.TrimSpace(sessionID)

	var hits []candidate

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if name == "last.plan.json" || !strings.HasSuffix(name, ".plan.json") {
			continue
		}

		info, err := entry.Info()
		if err != nil || (!notBefore.IsZero() && info.ModTime().Before(notBefore)) {
			continue
		}

		path, _, err := planartifactstore.StoreFilePath(root, name)
		if err != nil {
			continue
		}

		if sessionID != "" {
			body, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			var head struct {
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal(body, &head); err != nil || strings.TrimSpace(head.SessionID) != sessionID {
				continue
			}
		}

		hits = append(hits, candidate{path: path, mtime: info.ModTime()})
	}

	if len(hits) == 0 {
		if sessionID != "" {
			return "", fmt.Errorf("%w: no plan artifacts under %s written since %s with session_id=%s", planexecute.ErrPlanArtifactNotFound, dir, notBefore.Format(time.RFC3339), sessionID)
		}

		return "", fmt.Errorf("%w: no plan artifacts under %s written since %s", planexecute.ErrPlanArtifactNotFound, dir, notBefore.Format(time.RFC3339))
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].mtime.After(hits[j].mtime) })

	return hits[0].path, nil
}

func CleanupStalePlanArtifacts(root string, threshold time.Duration, logOut io.Writer) {
	dir, _, err := planartifactstore.StoreDir(root)
	if err != nil {
		fmt.Fprintf(logOut, "plan-execute cleanup: resolve plan dir: %v\n", err)
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(logOut, "plan-execute cleanup: read %s: %v\n", dir, err)
		}
		return
	}

	cutoff := time.Now().Add(-threshold)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if name == "last.plan.json" || !strings.HasSuffix(name, ".plan.json") {
			continue
		}

		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}

		path, _, err := planartifactstore.StoreFilePath(root, name)
		if err != nil {
			fmt.Fprintf(logOut, "plan-execute cleanup: resolve %s: %v\n", name, err)
			continue
		}
		if err := os.Remove(path); err != nil {
			fmt.Fprintf(logOut, "plan-execute cleanup: remove %s: %v\n", path, err)
		}
	}
}
