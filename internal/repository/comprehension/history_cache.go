package comprehension

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// historyCacheRelPath holds the parsed `git log` of one HEAD. The log is a
// pure function of HEAD, so HEAD's hash is the whole key; the ledger and the
// as_of cut are applied after reading it and never enter the cache.
const historyCacheRelPath = ".idiom/comprehension/history_cache.json"

type cachedCommit struct {
	Time    int64    `json:"t"`
	Hash    string   `json:"h"`
	Merge   bool     `json:"m,omitempty"`
	Message string   `json:"s"`
	Files   []string `json:"f"`
}

type historyCacheFile struct {
	Head     string         `json:"head"`
	HeadTime int64          `json:"head_time"`
	Commits  []cachedCommit `json:"commits"`
}

// cachedRawCommits returns readRawCommits' answer, from disk when the cache
// matches HEAD. A missing, stale or unreadable cache falls back to git, and a
// failed write is ignored: the cache only saves time.
func cachedRawCommits(ctx context.Context, root string) ([]rawCommit, int64) {
	head := gitHeadHash(root)
	path := filepath.Join(root, filepath.FromSlash(historyCacheRelPath))

	if head != "" {
		if commits, headTime, ok := readHistoryCache(path, head); ok {
			return commits, headTime
		}
	}

	commits, headTime := readRawCommits(ctx, root)
	if head != "" && len(commits) > 0 {
		writeHistoryCache(path, head, headTime, commits)
	}

	return commits, headTime
}

func readHistoryCache(path, head string) ([]rawCommit, int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false
	}

	var file historyCacheFile
	if json.Unmarshal(data, &file) != nil || file.Head != head {
		return nil, 0, false
	}

	commits := make([]rawCommit, len(file.Commits))
	for i, c := range file.Commits {
		commits[i] = rawCommit{time: c.Time, hash: c.Hash, merge: c.Merge, message: c.Message, files: c.Files}
	}

	return commits, file.HeadTime, true
}

func writeHistoryCache(path, head string, headTime int64, commits []rawCommit) {
	file := historyCacheFile{Head: head, HeadTime: headTime, Commits: make([]cachedCommit, len(commits))}
	for i, c := range commits {
		file.Commits[i] = cachedCommit{Time: c.time, Hash: c.hash, Merge: c.merge, Message: c.message, Files: c.files}
	}

	data, err := json.Marshal(file)
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "history_cache-*.tmp")
	if err != nil {
		return
	}

	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr != nil || cerr != nil || os.Rename(tmp.Name(), path) != nil {
		_ = os.Remove(tmp.Name())
	}
}
