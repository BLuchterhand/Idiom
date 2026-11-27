# Past-work channel (G884)

Status: candidate. Gate: usage-eval v1 (`testdata/localization/eval/AUDIT.md`), ships only if no worse than the
frozen e324acb1 scores on hold-out. Run command: `scripts/g884-eval.sh` (header documents every option).

## Problem

File ranking fuses BM25 over file text and the symbol graph by RRF. Both read the code as it is now. Neither knows
that "the last three times someone worked on X they edited A, B and C", or that A and B change together. An agent's
task text usually repeats the words of earlier work (a ledger row title, a commit message) more than a file's own words.

## Design

Two extra RRF lists, both from the repository's past, both cut at one instant.

1. **Similar work.** A *work item* is a planning-ledger row joined to every commit whose message names the row id
   (merges count only when they name a row; their diff against the first parent is the row's contribution), or a
   lone commit that names no row. Item text is the row line plus the commit messages. BM25 over items, score decayed by
   age (half-life 30 days), best 30 items give each of their files `score / log2(1 + files)`.
2. **Co-change.** Seeds are the top 8 of the fused bm25, graph and similar-work lists. A file's score is the sum over
   seeds of `w_seed * cos(seed, file)`, where cos is `shared / sqrt(changes_seed * changes_file)` over age-decayed
   commit counts. Pairs need 2 shared commits. Only non-merge commits of at most 50 files count: a sweep commit says
   little about any pair, and a merge's diff repeats its branch commits.

Both lists carry a weight on their RRF term (`rankedList.Weight`, default 1). Weights and the ledger path live in the
`history` section of `.idiom/config.json`, apart from `localization`, because they shape ranking and never the index
(no index cache invalidation). `{"disabled":true}` is the channel-off arm, so one binary measures both. The pack
cache key gains `HistoryFingerprint` (config, ledger bytes, HEAD hash): a new commit or ledger edit must miss it.

### Leakage

History is read from ancestors of the checked-out commit, so a case run at its base never sees later commits. On top,
`history.as_of` cuts by time: commits newer than `min(HEAD time, as_of)` are dropped in Go (commit dates are not
monotone, so `git --before` is not trusted). The eval runner writes each case's query time as `as_of`. The ledger is
read from the checkout, so it is the base's ledger. `TestHistoryNeverReadsPastTheCut` builds a repository whose newest
commit repeats the task and shows that neither list reaches its files, with an uncut control; it fails with the cut
removed (break-checked).

### Access pattern and cost

One `git log --name-only --diff-merges=first-parent` of at most 20000 commits per query, parsed once (BenTech: about
3.6k non-merge commits), then O(commits x files) for the co-change pass over seeds only. No index change. If the
measured per-query cost is high, the parsed history can be cached keyed by `HistoryFingerprint`; see AUDIT for the number.

### Alternatives rejected

- Learned ranker over history features: 95 labelled cases are far too few.
- A persistent co-change store in the index: the index would need rebuilding per commit; the git log is cheap enough.
- Merge commits as ordinary commits: double-counts branch commits and inflates co-change.
- Earlier p5 (commit messages only, no ledger, no co-change) lost on fresh3; this channel adds the ledger link and
  coupling and is judged on usage-eval v1 instead.

## Research

- Zimmermann, Weissgerber, Diehl, Zeller, "Mining Version Histories to Guide Software Changes" (ICSE 2004;
  https://www.st.cs.uni-saarland.de/softevo/changes.php): association rules over change transactions, support and
  confidence thresholds. Taken: minimum support, no rules from sweep transactions. Rejected: rule mining at entity
  level (we rank files).
- Gall, Hajek, Jazayeri, "Detection of Logical Coupling Based on Product Release History" (ICSM 1998): the term
  and the shared-change threshold idea (see the coupling survey, https://www.inf.unibz.it/~rrobbes/p/WCRE2008-coupling.pdf).
- Cubranic, Murphy et al., "Hipikat: Recommending Pertinent Software Development Artifacts" (ICSE 2003): link
  change records, issues and documents by text, recommend the artifacts of similar past work. Taken: the ledger row as the issue
  surrogate, joined to commits by id. Not re-read for this note.
- Chen et al., "LocAgent: Graph-Guided LLM Agents for Code Localization" (https://arxiv.org/abs/2503.09089): graph
  neighbourhood as a localisation signal. Taken: expand from top candidates along a graph (here co-change edges).
  Rejected: the LLM agent loop.
- Cormack, Clarke, Buettcher, "Reciprocal Rank Fusion" (SIGIR 2009): the existing fusion; we add weights only.
- Kapoor and Narayanan, "Leakage and the Reproducibility Crisis in ML-based Science" (2022): temporal leakage
  motivates the explicit time cut and its test.
- Searches this run: Zimmermann ROSE, Hipikat and LocAgent; the search tool returned ROSE and coupling papers only.
