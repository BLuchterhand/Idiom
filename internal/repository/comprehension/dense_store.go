package comprehension

import (
	"context"
	"crypto/sha1" //nolint:gosec // git blob ids are SHA-1; not a security use
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// The dense index lives in dense.sqlite beside graph.sqlite, not inside it:
// embedding calls take seconds and must never hold the graph write
// transaction. It is synced after every graph write by diffing the graph's
// file fingerprints, like the lexical index, so an incremental refresh and a
// cold build store the same rows and an unchanged file is never re-read or
// re-embedded.
//
//	dense_meta     model key and completeness
//	dense_docs     one row per embedded file: path, graph fingerprint, vector key
//	dense_vectors  unit vectors keyed by sha1(path NUL git-blob-id of content)
//	dense_queries  bounded cache of embedded tasks
//
// A vector key names content, so a touched file keeps its vector and a
// renamed file re-embeds (the path is part of the embedded text). A shared
// per-user blob cache (same schema) lets a fresh worktree or a rebuilt
// index reuse vectors instead of re-embedding the tree.

const (
	denseQueryCacheRows = 512
	denseCacheDirEnv    = "IDIOM_DENSE_CACHE_DIR" // "off" disables the shared cache
	metaModel           = "model"
	metaComplete        = "complete"
)

func denseStorePath(root string) string {
	return filepath.Join(root, ".idiom", "comprehension", "dense.sqlite")
}

type denseStore struct{ db *sql.DB }

func openDenseStore(path string) (*denseStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	db, err := openSQLite(path)
	if err != nil {
		return nil, err
	}

	for _, stmt := range []string{
		// a derived cache: a crash costs a re-sync, never correctness, so
		// skip the per-row fsync that dominated cold syncs
		`PRAGMA synchronous = OFF`,
		`CREATE TABLE IF NOT EXISTS dense_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS dense_docs (path TEXT PRIMARY KEY, fingerprint TEXT NOT NULL, vec_key TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS dense_vectors (vec_key TEXT PRIMARY KEY, vec BLOB NOT NULL) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS dense_queries (qkey TEXT PRIMARY KEY, seq INTEGER NOT NULL, vec BLOB NOT NULL) WITHOUT ROWID`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			_ = db.Close()

			return nil, err
		}
	}

	return &denseStore{db: db}, nil
}

func (s *denseStore) Close() error { return s.db.Close() }

func (s *denseStore) meta(key string) string {
	var v string
	_ = s.db.QueryRow(`SELECT value FROM dense_meta WHERE key = ?`, key).Scan(&v)

	return v
}

func (s *denseStore) setMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO dense_meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)

	return err
}

// reset drops every vector and query when the model (digest or text
// construction) changed: vectors of different models are not comparable.
func (s *denseStore) reset(model DenseModel) error {
	for _, stmt := range []string{`DELETE FROM dense_docs`, `DELETE FROM dense_vectors`, `DELETE FROM dense_queries`, `DELETE FROM dense_meta`} {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}

	return s.setMeta(metaModel, model.key())
}

func encodeVector(v []float32) []byte {
	out := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(x))
	}

	return out
}

func float32frombytes(b []byte) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b)) }

func decodeVector(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}

	return out
}

// denseVectorKey is sha1(path NUL git blob id): content-addressed (the blob
// id is the one git would give the file) and path-specific.
func denseVectorKey(path string, content []byte) string {
	h := sha1.New() //nolint:gosec
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	blob := hex.EncodeToString(h.Sum(nil))
	sum := sha1.Sum([]byte(path + "\x00" + blob)) //nolint:gosec

	return hex.EncodeToString(sum[:])
}

// denseDocumentText is what is embedded for a file: its path, then its head.
// ok is false for files that are not text (binary, oversize, empty).
func denseDocumentText(path string, content []byte) (string, bool) {
	if len(content) == 0 || len(content) > LexicalMaxFileBytes || slices.Contains(content[:min(len(content), 8192)], 0) {
		return "", false
	}

	body := strings.ToValidUTF8(string(content), "�")
	if utf8.RuneCountInString(body) > denseHeadRunes {
		body = string([]rune(body)[:denseHeadRunes])
	}

	return path + "\n" + body, true
}

