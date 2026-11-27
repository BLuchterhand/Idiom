package llmrerank

import (
	"context"
	"fmt"
	"math"
	"strings"
)

const (
	crossPromptOverheadTokens = 60 // the reranker's instruction template, per pair
	// queryInstruction is Qwen3-Embedding's documented query prefix; documents
	// are embedded bare.
	queryInstruction = "Instruct: Given a code search query, retrieve the repository files most relevant to it\nQuery: "
)

type tokenUsage struct {
	Prompt int `json:"prompt_tokens"`
}

// crossBackend scores query to file pairs with a cross-encoder served by a
// llama.cpp server started with --rerank (POST /v1/rerank).
type crossBackend struct{ url, model string }

func (b crossBackend) Name() string { return BackendCross }

func (b crossBackend) Rank(ctx context.Context, query string, docs []Doc) (Ranked, error) {
	texts := make([]string, len(docs))
	for i, d := range docs {
		texts[i] = d.Text
	}

	var reply struct {
		Results []struct {
			Index int      `json:"index"`
			Score *float64 `json:"relevance_score"`
		} `json:"results"`
		Usage tokenUsage `json:"usage"`
	}

	err := postJSON(ctx, strings.TrimRight(b.url, "/")+"/v1/rerank", nil,
		map[string]any{"model": b.model, "query": query, "documents": texts}, &reply)
	if err != nil {
		return Ranked{}, err
	}

	scores := make([]float64, len(docs))
	for i := range scores {
		scores[i] = math.NaN()
	}

	for _, r := range reply.Results {
		if r.Index < 0 || r.Index >= len(docs) || r.Score == nil {
			return Ranked{}, fmt.Errorf("bad result index %d", r.Index)
		}

		scores[r.Index] = *r.Score
	}

	order, err := orderByScore(scores)
	if err != nil {
		return Ranked{}, err
	}

	return Ranked{Order: order, InTokens: reply.Usage.Prompt}, nil
}

func (b crossBackend) Estimate(query string, docs []Doc) (int, int) {
	in := 0
	for _, d := range docs {
		in += estimateTokens(len(query)+len(d.Text)) + crossPromptOverheadTokens
	}

	return in, 0
}

// embedBackend ranks by cosine similarity between the query and each file
// text, from a llama.cpp server started with --embeddings (POST /v1/embeddings).
type embedBackend struct{ url, model string }

func (b embedBackend) Name() string { return BackendEmbed }

func (b embedBackend) Rank(ctx context.Context, query string, docs []Doc) (Ranked, error) {
	input := make([]string, 0, len(docs)+1)
	input = append(input, queryInstruction+query)

	for _, d := range docs {
		input = append(input, d.Text)
	}

	var reply struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
		Usage tokenUsage `json:"usage"`
	}

	err := postJSON(ctx, strings.TrimRight(b.url, "/")+"/v1/embeddings", nil,
		map[string]any{"model": b.model, "input": input}, &reply)
	if err != nil {
		return Ranked{}, err
	}

	vectors := make([][]float64, len(input))

	for _, d := range reply.Data {
		if d.Index < 0 || d.Index >= len(input) || len(d.Embedding) == 0 {
			return Ranked{}, fmt.Errorf("bad embedding index %d", d.Index)
		}

		vectors[d.Index] = d.Embedding
	}

	scores := make([]float64, len(docs))

	for i := range docs {
		if vectors[0] == nil || vectors[i+1] == nil || len(vectors[0]) != len(vectors[i+1]) {
			return Ranked{}, fmt.Errorf("missing or mismatched embedding")
		}

		scores[i] = cosine(vectors[0], vectors[i+1])
	}

	order, err := orderByScore(scores)
	if err != nil {
		return Ranked{}, err
	}

	return Ranked{Order: order, InTokens: reply.Usage.Prompt}, nil
}

func (b embedBackend) Estimate(query string, docs []Doc) (int, int) {
	bytesLen := len(queryInstruction) + len(query)
	for _, d := range docs {
		bytesLen += len(d.Text)
	}

	return estimateTokens(bytesLen), 0
}

func cosine(a, b []float64) float64 {
	var dot, na, nb float64

	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}

	if na == 0 || nb == 0 {
		return 0
	}

	return dot / math.Sqrt(na*nb)
}
