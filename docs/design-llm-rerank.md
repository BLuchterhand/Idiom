# Optional rerank stage for context_impact (G885)

Status: built, off by default. Package `internal/capabilities/workspace/llmrerank`, hook in
`internal/capabilities/workspace/context_rerank.go`.

## Goal

Idiom ranks files for a query. A second stage may reorder the top 30 with a model that reads the query and a
short text per file. It ships only if it scores no worse than the frozen baseline (e324acb1) on the usage-eval v1
hold-out (`testdata/localization/eval/AUDIT.md`, "usage-eval v1"), and adds under 1 s per query at p50.

## Design

- **Where.** `contextImpact` asks `Pack` for `max(limit, 30)` files, reorders the first 30 candidate files, then
  truncates to the caller's limit (default 8). With the stage off, `Pack` gets the caller's limit and nothing else
  changes.
- **Scorers (`Backend`).** One interface, four implementations, chosen by `IDIOM_RERANK_BACKENDS`:
  `cross` (llama.cpp `/v1/rerank`, Qwen3-Reranker-0.6B), `embed` (`/v1/embeddings`, Qwen3-Embedding-0.6B, cosine),
  `chat` (OpenAI-compatible chat, listwise, a local 14B) and `anthropic` (Messages API, listwise, Haiku, reference
  arm). `cross,embed` fuses the two orders by reciprocal rank fusion (k = 60).
- **Document text.** Repo path, the file's one-line purpose from the repository's code map
  (`docs/engineering/code-map.md`) and its first five public symbols (same line). A missing code map leaves the path.
  BenTech's code map is generated and gitignored, so a historical base has none: the eval runs
  `-codemap-cmd "node scripts/code_map.mjs"` in each case's base worktree, moves the result out of the tree (the
  index stays fresh) and hands its path over as `IDIOM_RERANK_CODEMAP`. Without it every document is a bare path.
- **Never a failure.** Unreachable server, timeout (default 2 s local, 30 s chat and Anthropic), HTTP error, bad JSON,
  all-equal scores (a model without a rank head) or an unparsable listwise reply return Idiom's order with
  `rerank: off: <reason>` in the report (`rerank` field, and the tool summary). Any failed channel of a fused arm
  fails the whole arm, so an arm never runs on fewer channels than it names.
- **Network.** Backend hosts must be loopback or on the tailnet (100.64.0.0/10) unless
  `IDIOM_RERANK_ALLOW_REMOTE=1`. Only the Anthropic backend leaves the user's machines. Endpoints:
  `IDIOM_RERANK_CROSS_URL`, `IDIOM_RERANK_EMBED_URL`, `IDIOM_RERANK_CHAT_URL` (+ `_MODEL`). The eval raises the
  30 s tool budget with `IDIOM_TOOL_TIMEOUT_SECONDS` (it only raises): context_impact takes 16-27 s on late BenTech bases. Defaults are
  loopback ports; the tailnet addresses are set by the caller's environment, not committed.
- **Queue.** If `IDIOM_RERANK_POOL` is set, each real call group runs inside `runner.MaybeAcquire` (the existing
  `idiom queue` gate), with the daemon unreachable meaning ungated, as for the other entry points.
- **Cache and log.** Results are cached by sha256 of (prompt version, pool size, query, candidate texts, backends and
  models) under `IDIOM_RERANK_CACHE_DIR`; only successes are cached. `IDIOM_RERANK_LOG` gets one JSON line per call
  (key prefix, backends, status, cached, latency, tokens): no query, no file text.
- **Key.** The Anthropic key is read at call time from the file named by `BENTECH_API_KEY_FILE` (default
  `~/.config/anthropic/bentech.key`) and sent only in the `x-api-key` header. Errors never contain it.
- **Dry run.** `IDIOM_LLM_RERANK=dry` builds the real candidate lists, calls nothing and logs estimated tokens
  (bytes / 3.5). `idiom-localization-eval rerank-report` turns the log into calls, tokens and USD.

## Alternatives rejected

- **Python sidecar with torch and transformers.** Exact Qwen3 scoring, but several GB of installs.
- **ONNX Runtime from Go.** Needs cgo, a native library and an export step.
- **Reranker inside the Go binary.** Same native-runtime problem; a loopback or tailnet HTTP call is small and
  already served by llama.cpp, mlx and ollama.
- **Always widening the pool.** Widening only when the stage is on keeps the default path byte-identical.

## Eval

`scripts/g885-rerank-arm.sh` runs one arm (`run`, `case-scores`, `compare-scores` for dev and hold-out,
`rerank-report`). Arms: installed (no rerank), `cross`, `cross,embed`, a dry control (pool widened to 30, order
unchanged), and the 14B and Haiku references. Case files, caches and logs stay in `.cache/` (ignored).

## Research

- LocAgent, graph-guided LLM agents for code localization: https://arxiv.org/abs/2503.09089 (takes: file purpose
  text plus symbols as the unit a model reads; rejects: a multi-turn agent, too slow for a per-query stage).
- RankGPT, listwise reranking with LLMs: https://arxiv.org/abs/2304.09542 (takes: one call, the list in, a
  permutation out; the `chat` and `anthropic` arms).
- Qwen3 Embedding and Reranker models: https://arxiv.org/abs/2506.05176 (takes: a 0.6B cross-encoder and embedder
  small enough for a sub-second stage).
- Reciprocal rank fusion, Cormack, Clarke and Buettcher, SIGIR 2009: https://dl.acm.org/doi/10.1145/1571941.1572114
  (takes: score-free fusion with k = 60).
- llama.cpp server `--rerank` and `/v1/rerank`: https://github.com/ggml-org/llama.cpp/tree/master/tools/server
  (takes: the request and reply shape; community GGUFs can lack the rank head, hence the all-equal-scores guard).
- Haiku 5.5 pricing, $0.10 input and $0.50 output per million tokens up to 100K-token prompts: Anthropic models
  overview, https://platform.claude.com/docs/en/about-claude/models/overview (and the pricing page,
  https://platform.claude.com/docs/en/about-claude/pricing).