// sharedBlobCache opens the per-user vector cache for model, or nil.
func sharedBlobCache(model DenseModel) *denseStore {
	dir := os.Getenv(denseCacheDirEnv)
	if dir == "off" {
		return nil
	}

	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil
		}

		dir = filepath.Join(base, "idiom")
	}

	sum := sha256.Sum256([]byte(model.key()))

	store, err := openDenseStore(filepath.Join(dir, "dense-"+hex.EncodeToString(sum[:6])+".sqlite"))
	if err != nil {
		return nil
	}

	return store
}

func (s *denseStore) hasVector(key string) bool {
	var one int

	return s.db.QueryRow(`SELECT 1 FROM dense_vectors WHERE vec_key = ?`, key).Scan(&one) == nil
}

func (s *denseStore) vector(key string) ([]float32, bool) {
	var b []byte
	if err := s.db.QueryRow(`SELECT vec FROM dense_vectors WHERE vec_key = ?`, key).Scan(&b); err != nil {
		return nil, false
	}

	return decodeVector(b), true
}

// DenseSyncReport counts one sync's work.
type DenseSyncReport struct {
	Files, Embedded, Reused, Removed, Skipped int
}

type denseWant struct{ path, fingerprint string }

func denseWantedFiles(root string) ([]denseWant, error) {
	db, err := openSQLite(sqliteGraphPath(root))
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT path, COALESCE(MAX(fingerprint), '') FROM graph_nodes
		WHERE kind = 'file' AND path IS NOT NULL AND path <> '' GROUP BY path ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []denseWant

	for rows.Next() {
		var w denseWant
		if err := rows.Scan(&w.path, &w.fingerprint); err != nil {
			return nil, err
		}

		out = append(out, w)
	}

	return out, rows.Err()
}

// syncDenseIndexBestEffort runs after a graph write. A missing model or
// server leaves the store as it was; the query path reports it.
func syncDenseIndexBestEffort(root string) {
	if !denseEnabled() {
		return
	}

	embedder := denseEmbedderFactory()
	if embedder == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), denseEmbedTimeout*6)
	defer cancel()

	if _, err := SyncDenseIndex(ctx, root, embedder); err != nil {
		fmt.Fprintf(os.Stderr, "idiom: dense index not updated: %v\n", err)
	}
}

// SyncDenseIndex makes dense.sqlite describe exactly the files now in the
// graph. Vectors are written a batch at a time, so an interrupted sync
// resumes where it stopped.
func SyncDenseIndex(ctx context.Context, root string, embedder DenseEmbedder) (DenseSyncReport, error) {
	var report DenseSyncReport

	model, err := embedder.Model(ctx)
	if err != nil {
		return report, err
	}

	want, err := denseWantedFiles(root)
	if err != nil {
		return report, err
	}

	store, err := openDenseStore(denseStorePath(root))
	if err != nil {
		return report, err
	}
	defer store.Close()

	if store.meta(metaModel) != model.key() {
		if err := store.reset(model); err != nil {
			return report, err
		}
	}

	shared := sharedBlobCache(model)
	if shared != nil {
		defer shared.Close()
	}

	report.Files = len(want)

	if report.Removed, err = store.dropStaleDocs(want); err != nil {
		return report, err
	}

	if err := store.setMeta(metaComplete, "0"); err != nil {
		return report, err
	}

	have, err := store.docPaths()
	if err != nil {
		return report, err
	}

	var pending []densePending

	for _, w := range want {
		if _, ok := have[w.path]; ok && w.fingerprint != "" {
			continue
		}

		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(w.path)))
		text, ok := denseDocumentText(w.path, content)

		if err != nil || !ok {
			report.Skipped++

			continue
		}

		key := denseVectorKey(w.path, content)

		if store.hasVector(key) {
			if err := store.putDoc(w, key); err != nil {
				return report, err
			}

			report.Reused++

			continue
		}

		if shared != nil {
			if vec, ok := shared.vector(key); ok {
				if err := store.putVectorAndDoc(w, key, vec); err != nil {
					return report, err
				}

				report.Reused++

				continue
			}
		}

		pending = append(pending, densePending{w, key, text})
	}

	for start := 0; start < len(pending); start += denseBatch {
		batch := pending[start:min(start+denseBatch, len(pending))]
		texts := make([]string, len(batch))

		for i, p := range batch {
			texts[i] = p.text
		}

		batchCtx, cancel := context.WithTimeout(ctx, denseEmbedTimeout)
		vecs, err := embedder.Embed(batchCtx, texts, false)

		cancel()

		if err != nil {
			return report, err
		}

		for i, p := range batch {
			if err := store.putVectorAndDoc(p.denseWant, p.key, vecs[i]); err != nil {
				return report, err
			}

			if shared != nil {
				_ = shared.putVector(p.key, vecs[i])
			}

			report.Embedded++
		}
	}

	if _, err := store.db.Exec(`DELETE FROM dense_vectors WHERE vec_key NOT IN (SELECT vec_key FROM dense_docs)`); err != nil {
		return report, err
	}

	return report, store.setMeta(metaComplete, "1")
}

