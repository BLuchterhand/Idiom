package comprehension

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LearningLogEntry is one record in the per-workspace ranker learning log.
// The log lets every trigger site (eval oracle, CLI, future verified-edit
// hook) leave an auditable trail without coupling them to each other.
type LearningLogEntry struct {
	Timestamp string   `json:"timestamp"`
	Source    string   `json:"source"`
	Action    string   `json:"action"` // boost | penalize | reset | no_op
	Score     float64  `json:"score,omitempty"`
	Files     []string `json:"files,omitempty"`
	TaskID    string   `json:"task_id,omitempty"`
	Note      string   `json:"note,omitempty"`
}

const (
	learningLogFileName = "learning_log.jsonl"
)

func learningLogPath(root string) string {
	return filepath.Join(root, ".idiom", learnedWeightsDirSuffix, learningLogFileName)
}

// AppendLearningLog writes one entry to .idiom/ranker/learning_log.jsonl. The
// caller is responsible for filling Source/Action/Files; Timestamp defaults to
// now if empty.
func AppendLearningLog(root string, entry LearningLogEntry) error {
	if strings.TrimSpace(entry.Source) == "" {
		return errors.New("learning log entry needs a non-empty source")
	}
	if strings.TrimSpace(entry.Action) == "" {
		return errors.New("learning log entry needs a non-empty action")
	}
	if entry.Timestamp == "" {
		entry.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
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

// ReadLearningLog returns every entry in the log, oldest first, dropping
// malformed lines. Missing log returns an empty slice with no error.
func ReadLearningLog(root string) ([]LearningLogEntry, error) {
	path := learningLogPath(root)
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var out []LearningLogEntry
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry LearningLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		out = append(out, entry)
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// LearningLogSummary aggregates the log into the shape the harness audit
// surfaces. It is deliberately narrow: counts, the most recent entry per
// source, and the distinct sources observed so the audit can see which
// trigger sites are wired versus dormant.
type LearningLogSummary struct {
	TotalEntries    int                `json:"total_entries"`
	BoostCount      int                `json:"boost_count"`
	PenalizeCount   int                `json:"penalize_count"`
	NoOpCount       int                `json:"no_op_count"`
	ResetCount      int                `json:"reset_count"`
	BySource        map[string]int     `json:"by_source,omitempty"`
	LatestPerSource map[string]string  `json:"latest_per_source,omitempty"`
	LatestEntry     *LearningLogEntry  `json:"latest_entry,omitempty"`
	Recent          []LearningLogEntry `json:"recent,omitempty"`
}

// SummarizeLearningLog folds the log into a small summary for downstream
// reporting. The "recent" slice keeps the last 10 entries newest-first.
func SummarizeLearningLog(entries []LearningLogEntry) LearningLogSummary {
	summary := LearningLogSummary{
		BySource:        make(map[string]int),
		LatestPerSource: make(map[string]string),
	}
	for i := range entries {
		entry := entries[i]
		summary.TotalEntries++
		switch entry.Action {
		case "boost":
			summary.BoostCount++
		case "penalize":
			summary.PenalizeCount++
		case "reset":
			summary.ResetCount++
		default:
			summary.NoOpCount++
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
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Timestamp > sorted[j].Timestamp
	})
	if len(sorted) > 10 {
		sorted = sorted[:10]
	}
	summary.Recent = sorted
	return summary
}
