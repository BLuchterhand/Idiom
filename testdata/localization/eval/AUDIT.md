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
| p4-bm25f (`pc-idiom-p4-bm25f` e5e3dc03; extra files in their own statistics class, weight 1.0) | .422 / .765 / .455 | .328 / .762 / .477 (21) | .447 / .818 / .549 | .372 / .813 / .615 | **equal to base on both fresh3 corpora** (all 21 BenTech cases identical); shipped by the Mac ruling on dev gain |
| p5-timesplit (`pc-idiom-p5-timesplit` 77f8392e; history before the cutoff, fresh3 commits excluded) | see time split | not read | see time split | not read | **fail on dev confirm** |

p4-bm25f weight 0.5 equals base on both dev sets, so weight 1.0 was chosen on BenTech dev. BenTech fresh3 case
41e16bd69 hit a git lock in the shared clone (a concurrent stream) and was rescored alone with the same binary,
cache and settings (`-only 41e16bd69`): 0 / 0 / 0, as base. The first report of .345 / .800 / .501 averaged 20
cases against the 21 of base and was wrong; on 21 cases every case equals base. Extra files enter no fresh3 top 8.

p5-timesplit cutoffs: BenTech 900a05152 (2026-11-25T13:05-05:00), Idiom 40ea5c869 (2026-11-25T16:05-05:00).
Leakage audit: 0 cases read their own change. 0 BenTech fresh3 commits are in any dev history. 12 Idiom fresh3
commits were ancestors of dev bases and are now removed through `history_exclude`. Dev cases are sorted by
commit time: the oldest 2/3 select the weight, the newest 1/3 confirm it. Idiom confirm (15 cases): base
.393 / .800 / .352; weights 0.25, 0.5 and 1.0 score .451-.461 recall but .733 hit. One case loses its hit,
so every weight fails "no worse on any mean". It was not tuned on the confirm slice, and fresh3 was not read.

## bentech-usage (built 2026-11-27, PC Idiom lane, not gating yet)

Cases from real agent use, built by `idiom-localization-eval build-usage-cases`. The seed (the builder config:
regexes and numbers only) and the case files are kept on each machine and never committed, because the case files
hold query text from private sessions. `.gitignore` excludes `testdata/localization/eval/*usage*` and
`.cache/g755/*.csv`. Seed fields as used on the PC: `query_regex` `(?s)idiom\.sh\s+(?:context_impact|context_query)\b(.*)`,
`path_root_regex` `(?i)^.*?/bentech(?:/\.claude/worktrees/[^/]+)?/(.+)$`, `label_tools` Read, Edit, Write,
MultiEdit, `window_minutes` 30, `dedupe_jaccard` 0.8, `worktree_regex` `(?i)/bentech/\.claude/worktrees/([^/\s"'&;)]+)`,
`branch_ref_prefix` `refs/remotes/origin/`, plus a drop rule for emails, absolute or home paths and `<placeholders>`.
The source, test and doc regexes, `max_gold_files` 60 and `min_query_len` 12 are the same as fresh3's.

**Source.** This PC's Claude Code transcripts for BenTech, including subagents, from 2026-11-11 (the oldest one kept)
to 2026-11-27. The repository PostToolUse logger (`build/idiom/usage/usage.jsonl`, G755) held 36 lines from one
worktree. That logger is on origin/main, but most local checkouts predate it, so the transcripts are the record.

**Label rules.**
- A query is each Bash `scripts/idiom.sh context_impact` or `context_query` call. Its text comes from `task=` or
  `query=`, or from the JSON argument.
- A query is dropped if it contains an email address, an absolute or home path, or a `<placeholder>`.
- A query's labels are the repository files it opened with Read, Edit, Write or MultiEdit within 30 minutes. The
  session's next query ends the window early.
- These labels are dropped: files outside the repository, files the session had opened before the query, and files
  whose name the query itself contains. Files named in the brief are kept (`drop_brief_named` is off; turning it
  on gains only 4 cases).
- Queries whose word sets overlap by Jaccard 0.8 or more merge into the earliest, which takes the union of their
  labels.
- Each case's base is the newest commit on BenTech origin/main at the query time.
- Labels are sorted with the same source, test and doc regexes as fresh3. A source label missing at base becomes
  gold_new. A case needs at least one gold file.

**Funnel.** 220 calls in, 19 dropped, 28 merged. 58 had no labels and 71 had no gold (27 of them only on source
files newer than main), leaving 44 cases. Labels: 1237 opened, 133 dropped as already opened before the query.

**Split by time.** Cut `split_cut` 2026-11-16T22:52:49Z: dev 29 cases, hold-out 15.

