package eval

// Localization baselines: lexical and graph rankers scored on the same cases,
// gold and metrics as the idiom runs, so a ranker change can be judged
// against what plain BM25 already gets. Each case reads the base tree from
// git, the file universe and file-to-file graph from the case's cached idiom
// index (the same universe idiom ranks), and optionally a code map generated
// at that base.

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"idiom/internal/repository/comprehension"

	_ "modernc.org/sqlite"
)

// bm25Field holds, for one document field, the query-term frequencies and
// lengths of every document. Only query terms are counted: the ranker never
// needs the rest of the vocabulary.
type bm25Field struct {
	tf     []map[string]int
	length []int
}

func newBM25Field(n int) *bm25Field {
	return &bm25Field{tf: make([]map[string]int, n), length: make([]int, n)}
}

func (f *bm25Field) add(doc int, tokens []string, query map[string]struct{}) {
	f.length[doc] += len(tokens)

	for _, t := range tokens {
		if _, ok := query[t]; !ok {
			continue
		}

		if f.tf[doc] == nil {
			f.tf[doc] = map[string]int{}
		}

		f.tf[doc][t]++
	}
}

// scores returns Okapi BM25 per document (Robertson and Zaragoza 2009,
// idf with the +1 floor so no term scores negative).
func (f *bm25Field) scores(query []string) []float64 {
	n := len(f.length)
	out := make([]float64, n)

	if n == 0 {
		return out
	}

	total := 0
	for _, l := range f.length {
		total += l
	}

	avg := comprehension.BM25AvgLength(total, n)

	for _, term := range comprehension.UniqueLexicalTerms(query) {
		df := f.docFreq(term)
		if df == 0 {
			continue
		}

		idf := comprehension.BM25IDF(n, df)

		for d, tf := range f.tf {
			if c := tf[term]; c > 0 {
				out[d] += idf * comprehension.BM25TermScore(c, f.length[d], avg)
			}
		}
	}

	return out
}

func (f *bm25Field) docFreq(term string) int {
	df := 0

	for _, tf := range f.tf {
		if tf[term] > 0 {
			df++
		}
	}

	return df
}

// baselineCase is everything the rankers need for one case.
type baselineCase struct {
	c       LocalizationEvalCase
	files   []string
	index   map[string]int
	query   []string
	content *bm25Field // file text plus path
	path    *bm25Field // path only
	docs    *bm25Field // code-map entry (nil lengths when the map lacks the file)
	hasDocs bool
	isTest  []bool
	adj     []map[int]float64 // file graph, undirected, edge counts
	pairs   []map[int]float64 // header/implementation pairs only
	imports []map[int]float64 // import/include edges plus pairs (no call edges)
}

func loadBaselineCase(repo, indexDir, codeMap string, c LocalizationEvalCase, cfg LocalizationEvalBuilderConfig) (*baselineCase, error) {
	db := filepath.Join(indexDir, "graph.sqlite")

	files, edges, err := readIndexGraph(db, cfg.SourceRegex)
	if err != nil {
		return nil, err
	}

	bc := &baselineCase{c: c, files: files, index: map[string]int{}, query: comprehension.LexicalTokens(c.Query)}
	for i, f := range files {
		bc.index[f] = i
	}

	querySet := map[string]struct{}{}
	for _, t := range bc.query {
		querySet[t] = struct{}{}
	}

	bc.content, bc.path, bc.docs = newBM25Field(len(files)), newBM25Field(len(files)), newBM25Field(len(files))

	testRx, _ := regexp.Compile(orNever(cfg.TestRegex))
	bc.isTest = make([]bool, len(files))

	for i, f := range files {
		pt := comprehension.LexicalTokens(f)
		bc.path.add(i, pt, querySet)
		bc.content.add(i, pt, querySet)
		bc.isTest[i] = testRx.MatchString(f)
	}

	if err := readBlobs(repo, c.Base, files, func(i int, data []byte) {
		bc.content.add(i, comprehension.LexicalTokens(string(data)), querySet)
	}); err != nil {
		return nil, err
	}

	if codeMap != "" {
		if entries, err := parseCodeMap(codeMap); err == nil {
			bc.hasDocs = true

			for f, text := range entries {
				if i, ok := bc.index[f]; ok {
					bc.docs.add(i, comprehension.LexicalTokens(f+" "+text), querySet)
				}
			}
		}
	}

	bc.adj = make([]map[int]float64, len(files))
	link := func(a, b int, w float64) {
		if a == b {
			return
		}

		for _, p := range [][2]int{{a, b}, {b, a}} {
			if bc.adj[p[0]] == nil {
				bc.adj[p[0]] = map[int]float64{}
			}

			bc.adj[p[0]][p[1]] += w
		}
	}

	bc.imports = make([]map[int]float64, len(files))
	linkInto := func(adj []map[int]float64, a, b int) {
		for _, p := range [][2]int{{a, b}, {b, a}} {
			if adj[p[0]] == nil {
				adj[p[0]] = map[int]float64{}
			}

			adj[p[0]][p[1]]++
		}
	}

	for _, e := range edges {
		a, okA := bc.index[e[0]]
		b, okB := bc.index[e[1]]

		if okA && okB {
			link(a, b, 1)

			if e[2] == "imports" && a != b {
				linkInto(bc.imports, a, b)
			}
		}
	}

	linkHeaderPairs(files, func(a, b int, _ float64) { linkInto(bc.imports, a, b) })

	linkHeaderPairs(files, link)

	bc.pairs = make([]map[int]float64, len(files))
	linkHeaderPairs(files, func(a, b int, w float64) {
		for _, p := range [][2]int{{a, b}, {b, a}} {
			if bc.pairs[p[0]] == nil {
				bc.pairs[p[0]] = map[int]float64{}
			}

			bc.pairs[p[0]][p[1]] += w
		}
	})

	return bc, nil
}

