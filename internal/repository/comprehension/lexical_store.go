package comprehension

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The lexical index lives beside the graph in graph.sqlite as two derived
// tables: one row per indexed file, and one posting per (term, file). It is
// rebuilt from the file nodes after every graph write by diffing fingerprints,
// so an incremental refresh and a cold build store the same rows and an
// unchanged file is never re-read. Corpus statistics (document count, mean
// length, document frequency) are derived at query time and never stored, so
// there is nothing global to go stale.

const (
	lexicalSQLBatchTerms = 200 // SQLite variable limit headroom for IN lists
)

func createLexicalSchema(tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS lexical_docs (
			doc_id INTEGER PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			fingerprint TEXT NOT NULL,
			length INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS lexical_postings (
			term TEXT NOT NULL,
			doc_id INTEGER NOT NULL,
			tf INTEGER NOT NULL,
			PRIMARY KEY (term, doc_id)
		) WITHOUT ROWID`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}

	return nil
}

type lexicalFile struct {
	path        string
	fingerprint string
}

// syncLexicalIndex makes the lexical tables describe exactly the file nodes
// now in graph_nodes. Files whose fingerprint is unchanged keep their rows.
func syncLexicalIndex(tx *sql.Tx, root string) error {
	want, err := lexicalFilesFromGraph(tx)
	if err != nil {
		return err
	}

	have, err := lexicalDocFingerprints(tx)
	if err != nil {
		return err
	}

	if err := deleteStaleLexicalDocs(tx, want, have); err != nil {
		return err
	}

	docStmt, err := tx.Prepare(`INSERT INTO lexical_docs(path, fingerprint, length) VALUES(?, ?, ?)`)
	if err != nil {
		return err
	}
	defer docStmt.Close()

	postingStmt, err := tx.Prepare(`INSERT INTO lexical_postings(term, doc_id, tf) VALUES(?, ?, ?)`)
	if err != nil {
		return err
	}
	defer postingStmt.Close()

	for _, file := range want {
		if fingerprint, ok := have[file.path]; ok && fingerprint == file.fingerprint && file.fingerprint != "" {
			continue
		}

		if err := insertLexicalDoc(docStmt, postingStmt, root, file); err != nil {
			return err
		}
	}

	return nil
}

func lexicalFilesFromGraph(tx *sql.Tx) ([]lexicalFile, error) {
	rows, err := tx.Query(`SELECT path, COALESCE(MAX(fingerprint), '') FROM graph_nodes
		WHERE kind = 'file' AND path IS NOT NULL AND path <> '' GROUP BY path ORDER BY path`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []lexicalFile

	for rows.Next() {
		var file lexicalFile
		if err := rows.Scan(&file.path, &file.fingerprint); err != nil {
			return nil, err
		}

		files = append(files, file)
	}

	return files, rows.Err()
}

func lexicalDocFingerprints(tx *sql.Tx) (map[string]string, error) {
	rows, err := tx.Query(`SELECT path, fingerprint FROM lexical_docs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	have := map[string]string{}

	for rows.Next() {
		var path, fingerprint string
		if err := rows.Scan(&path, &fingerprint); err != nil {
			return nil, err
		}

		have[path] = fingerprint
	}

	return have, rows.Err()
}

// deleteStaleLexicalDocs drops documents whose file is gone or whose content
// changed (the changed ones are re-inserted by the caller).
func deleteStaleLexicalDocs(tx *sql.Tx, want []lexicalFile, have map[string]string) error {
	wantFingerprint := make(map[string]string, len(want))
	for _, file := range want {
		wantFingerprint[file.path] = file.fingerprint
	}

	for path, fingerprint := range have {
		if current, ok := wantFingerprint[path]; ok && current == fingerprint && current != "" {
			continue
		}

		if _, err := tx.Exec(`DELETE FROM lexical_postings WHERE doc_id = (SELECT doc_id FROM lexical_docs WHERE path = ?)`, path); err != nil {
			return err
		}

		if _, err := tx.Exec(`DELETE FROM lexical_docs WHERE path = ?`, path); err != nil {
			return err
		}
	}

	return nil
}

func insertLexicalDoc(docStmt, postingStmt *sql.Stmt, root string, file lexicalFile) error {
	text, err := readLexicalText(root, file.path)
	if err != nil {
		return err
	}

	doc := NewLexicalDocument(file.path, text)

	result, err := docStmt.Exec(file.path, file.fingerprint, doc.Length)
	if err != nil {
		return err
	}

	docID, err := result.LastInsertId()
	if err != nil {
		return err
	}

	for term, tf := range doc.Terms {
		if _, err := postingStmt.Exec(term, docID, tf); err != nil {
			return err
		}
	}

	return nil
}

// readLexicalText reads a file's text for indexing. A file over
// LexicalMaxFileBytes or missing from disk indexes by path only, and the
// oversize file is never read.
func readLexicalText(root, path string) ([]byte, error) {
	full := filepath.Join(root, filepath.FromSlash(path))

	info, err := os.Stat(full)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.Size() > LexicalMaxFileBytes) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	text, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	return text, err
}

