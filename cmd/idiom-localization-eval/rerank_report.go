package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// rerankEnv is the environment that switches the rerank stage on for the idiom
// subprocess of a run: the arm's backends, its cache and its usage log.
func rerankEnv(backends, dir string, dry bool) ([]string, error) {
	if backends == "" {
		return nil, nil
	}

	if dir == "" {
		return nil, fmt.Errorf("-rerank needs -rerank-dir (cache and usage log)")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	mode := "on"
	if dry {
		mode = "dry"
	}

	return []string{
		"IDIOM_LLM_RERANK=" + mode,
		"IDIOM_RERANK_BACKENDS=" + backends,
		"IDIOM_RERANK_CACHE_DIR=" + filepath.Join(dir, "cache"),
		"IDIOM_RERANK_LOG=" + filepath.Join(dir, "usage.jsonl"),
	}, nil
}

type rerankLogRow struct {
	Backends  string `json:"backends"`
	Status    string `json:"status"`
	Cached    bool   `json:"cached"`
	N         int    `json:"n"`
	LatencyMS int64  `json:"latency_ms"`
	InTokens  int    `json:"in_tokens"`
	OutTokens int    `json:"out_tokens"`
	EstIn     int    `json:"est_in_tokens"`
	EstOut    int    `json:"est_out_tokens"`
}

// rerankReport summarises a usage log: calls, fallbacks, latency of the real
// calls (the first is the cold model, the rest warm) and tokens and cost.
func rerankReport(args []string) error {
	fs := flag.NewFlagSet("rerank-report", flag.ExitOnError)
	logPath := fs.String("log", "", "usage.jsonl written by a rerank run")
	inUSD := fs.Float64("in-usd-per-mtok", 0, "input price, USD per million tokens (0: no cost line)")
	outUSD := fs.Float64("out-usd-per-mtok", 0, "output price, USD per million tokens")
	_ = fs.Parse(args)

	if *logPath == "" {
		return fmt.Errorf("-log is required")
	}

	rows, err := readRerankLog(*logPath)
	if err != nil {
		return err
	}

	var real, fallbacks, dry, cached int

	var lat []int64

	var in, out, estIn, estOut int

	for _, r := range rows {
		switch {
		case r.Status == "dry":
			dry++
			estIn += r.EstIn
			estOut += r.EstOut
		case r.Cached:
			cached++
		case strings.HasPrefix(r.Status, "off"):
			fallbacks++
		default:
			real++
			lat = append(lat, r.LatencyMS)
			in += r.InTokens
			out += r.OutTokens
		}
	}

	fmt.Printf("rows %d: real calls %d, cache hits %d, fallbacks %d, dry %d\n", len(rows), real, cached, fallbacks, dry)

	if len(lat) > 0 {
		warm := slices.Clone(lat[1:])
		fmt.Printf("rerank stage latency ms: cold (first call) %d; warm p50 %d p95 %d (n=%d); all p50 %d p95 %d\n",
			lat[0], percentile(warm, 50), percentile(warm, 95), len(warm), percentile(lat, 50), percentile(lat, 95))
	}

	if real > 0 {
		fmt.Printf("tokens (reported): in %d out %d; per call in %d out %d\n", in, out, in/real, out/real)
	}

	if dry > 0 {
		fmt.Printf("tokens (estimated, bytes/3.5): in %d out %d; per call in %d out %d\n", estIn, estOut, estIn/dry, estOut/dry)
		in, out, real = estIn, estOut, dry
	}

	if *inUSD > 0 && real > 0 {
		usd := float64(in)/1e6**inUSD + float64(out)/1e6**outUSD
		fmt.Printf("cost: total $%.4f, per query $%.6f\n", usd, usd/float64(real))
	}

	return nil
}

func readRerankLog(path string) ([]rerankLogRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []rerankLogRow

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r rerankLogRow
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			rows = append(rows, r)
		}
	}

	return rows, sc.Err()
}

// percentile is the nearest-rank percentile of v (v is sorted in place).
func percentile(v []int64, p int) int64 {
	if len(v) == 0 {
		return 0
	}

	slices.Sort(v)

	rank := (p*len(v) + 99) / 100
	if rank < 1 {
		rank = 1
	}

	return v[rank-1]
}
