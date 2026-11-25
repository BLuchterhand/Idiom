package comprehension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// denseOutcome is what the dense channel produced for one query: the ranked
// files, or why it did not run. Note goes to the pack's Unknowns; Transient
// marks a failure that may not repeat (server down), which keeps the pack
// out of the view cache.
type denseOutcome struct {
	Hits      []LexicalHit
	Note      string
	Transient bool
}

// denseHits ranks files by cosine similarity to the task. admit says which
// files may appear (scope, and languages whose policy enables the channel).
// It never fails the query: every problem becomes a note.
func denseHits(ctx context.Context, root, task string, admit func(string) bool) denseOutcome {
	embedder := denseEmbedderFactory()
	if embedder == nil {
		return denseOutcome{}
	}

	if _, err := os.Stat(denseStorePath(root)); err != nil {
		return denseOutcome{Note: "dense channel skipped: no embedding index (run context_index with the ollama embedding model available)"}
	}

	model, err := embedder.Model(ctx)
	if err != nil {
		return denseOutcome{Note: "dense channel skipped: " + err.Error(), Transient: true}
	}

	store, err := openDenseStore(denseStorePath(root))
	if err != nil {
		return denseOutcome{Note: "dense channel skipped: embedding index unreadable: " + err.Error()}
	}
	defer store.Close()

	if stored := store.meta(metaModel); stored != model.key() {
		return denseOutcome{Note: fmt.Sprintf("dense channel skipped: embedding index was built with %q, current model is %q; re-run context_index", stored, model.key())}
	}

	query, err := store.queryVector(ctx, embedder, model, task)
	if err != nil {
		return denseOutcome{Note: "dense channel skipped: " + err.Error(), Transient: true}
	}

	hits, err := store.rank(query, admit)
	if err != nil {
		return denseOutcome{Note: "dense channel skipped: " + err.Error()}
	}

	out := denseOutcome{Hits: hits}
	if store.meta(metaComplete) != "1" {
		out.Note = "dense index is incomplete (an embedding run did not finish); semantic ranking covers only the files embedded so far"
	}

	return out
}

func (s *denseStore) queryVector(ctx context.Context, embedder DenseEmbedder, model DenseModel, task string) ([]float32, error) {
	sum := sha256.Sum256([]byte(model.key() + "\x00" + task))
	key := hex.EncodeToString(sum[:])

	var blob []byte
	if err := s.db.QueryRow(`SELECT vec FROM dense_queries WHERE qkey = ?`, key).Scan(&blob); err == nil {
		return decodeVector(blob), nil
	}

	ctx, cancel := context.WithTimeout(ctx, denseQueryTimeout)
	defer cancel()

	vecs, err := embedder.Embed(ctx, []string{task}, true)
	if err != nil {
		return nil, err
	}

	if len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil, errors.New("embedding model returned no vector")
	}

	_, _ = s.db.Exec(`INSERT OR REPLACE INTO dense_queries(qkey, seq, vec)
		VALUES(?, COALESCE((SELECT MAX(seq) FROM dense_queries), 0) + 1, ?)`, key, encodeVector(vecs[0]))
	_, _ = s.db.Exec(`DELETE FROM dense_queries WHERE seq <= (SELECT MAX(seq) FROM dense_queries) - ?`, denseQueryCacheRows)

	return vecs[0], nil
}

// rank scores every admitted file by dot product (vectors are unit length)
// and returns the best fusionDepth, ties by path.
func (s *denseStore) rank(query []float32, admit func(string) bool) ([]LexicalHit, error) {
	rows, err := s.db.Query(`SELECT d.path, v.vec FROM dense_docs d JOIN dense_vectors v ON v.vec_key = d.vec_key ORDER BY d.path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []LexicalHit

	for rows.Next() {
		var (
			path string
			blob []byte
		)

		if err := rows.Scan(&path, &blob); err != nil {
			return nil, err
		}

		if admit != nil && !admit(path) {
			continue
		}

		if len(blob) != 4*len(query) {
			return nil, fmt.Errorf("stored vector for %s has dimension %d, query has %d", path, len(blob)/4, len(query))
		}

		hits = append(hits, LexicalHit{Path: path, Score: dotBlob(query, blob)})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	slices.SortFunc(hits, func(a, b LexicalHit) int {
		switch {
		case a.Score > b.Score:
			return -1
		case a.Score < b.Score:
			return 1
		default:
			return strings.Compare(a.Path, b.Path)
		}
	})

	return hits[:min(fusionDepth, len(hits))], rows.Err()
}

func dotBlob(q []float32, blob []byte) float64 {
	var sum float64

	for i, x := range q {
		sum += float64(x) * float64(float32frombytes(blob[4*i:]))
	}

	return sum
}
