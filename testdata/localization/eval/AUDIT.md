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

### 2026-11-26 PC gate: p5, p4, p5+p4 against e324acb1

**Dev choice, fixed before any fresh3 score was read** (recall@8 / hit@8 / MRR@8; phase-3 rule: highest dev
recall, ties by hit then MRR):

| Arm | BenTech dev (51) | Idiom dev (44) |
|---|---|---|
| base e324acb1 | .402 / .745 / .450 | .447 / .818 / .549 |
| p5 without hub discount (34fc1504) | .433 / .804 / .547 | .505 / .818 / .631 |
| p5 with hub discount (efdd8ba2) | .425 / .740 / .519 (50 cases, 1 query error) | .497 / .818 / .643 |
| p4 (62e43c1d) | pending at the time of the choice | .447 / .818 / .549 |

The hub discount loses recall on both corpora and .06 hit on BenTech (scoring the errored case at 1.0 would
still leave hit at .745), so **p5 ships without it** (`pc-idiom-p5a`); p5+p4 is `pc-idiom-p5ap4`. p4 has no dev
knob left (x0.5 chosen on dev in 62e43c1d); its BM25F experiment 87ae2d48 is excluded, as its own message marks
it not shippable. Eval binaries raise the context_impact dispatch timeout to 5 min and context_index to 40 min
(BenTech queries take 18-29 s on this PC); ranking code is unchanged.

**fresh3, one run per candidate** (file hashes checked against the freeze before reading; all cases scored,
deterministic on repeat):

| Arm | BenTech dev (51) | BenTech fresh3 (21) | Idiom dev (44) | Idiom fresh3 (16) | Verdict |
|---|---|---|---|---|---|
| base e324acb1 | .402 / .745 / .450 | .328 / .762 / .477 | .447 / .818 / .549 | .372 / .813 / .615 | installed |
| p5 (`pc-idiom-p5a`) | .433 / .804 / .547 | .296 / .762 / .417 | .505 / .818 / .631 | .477 / .875 / .594 | **fail**: BenTech fresh3 recall and MRR, Idiom fresh3 MRR |
| p4 (`pc-idiom-p4`) | .407 / .765 / .454 | .326 / .762 / .477 | .447 / .818 / .549 | .372 / .813 / .615 | **fail by .002 recall** on BenTech fresh3 (2 of 21 cases reorder, +.125 / -.167); hit and MRR equal or better everywhere |
| p5+p4 (`pc-idiom-p5ap4`) | not run (verdict already fixed by fresh3) | .302 / .762 / .414 | .505 / .818 / .631 | .477 / .875 / .594 | **fail**, as p5 |

p5's dev gain does not hold on unseen cases: BenTech fresh3 MRR per case 4 wins, 10 losses. p4 extra files
(.json, .gdshader, .ps1) reach the top 24 in 2 of 51 dev cases and 0 of 21 fresh3 cases, while gold holds 23 and
7 such files: p4's fresh3 movement comes from re-ranking source files (corpus statistics shift once the extra
documents join BM25), not from extra files displacing them.

### 2026-11-27 PC gate: p4-bm25f and p5-timesplit against e324acb1

Design note: `docs/design-localization-classes-and-history-split.md` (on both candidate branches). Ship rule:
beat base on recall@8 and be no worse on hit@8 and MRR@8, on both corpora; equal is a fail.

| Arm | BenTech dev (51) | BenTech fresh3 | Idiom dev (44) | Idiom fresh3 (16) | Verdict |
|---|---|---|---|---|---|
| base e324acb1 | .402 / .745 / .450 | .328 / .762 / .477 (21) | .447 / .818 / .549 | .372 / .813 / .615 | installed |
| p4-bm25f (`pc-idiom-p4-bm25f` e5e3dc03; extra files in their own statistics class, weight 1.0) | .422 / .765 / .455 | .345 / .800 / .501 (20 of 21) | .447 / .818 / .549 | .372 / .813 / .615 | **fail**: equal on Idiom (no extra files configured there) |
| p5-timesplit (`pc-idiom-p5-timesplit` 77f8392e; history before the cutoff, fresh3 commits excluded) | see time split | not read | see time split | not read | **fail on dev confirm** |

p4-bm25f weight 0.5 equals base on both dev sets, so weight 1.0 was chosen on BenTech dev. BenTech fresh3 case
41e16bd69 did not score (a git lock in the shared clone, from a concurrent stream). It is not rerun because the
Idiom tie already fixes the verdict.

p5-timesplit cutoffs: BenTech 900a05152 (2026-11-25T13:05-05:00), Idiom 40ea5c869 (2026-11-25T16:05-05:00).
Leakage audit: 0 cases read their own change. 0 BenTech fresh3 commits are in any dev history. 12 Idiom fresh3
commits were ancestors of dev bases and are now removed through `history_exclude`. Dev cases are sorted by
commit time: the oldest 2/3 select the weight, the newest 1/3 confirm it. Idiom confirm (15 cases): base
.393 / .800 / .352; weights 0.25, 0.5 and 1.0 score .451-.461 recall but .733 hit. One case loses its hit,
so every weight fails "no worse on any mean". It was not tuned on the confirm slice, and fresh3 was not read.
