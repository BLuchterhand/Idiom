package languagetools

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"idiom/internal/platform/atomicfile"
	"idiom/internal/platform/gitstate"
	semanticcpp "idiom/internal/repository/semantic/cpp"
)

const (
	cppCacheFormat       = 1
	cppCacheEntryLimit   = 64 << 20
	cppCacheHeaderLimit  = 1 << 20
	cppCacheDefaultBytes = 256 << 20
	cppCacheEntryCount   = 64
)

// Each entry is one atomic file: a small JSON metadata line followed by a
// checksummed JSON index. Metadata can be inspected without decoding all indexes.
type cppCacheHeader struct {
	Format    int
	Collector string
	Info      cppIndexInfo
	Complete  bool
	Checksum  string
}

type cppDiskCache struct{ root *os.Root }

func openCppDiskCache() (*cppDiskCache, error) {
	base := strings.TrimSpace(os.Getenv("IDIOM_CACHE_DIR"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, ".idiom", "cache")
	}
	dir := filepath.Join(base, "cpp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &cppDiskCache{root: root}, nil
}

func (c *cppDiskCache) read(key string, body bool) (cppCacheHeader, *semanticcpp.CppIndex, error) {
	var header cppCacheHeader
	if len(key) != 16 {
		return header, nil, fmt.Errorf("invalid C++ cache key")
	}
	if _, err := hex.DecodeString(key); err != nil {
		return header, nil, err
	}
	name := key + ".index"
	stat, err := c.root.Lstat(name)
	if err != nil {
		return header, nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() > cppCacheEntryLimit {
		return header, nil, fmt.Errorf("invalid C++ cache file")
	}
	f, err := c.root.Open(name)
	if err != nil {
		return header, nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(io.LimitReader(f, cppCacheEntryLimit+1), cppCacheHeaderLimit)
	line, err := r.ReadSlice('\n')
	if err != nil {
		return header, nil, err
	}
	if err := json.Unmarshal(line, &header); err != nil {
		return header, nil, err
	}
	if header.Format != cppCacheFormat || header.Collector != cppCollectorVersion || header.Info.Key != key {
		return header, nil, fmt.Errorf("incompatible C++ cache entry")
	}
	if !body {
		return header, nil, nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return header, nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != header.Checksum {
		return header, nil, fmt.Errorf("C++ cache checksum mismatch")
	}
	var index semanticcpp.CppIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return header, nil, err
	}
	// Cache paths are untrusted. Query tools must never receive an escaping path.
	for _, file := range index.Files {
		if !cppCachePath(file.Path) {
			return header, nil, fmt.Errorf("invalid cached C++ path")
		}
	}
	for _, path := range index.Paths {
		if !cppCachePath(path) {
			return header, nil, fmt.Errorf("invalid cached C++ path")
		}
	}
	return header, &index, nil
}

func cppCachePath(path string) bool {
	return path != "." && path != "" && filepath.IsLocal(filepath.FromSlash(path))
}

func (c *cppDiskCache) touch(key string) {
	now := time.Now()
	_ = c.root.Chtimes(key+".index", now, now)
}

func (c *cppDiskCache) entries() ([]os.DirEntry, error) {
	dir, err := c.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.ReadDir(-1)
}

// nearest selects the index requiring the fewest changed-file parses. Dirty
// and ignored files from both snapshots are compared in addition to git diff;
// a cached dirty file must also be reparsed when it becomes clean again.
func (c *cppDiskCache) nearest(ctx context.Context, info cppIndexInfo) (*semanticcpp.CppIndex, map[string]bool) {
	entries, err := c.entries()
	if err != nil {
		return nil, nil
	}
	bestCount := int(^uint(0) >> 1)
	var bestKey string
	var bestTime time.Time
	var bestChanged map[string]bool
	diffs := make(map[string][]string)
	for _, entry := range entries {
		if ctx.Err() != nil {
			break
		}
		if !strings.HasSuffix(entry.Name(), ".index") {
			continue
		}
		key := strings.TrimSuffix(entry.Name(), ".index")
		h, _, err := c.read(key, false)
		if err != nil || !h.Complete || h.Info.GitCommonDir != info.GitCommonDir {
			continue
		}
		paths, ok := diffs[h.Info.Tree]
		if !ok {
			if h.Info.Tree != info.Tree {
				args := append([]string{"diff", "--name-only", "--no-renames", "-z", h.Info.Tree, info.Tree, "--"}, cppGitPathspecs...)
				out, err := gitstate.Output(ctx, info.Root, args...)
				if err != nil {
					continue
				}
				paths = strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
			}
			diffs[h.Info.Tree] = paths
		}
		changed := make(map[string]bool)
		for _, path := range paths {
			if path != "" {
				changed[path] = true
			}
		}
		for path, hash := range h.Info.ContentHashes {
			if info.ContentHashes[path] != hash {
				changed[path] = true
			}
		}
		for path, hash := range info.ContentHashes {
			if h.Info.ContentHashes[path] != hash {
				changed[path] = true
			}
		}
		stat, err := entry.Info()
		if err != nil {
			continue
		}
		if len(changed) < bestCount || (len(changed) == bestCount && stat.ModTime().After(bestTime)) {
			bestCount, bestKey, bestChanged = len(changed), key, changed
			bestTime = stat.ModTime()
		}
	}
	if bestKey == "" {
		return nil, nil
	}
	_, index, err := c.read(bestKey, true)
	if err != nil {
		return nil, nil
	}
	c.touch(bestKey)
	return index, bestChanged
}

// Concurrent processes may build independently. Publication and eviction are
// serialized by an OS lock (released even on process death). A partial build
// can never overwrite a completed index or a checkpoint with more parsed files.
func (c *cppDiskCache) store(info cppIndexInfo, index *semanticcpp.CppIndex, complete bool) error {
	data, err := json.Marshal(index)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	header := cppCacheHeader{Format: cppCacheFormat, Collector: cppCollectorVersion, Info: info, Complete: complete, Checksum: hex.EncodeToString(sum[:])}
	metadata, err := json.Marshal(header)
	if err != nil {
		return err
	}
	if len(metadata) >= cppCacheHeaderLimit || len(metadata)+len(data)+1 > cppCacheEntryLimit {
		return fmt.Errorf("C++ index exceeds cache entry limit (%d bytes)", cppCacheEntryLimit)
	}
	stat, err := c.root.Lstat("publish.lock")
	if err == nil && !stat.Mode().IsRegular() {
		return fmt.Errorf("invalid C++ cache lock")
	}
	lock, err := c.root.OpenFile("publish.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		locked, err := tryLockCppCache(lock)
		if err != nil {
			return err
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	old, previous, readErr := c.read(info.Key, true)
	if readErr == nil && (old.Complete || (!complete && len(previous.Files) >= len(index.Files))) {
		c.touch(info.Key)
		return nil
	}
	body := bytes.Join([][]byte{metadata, data}, []byte{'\n'})
	if int64(len(body)) > cppCacheMaxBytes() {
		return fmt.Errorf("C++ index exceeds cache size bound")
	}
	if err := atomicfile.WriteFileRoot(c.root, info.Key+".index", body, 0o600); err != nil {
		return err
	}
	return c.evict()
}

func cppCacheMaxBytes() int64 {
	if n, err := strconv.ParseInt(os.Getenv("IDIOM_CPP_CACHE_MAX_BYTES"), 10, 64); err == nil && n > 0 {
		return n
	}
	return cppCacheDefaultBytes
}

func (c *cppDiskCache) evict() error {
	entries, err := c.entries()
	if err != nil {
		return err
	}
	var files []os.FileInfo
	var total int64
	for _, entry := range entries {
		// Publication holds the only writer lock, so any leftover atomic-write
		// temporary is from an interrupted process and can be reclaimed.
		if strings.HasPrefix(entry.Name(), ".") && strings.Contains(entry.Name(), ".index-") && strings.HasSuffix(entry.Name(), ".tmp") {
			_ = c.root.Remove(entry.Name())
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".index") {
			continue
		}
		stat, err := entry.Info()
		if err != nil || !stat.Mode().IsRegular() {
			continue
		}
		files = append(files, stat)
		total += stat.Size()
	}
	slices.SortFunc(files, func(a, b os.FileInfo) int { return a.ModTime().Compare(b.ModTime()) })
	for len(files) > 0 && (total > cppCacheMaxBytes() || len(files) > cppCacheEntryCount) {
		if err := c.root.Remove(files[0].Name()); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= files[0].Size()
		files = files[1:]
	}
	return nil
}