**VOID: baseline from the installed binary.** The first baseline ran the installed `~/.idiom/bin/idiom.exe` (sha256
5e0572b7…, dated 2026-11-15) and scored dev .251 / .586 / .343 and hold-out .117 / .200 / .122. That binary is not
e324acb1 (see the next section), so these numbers measure no committed version and are void. They are kept only so
the earlier report can be traced. The e324acb1 baselines in the gates above were built from e324acb1 source
(`pc-idiom-gate` has no Go change from it), so they stand.

**Short of the 100-case goal.** Ways to close the gap:
- add the Mac's transcripts: the builder takes any number of transcript directories;
- add weeks of use;
- resolve each base on the agent's own branch rather than main, which would recover up to 27 cases.

**Exposure bias.** The labels include files that agents opened because Idiom listed them. That favours rankers like
the installed one, so use this set as a paired comparison, not an absolute score.

### 2026-11-27 bentech-usage rescored with a pinned e324acb1 build; branch bases; Mac combined

**Pinned binary.** e324acb1 was built from a temporary worktree into `build/eval-e324acb1/idiom.exe`, and the
worktree was removed afterwards. It does not match the installed `~/.idiom/bin/idiom.exe`:
- the sha256 hashes differ (6d318896… against 5e0572b7…);
- the installed file is dated 2026-11-15, older than the e324acb1 commit (2026-11-25);
- both print version "dev";
- on 3 cases the rankings differ (recall .5/1/0 against .25/0/0).

The installed baseline above is therefore not e324acb1. All scoring below uses the e324acb1 build. Two cases (one
per set) hit e324acb1's 30 s dispatch timeout on the cold call. They were rescored with the same commit plus only
the eval timeout patch (`idiom-base.exe`, ranking code unchanged). The runner now reports idiom's stdout when the
tool fails, which is where that timeout is reported.

**Branch bases** (`-base branch`). Each case's base is the newest commit at query time on the agent's own branch:
- the checkout is found from a worktree the command `cd`s into, else from the session cwd;
- the branch is found from `git worktree list` or the branch the transcript recorded;
- queries whose branch is unknown or deleted are dropped (29).

This gives 39 cases, fewer than main bases (44). Branch bases do not recover the files agents create, because
those are opened before they are committed.

**Cut** fixed at 2026-11-16T22:52:49Z (`-split-cut`). Per-case scores (id, split, recall@8, hit@8, MRR@8, query
time, no query text) are written by `case-scores` and kept local.

| Set | dev n | dev recall / hit / MRR | hold-out n | hold-out recall / hit / MRR |
|---|---|---|---|---|
| PC, main bases | 29 | .362 / .655 / .392 | 15 | .192 / .333 / .182 |
| PC, branch bases | 26 | .365 / .692 / .424 | 13 | .240 / .385 / .276 |
| Mac (35, all hold-out) | 0 | — | 35 | .261 / .457 / .301 |
| PC main + Mac | 29 | .362 / .655 / .392 | 50 | .240 / .420 / .265 |
| PC branch + Mac | 26 | .365 / .692 / .424 | 48 | .256 / .438 / .294 |

Combined rows sum per-case scores and divide by the case count. No case id appears on both machines, and every
Mac case is after the cut.

## usage-eval v1 (frozen 2026-11-27)

**Set.** BenTech usage cases with branch bases, cut 2026-11-16T22:52:49Z: 74 cases. Dev has 26, all from the PC.
Hold-out has 48: 13 from the PC and 35 from the Mac. (The main-base variant would be 79 cases, but v1 uses branch
bases.) Case files and score files stay on the machine that built them. These sha256 hashes pin them:
- PC cases `usage-v1/pc-cases.json`: 28cfe5e9fbeb487f881396f31a6e0711023558077ab55bbf2d0ab7ae01ea5081
- PC baseline scores `usage-v1/baseline-pc.csv`: 0e95f7ef567b758a544835f7e894c2ce4688cd915f451d5e9c30b883f4c09191
- Mac baseline scores `usage-v1/baseline-mac.csv`: 7fe8b8db2d2e2cfe62293629a64d4a7a61ce0502f1a8aa59584f51b88d6cff89

**Baseline** e324acb1 (recall / hit / MRR @8): dev .365 / .692 / .424, hold-out .256 / .438 / .294.

**Judging rule.** A candidate is judged on the hold-out, paired per case against the v1 baseline scores. Report wins,
losses and ties per metric, with an exact two-sided sign test p-value (ties dropped):

    idiom-localization-eval compare-scores -split holdout \
      -before usage-v1/baseline-pc.csv,usage-v1/baseline-mac.csv \
      -after <candidate-pc.csv>,<candidate-mac.csv>

Each machine scores its own cases with `run`, then writes its score file with `case-scores`. The two files join by
case id; an id present on both machines is an error. Dev is for tuning only.

**Re-run policy.** A new version is built monthly, or once 25 new cases have accrued since the last one, from
sessions newer than the previous version's newest query. Older weeks are never mined to grow a set. v1 stays
frozen: a new version gets its own name, cut and hashes.