type densePending struct {
	denseWant
	key, text string
}

func (s *denseStore) docPaths() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT path, fingerprint FROM dense_docs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}

	for rows.Next() {
		var p, f string
		if err := rows.Scan(&p, &f); err != nil {
			return nil, err
		}

		out[p] = f
	}

	return out, rows.Err()
}

// dropStaleDocs deletes documents whose file is gone or whose content
// fingerprint changed (changed files are re-inserted by the caller).
func (s *denseStore) dropStaleDocs(want []denseWant) (int, error) {
	have, err := s.docPaths()
	if err != nil {
		return 0, err
	}

	current := make(map[string]string, len(want))
	for _, w := range want {
		current[w.path] = w.fingerprint
	}

	removed := 0

	for path, fp := range have {
		if cur, ok := current[path]; ok && cur == fp && cur != "" {
			continue
		}

		if _, err := s.db.Exec(`DELETE FROM dense_docs WHERE path = ?`, path); err != nil {
			return removed, err
		}

		removed++
	}

	return removed, nil
}

func (s *denseStore) putDoc(w denseWant, key string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO dense_docs(path, fingerprint, vec_key) VALUES(?, ?, ?)`, w.path, w.fingerprint, key)

	return err
}

func (s *denseStore) putVector(key string, vec []float32) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO dense_vectors(vec_key, vec) VALUES(?, ?)`, key, encodeVector(vec))

	return err
}

func (s *denseStore) putVectorAndDoc(w denseWant, key string, vec []float32) error {
	if err := s.putVector(key, vec); err != nil {
		return err
	}

	return s.putDoc(w, key)
}

// DenseStateKey summarizes the dense store (model key, completeness, size)
// for the pack cache key, so a cached pack never outlives the embeddings it
// was ranked with. Empty when there is no store.
func DenseStateKey(root string) string {
	path := denseStorePath(root)
	if _, err := os.Stat(path); err != nil {
		return ""
	}

	store, err := openDenseStore(path)
	if err != nil {
		return ""
	}
	defer store.Close()

	h := sha256.New()

	rows, err := store.db.Query(`SELECT path, vec_key FROM dense_docs ORDER BY path`)
	if err != nil {
		return ""
	}
	defer rows.Close()

	for rows.Next() {
		var p, k string
		if rows.Scan(&p, &k) == nil {
			fmt.Fprintf(h, "%s\x00%s\n", p, k)
		}
	}

	return "dense:" + store.meta(metaModel) + ":" + store.meta(metaComplete) + ":" + hex.EncodeToString(h.Sum(nil)[:8])
}
