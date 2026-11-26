# Localization eval audit

The record of which case sets gate a ship decision, how each was cut, and which ones are retired. Ranking
configurations are chosen on **dev** only; a hold-out is scored **once per candidate**, after the choice, and its
numbers never feed back into a choice.

## Gating sets (from 2026-11-26)

| Set | File | Cases | Role |
|---|---|---|---|
| BenTech dev | `bentech-cases.json`, split `dev` | 51 | configuration choice |
| Idiom dev | `idiom-cases.json`, split `dev` | 44 | configuration choice |
| BenTech fresh3 | `bentech-fresh3-cases.json`, all cases | 21 | ship gate (run with `-subset all`) |
| Idiom fresh3 | `idiom-fresh3-cases.json`, all cases | 16 | ship gate (run with `-subset all`) |

Ship rule (designer): no split's mean may be worse than the installed binary (e324acb1 at the time of writing).
The fresh3 files keep each case's hash split (`LocalizationEvalSplit`, which the reader enforces); the split label
inside them means nothing for gating: the whole file is the hold-out.

## Retired hold-outs (kept for reference, never gating)

- `bentech-cases.json` split `holdout` (24 cases) and `idiom-cases.json` split `holdout` (16 cases).
- The earlier "fresh" and "fresh2" hold-outs. Their frozen lists (FROZEN.txt, FROZEN2.txt) and the earlier audit
  notes lived in Mac scratch and were never committed; only the branch commit messages quote their numbers.
- p4's `bentech-scoped-cases.json` and `idiom-scoped-cases.json` (scoped variants of the dev and hold-out cases).

Reason: each of these was scored and read across about six rounds (phases 1-3, p4, p5, p6, the research branch),
and later configuration choices were made with those numbers in view. A set that has steered choices is no longer
unseen, so it cannot tell a real gain from a fit to it.

## fresh3 (frozen 2026-11-26, PC Idiom lane, BenTech ledger G755)

**Cut.** The newest commit in any earlier case file of each repository, `main` and the parked branches included:

- BenTech: `900a05152` (2026-11-25T13:05:03-05:00, newest merge in p4's `bentech-scoped-cases.json`).
- Idiom: `40ea5c869` (2026-11-25T16:05:31-05:00, newest commit in p4's `idiom-scoped-cases.json`).

**BenTech recipe.** The same builder config as `bentech-seed.json` on `idiom-loc-p4` (ledger row at base as the
query, the p4 `localization` section so p4's extra file kinds are indexed; the e324acb1 binary parses and ignores
it), with `since=900a05152` and no case cap, run on BenTech `origin/main` `46b5daa40` (2026-11-26T04:06). Every
qualifying merge after the cut is a case: 29 merges, 21 cases.

**Idiom recipe, and why it is not post-cut only.** Idiom history after the cut holds `e324acb1` and the candidate
branches' own commits (p4, p5, p6, research), which cannot judge themselves. Sixteen post-cut cases do not exist. The
property a hold-out needs is that no configuration choice has seen it, not that it is recent (every case indexes
its own base commit, so an older case leaks nothing). So Idiom fresh3 is the 16 newest qualifying commits of
`e324acb1`'s first-parent history (builder config of `idiom-seed.json`, `max_commits` 2000) whose commit is in no
earlier case file (`idiom-cases.json`, p4's `idiom-scoped-cases.json`): `e324acb1` (post-cut), `9fe60f44`,
`55a79c15`, `11a21c93` (localization-line commits from 2026-11-25 that no set took), and twelve from 2026-07-07/08,
just below the window `idiom-cases.json` covers.

**Freeze.**

| File | Cases (hash split dev/holdout) | sha256 of the file | sha256 of the sorted merge list |
|---|---|---|---|
| `bentech-fresh3-cases.json` | 21 (11/10) | `70a823eb63f3afc121cc83f6ab898b2af39c938a232348190f3ba7f677076ff8` | `c2f945c42c5d3baa93329942c4310cc1cbac5b7b7cef2dff13e5020a424e36de` |
| `idiom-fresh3-cases.json` | 16 (9/7) | `f7a5cd9a50732339fcdf1288729038cf6604ede0b9ee55cc0c8e8712fc271c5b` | `3a94d67a31ac751a0b726064ffaa1aa7893507b233f4f5c7d2b584df1b371647` |

The merge-list hash is `sha256` of the sorted full merge hashes, one per line with a trailing newline
(`node -e '...cases.map(c=>c.merge).sort().join("\n")' | sha256sum`, as `console.log` adds the final newline).
Check before every fresh3 run; a mismatch voids the run.

**Scoring discipline.** No fresh3 score was read before the candidate configurations below were fixed on dev.
Each candidate gets one fresh3 run. When fresh3 has been read for about three ship decisions, cut fresh4 the same
way and retire fresh3 here.

## Results log

Filled in by each gate run: candidate, binary commit, dev and fresh3 means, verdict.
