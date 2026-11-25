package comprehension

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"
)

// The dense channel ranks files by meaning: every file's path and head are
// embedded once by a local embedding model, a task is embedded the same way,
// and files are ordered by cosine similarity. It joins the fusion beside BM25
// and the graph channel, and is skipped (with an Unknowns note, never an
// error) when no embedding model is reachable.

const (
	// DenseModelEnv names the ollama embedding model; DenseOffEnv=off disables
	// the channel. Only models already installed locally are ever used.
	DenseModelEnv = "IDIOM_DENSE_MODEL"
	DenseOffEnv   = "IDIOM_DENSE"

	defaultDenseModel = "qwen3-embedding:0.6b"
	denseHeadRunes    = 6000 // file head embedded with its path
	denseBatch        = 16
	denseProbeTimeout = 2 * time.Second
	denseEmbedTimeout = 10 * time.Minute
	denseQueryTimeout = 20 * time.Second

	// denseTextVersion changes whenever the embedded text's construction
	// does, so stored vectors from the old construction are discarded.
	denseTextVersion = "1"
)

// DenseModel identifies an embedding model exactly: the digest, not the
// tag, decides whether stored vectors are still valid.
type DenseModel struct {
	Name   string
	Digest string
}

func (m DenseModel) key() string { return m.Name + "@" + m.Digest + "#" + denseTextVersion }

// DenseEmbedder turns texts into vectors. Embed receives documents or, when
// query is true, a task description (models may use a different prefix).
type DenseEmbedder interface {
	Model(ctx context.Context) (DenseModel, error)
	Embed(ctx context.Context, texts []string, query bool) ([][]float32, error)
}

// denseEmbedderFactory returns the embedder in use, or nil when the channel
// is disabled. Tests replace it.
var denseEmbedderFactory = newOllamaEmbedder

var errDenseUnavailable = errors.New("dense embedding unavailable")

type ollamaEmbedder struct {
	base   string
	name   string
	prefix string // query prefix required by the model card
	client *http.Client
	model  *DenseModel
}

func newOllamaEmbedder() DenseEmbedder {
	if strings.EqualFold(os.Getenv(DenseOffEnv), "off") {
		return nil
	}

	name := strings.TrimSpace(os.Getenv(DenseModelEnv))
	if name == "" {
		name = defaultDenseModel
	}

	host := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if host == "" {
		host = "127.0.0.1:11434"
	}

	if !strings.Contains(host, "://") {
		host = "http://" + host
	}

	return &ollamaEmbedder{base: strings.TrimRight(host, "/"), name: name, prefix: denseQueryPrefix(name), client: &http.Client{}}
}

// denseQueryPrefix is the instruction each model card requires before a
// query (documents take none).
func denseQueryPrefix(model string) string {
	switch {
	case strings.HasPrefix(model, "qwen3-embedding"):
		return "Instruct: Given a software change request, retrieve the source files that must be edited to implement it\nQuery: "
	case strings.Contains(model, "coderankembed"):
		return "Represent this query for searching relevant code: "
	}

	return ""
}

func (o *ollamaEmbedder) Model(ctx context.Context) (DenseModel, error) {
	if o.model != nil {
		return *o.model, nil
	}

	ctx, cancel := context.WithTimeout(ctx, denseProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/api/tags", nil)
	if err != nil {
		return DenseModel{}, err
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return DenseModel{}, fmt.Errorf("%w: ollama unreachable at %s", errDenseUnavailable, o.base)
	}
	defer resp.Body.Close()

	var tags struct {
		Models []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return DenseModel{}, fmt.Errorf("%w: ollama tags unreadable", errDenseUnavailable)
	}

	for _, m := range tags.Models {
		if m.Name == o.name || m.Name == o.name+":latest" {
			o.model = &DenseModel{Name: o.name, Digest: m.Digest}

			return *o.model, nil
		}
	}

	return DenseModel{}, fmt.Errorf("%w: model %s is not installed in ollama", errDenseUnavailable, o.name)
}

func (o *ollamaEmbedder) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	input := texts

	if query && o.prefix != "" {
		input = make([]string, len(texts))
		for i, t := range texts {
			input[i] = o.prefix + t
		}
	}

	body, err := json.Marshal(map[string]any{"model": o.name, "input": input, "truncate": true, "options": map[string]any{"num_ctx": 2048}})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errDenseUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 200))

		return nil, fmt.Errorf("%w: ollama embed status %d: %s", errDenseUnavailable, resp.StatusCode, msg)
	}

	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}

	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("%w: ollama returned %d vectors for %d texts", errDenseUnavailable, len(out.Embeddings), len(texts))
	}

	for _, v := range out.Embeddings {
		normalizeVector(v)
	}

	return out.Embeddings, nil
}

func normalizeVector(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}

	if sum == 0 {
		return
	}

	inv := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= inv
	}
}