// LexicalPosting is one file's frequency for one query term.
type LexicalPosting struct {
	Path   string
	Length int
	TF     int
}

// LexicalCorpus is what BM25 needs from the index for one query: corpus
// size, total length, and the postings of each query term.
type LexicalCorpus struct {
	Docs     int
	TotalLen int
	Postings map[string][]LexicalPosting
	// Lengths is every indexed file's token length, for per-class statistics.
	Lengths map[string]int
}

// ErrLexicalIndexMissing reports an index built before the lexical tables
// existed or an empty one; the caller falls back to graph-only candidates.
var ErrLexicalIndexMissing = errors.New("lexical index missing")

// LoadLexicalCorpus reads the postings of terms from the graph store. The
// corpus statistics and the postings come from one read transaction, so a
// concurrent index write cannot make them disagree.
func LoadLexicalCorpus(root string, terms []string) (LexicalCorpus, error) {
	db, err := openSQLite(sqliteGraphPath(root))
	if err != nil {
		return LexicalCorpus{}, err
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return LexicalCorpus{}, err
	}

	defer func() { _ = tx.Rollback() }()

	corpus := LexicalCorpus{Postings: make(map[string][]LexicalPosting, len(terms))}

	if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length), 0) FROM lexical_docs`).Scan(&corpus.Docs, &corpus.TotalLen); err != nil {
		if strings.Contains(err.Error(), "no such table") {
			return LexicalCorpus{}, ErrLexicalIndexMissing
		}

		return LexicalCorpus{}, err
	}

	if corpus.Docs == 0 {
		return LexicalCorpus{}, ErrLexicalIndexMissing
	}

	lengths, err := loadLexicalLengths(tx)
	if err != nil {
		return LexicalCorpus{}, err
	}

	corpus.Lengths = lengths

	for start := 0; start < len(terms); start += lexicalSQLBatchTerms {
		batch := terms[start:min(start+lexicalSQLBatchTerms, len(terms))]
		if err := loadLexicalPostings(tx, batch, corpus.Postings); err != nil {
			return LexicalCorpus{}, err
		}
	}

	return corpus, nil
}

func loadLexicalLengths(tx *sql.Tx) (map[string]int, error) {
	rows, err := tx.Query(`SELECT path, length FROM lexical_docs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	lengths := map[string]int{}

	for rows.Next() {
		var path string

		var length int
		if err := rows.Scan(&path, &length); err != nil {
			return nil, err
		}

		lengths[path] = length
	}

	return lengths, rows.Err()
}

func loadLexicalPostings(q *sql.Tx, terms []string, into map[string][]LexicalPosting) error {
	if len(terms) == 0 {
		return nil
	}

	args := make([]any, len(terms))
	for i, term := range terms {
		args[i] = term
	}

	query := fmt.Sprintf(`SELECT p.term, d.path, d.length, p.tf FROM lexical_postings p
		JOIN lexical_docs d ON d.doc_id = p.doc_id WHERE p.term IN (%s) ORDER BY p.term, d.path`,
		strings.TrimSuffix(strings.Repeat("?,", len(terms)), ","))

	rows, err := q.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			term string
			post LexicalPosting
		)

		if err := rows.Scan(&term, &post.Path, &post.Length, &post.TF); err != nil {
			return err
		}

		into[term] = append(into[term], post)
	}

	return rows.Err()
}
