package llmrerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

const (
	rrfK                = 60 // Cormack et al. 2009: reciprocal rank fusion constant
	bytesPerTokenEst    = 3.5
	maxResponseBytes    = 1 << 20
	maxErrorBodyBytes   = 200
	degenerateScoreSpan = 1e-9
)

// Ranked is one backend's answer: a full permutation of the candidates, best
// first, and the tokens the call used (zero when the server reports none).
type Ranked struct {
	Order     []int
	InTokens  int
	OutTokens int
}

// Backend scores candidates for a query. Estimate predicts the tokens of one
// call without making it (the dry run).
type Backend interface {
	Name() string
	Rank(ctx context.Context, query string, docs []Doc) (Ranked, error)
	Estimate(query string, docs []Doc) (in, out int)
}

var httpClient = &http.Client{}

// tailnetRange is the Tailscale CGNAT block.
var tailnetRange = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}

	return n
}

// orderByScore sorts candidate indexes by descending score; ties keep
// Idiom's order. Every index must have a score.
func orderByScore(scores []float64) ([]int, error) {
	if len(scores) < 2 {
		return nil, fmt.Errorf("%d scores", len(scores))
	}

	lo, hi := math.Inf(1), math.Inf(-1)

	for _, s := range scores {
		if math.IsNaN(s) || math.IsInf(s, 0) {
			return nil, fmt.Errorf("non-finite or missing score")
		}

		lo, hi = math.Min(lo, s), math.Max(hi, s)
	}

	if hi-lo < degenerateScoreSpan {
		return nil, fmt.Errorf("degenerate scores (all equal): the model may lack a rank head")
	}

	order := make([]int, len(scores))
	for i := range order {
		order[i] = i
	}

	sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] > scores[order[b]] })

	return order, nil
}

// completeOrder keeps the valid, first-seen indexes of order and appends the
// candidates it omitted in Idiom's order. It returns false when order holds
// no valid index at all.
func completeOrder(order []int, n int) ([]int, bool) {
	seen := make([]bool, n)
	out := make([]int, 0, n)

	for _, i := range order {
		if i >= 0 && i < n && !seen[i] {
			seen[i] = true
			out = append(out, i)
		}
	}

	if len(out) == 0 {
		return nil, false
	}

	for i := 0; i < n; i++ {
		if !seen[i] {
			out = append(out, i)
		}
	}

	return out, true
}

// fuseRRF merges several permutations by reciprocal rank fusion.
func fuseRRF(orders [][]int, n int) []int {
	score := make([]float64, n)

	for _, order := range orders {
		for rank, i := range order {
			score[i] += 1 / float64(rrfK+rank+1)
		}
	}

	out := make([]int, n)
	for i := range out {
		out[i] = i
	}

	sort.SliceStable(out, func(a, b int) bool { return score[out[a]] > score[out[b]] })

	return out
}

func estimateTokens(bytesLen int) int {
	return int(math.Ceil(float64(bytesLen) / bytesPerTokenEst))
}

// checkPrivateHost refuses a host that is neither loopback nor on the tailnet
// (100.64.0.0/10) for the local backends: nothing
// leaves the user's own machines unless IDIOM_RERANK_ALLOW_REMOTE=1.
func checkPrivateHost(raw string, allowRemote bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("bad url")
	}

	if allowRemote {
		return nil
	}

	host := u.Hostname()
	if host == "localhost" {
		return nil
	}

	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() || tailnetRange.Contains(ip) {
		return nil
	}

	return fmt.Errorf("host %q is not loopback or tailnet (set IDIOM_RERANK_ALLOW_REMOTE=1 to allow)", host)
}

// postJSON posts body and decodes the JSON reply into out. Error text never
// carries request headers or the URL.
func postJSON(ctx context.Context, endpoint string, headers map[string]string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("bad request")
	}

	req.Header.Set("Content-Type", "application/json")

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("call failed: %w", scrubURLError(err))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read reply: %w", err)
	}

	if resp.StatusCode/100 != 2 {
		text := strings.TrimSpace(string(data))
		if len(text) > maxErrorBodyBytes {
			text = text[:maxErrorBodyBytes]
		}

		return fmt.Errorf("http %d: %s", resp.StatusCode, text)
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("bad reply: %w", err)
	}

	return nil
}

// scrubURLError drops the URL net/http puts in its errors.
func scrubURLError(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}

	return err
}