func orNever(rx string) string {
	if rx == "" {
		return `$^`
	}

	return rx
}

// linkHeaderPairs joins files in one directory that share a stem
// (foo.h and foo.cpp): the interface and its implementation change together.
func linkHeaderPairs(files []string, link func(a, b int, w float64)) {
	byStem := map[string][]int{}

	for i, f := range files {
		stem := strings.TrimSuffix(f, path.Ext(f))
		byStem[stem] = append(byStem[stem], i)
	}

	for _, group := range byStem {
		for x := range group {
			for y := x + 1; y < len(group); y++ {
				link(group[x], group[y], 1)
			}
		}
	}
}

// readIndexGraph returns the indexed files matching sourceRegex and every
// cross-file edge (imports, calls, tests, extends) between indexed paths,
// as source, target and kind.
func readIndexGraph(dbPath, sourceRegex string) ([]string, [][3]string, error) {
	rx, err := regexp.Compile(sourceRegex)
	if err != nil {
		return nil, nil, err
	}

	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()

	rows, err := db.Query(`SELECT DISTINCT path FROM graph_nodes WHERE kind = 'file' AND path IS NOT NULL ORDER BY path`)
	if err != nil {
		return nil, nil, err
	}

	var files []string

	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, nil, err
		}

		if rx.MatchString(p) {
			files = append(files, p)
		}
	}

	rows.Close()

	rows, err = db.Query(`SELECT s.path, t.path, e.kind FROM graph_edges e
		JOIN graph_nodes s ON s.id = e.source_id JOIN graph_nodes t ON t.id = e.target_id
		WHERE e.kind IN ('imports','calls','tests','extends') AND s.path IS NOT NULL AND t.path IS NOT NULL AND s.path <> t.path`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var edges [][3]string

	for rows.Next() {
		var a, b, kind string
		if err := rows.Scan(&a, &b, &kind); err != nil {
			return nil, nil, err
		}

		edges = append(edges, [3]string{a, b, kind})
	}

	return files, edges, rows.Err()
}

// readBlobs streams base:file for every file through one git cat-file.
func readBlobs(repo, base string, files []string, fn func(int, []byte)) error {
	cmd := exec.Command("git", "-C", repo, "cat-file", "--batch")

	var input bytes.Buffer
	for _, f := range files {
		fmt.Fprintf(&input, "%s:%s\n", base, f)
	}

	cmd.Stdin = &input

	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	r := bufio.NewReaderSize(out, 1<<20)

	for i := range files {
		header, err := r.ReadString('\n')
		if err != nil {
			return err
		}

		fields := strings.Fields(header)
		if len(fields) < 3 || fields[1] != "blob" {
			continue // missing at base
		}

		size, _ := strconv.Atoi(fields[2])
		data := make([]byte, size+1)

		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}

		if size <= comprehension.LexicalMaxFileBytes {
			fn(i, data[:size])
		}
	}

	return cmd.Wait()
}

// parseCodeMap reads a BenTech code map: `## <dir>` sections of
// `- <name> (<lines>) <purpose> | <declarations>` lines.
func parseCodeMap(file string) (map[string]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	entries := map[string]string{}
	dir := ""
	lineRx := regexp.MustCompile(`^- (\S+) \(\d+\)\s*(.*)$`)

	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "## ") {
			dir = strings.TrimSpace(line[3:])
			continue
		}

		if m := lineRx.FindStringSubmatch(line); m != nil && dir != "" {
			entries[dir+"/"+m[1]] = m[2]
		}
	}

	return entries, nil
}

func maxScore(v []float64) float64 {
	m := 0.0
	for _, x := range v {
		m = math.Max(m, x)
	}

	return m
}

// rankScores returns file indices with a positive score, best first, ties by path.
func (bc *baselineCase) rankScores(s []float64) []string {
	idx := make([]int, 0, len(s))

	for i, v := range s {
		if v > 0 {
			idx = append(idx, i)
		}
	}

	slices.SortFunc(idx, func(a, b int) int {
		if s[a] != s[b] {
			if s[a] > s[b] {
				return -1
			}

			return 1
		}

		return strings.Compare(bc.files[a], bc.files[b])
	})

	out := make([]string, len(idx))
	for i, j := range idx {
		out[i] = bc.files[j]
	}

	return out
}
