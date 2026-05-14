# P9.5 Review-Protocol Compliance Gate

Gate date: 2026-05-14

Decision: no-go, needs durable review evidence. P9.5 does not support a release
readiness claim.

Artifact acceptance scope: this report can be accepted only as a privacy-safe,
fail-closed compliance inventory. It records the current ledger state, the
review-protocol criteria that must be proved, the follow-up closure shape visible
in the tracker, and the blockers that prevent release readiness. It does not
mark P9.5 reviewed, does not update the implementation tracker, and does not
approve release.

P9.5 passes only when every implemented MVP Task Ledger row has either:

- a durable schema-complete review output from a separate reviewer subagent with
  `Decision: GO`, required criteria evidence, client-classification table,
  product-success table, validation evidence, findings/follow-up disposition,
  and signoff; or
- a project-approved exception, no-go, or pivot recorded in the plan with the
  release claim explicitly blocked or narrowed.

Current evidence fails closed. The ledger says P9.5 is implemented but pending
review, and tracked files do not contain durable schema-complete reviewer
transcripts for the older reviewed rows. The tracker is useful status evidence;
it is not enough by itself to prove full review-protocol compliance.

## Evidence Inputs

| Input | Privacy-safe result | Gate effect |
| --- | --- | --- |
| P9.5 context packet | `context_pack` for the P9.5 task returned a fresh packet with candidate ledger/review files and two stale runtime-trace gaps unrelated to this docs gate. | Localization evidence only. |
| MVP Task Ledger in `docs/idiom-implementation-plan.md` | Parsed 148 P9.5 compliance rows: 141 implemented rows, 140 rows with reviewed checkboxes, P9.5 implemented with review unchecked, and 7 unimplemented future P9 rows. | Establishes tracker state and row counts. |
| Canonical review protocol in `docs/idiom-implementation-plan.md` | Protocol requires separate reviewer subagent, GO/NO-GO decision, criteria checklist, all seven client classifications, product-success table, validation evidence, findings/follow-up tasks, and signoff. | Defines the compliance standard. |
| Tracked review-output search | Search outside ignored/generated logs found the canonical schema template and P9.4 reviewer-output instructions, but no row-specific durable `Decision: GO` / schema-complete review transcript set for the 141 implemented rows. | Blocks release-readiness compliance. |
| P9.2 decision and P9.3/P9.4 gate artifacts | P9.2 is no-go/continue; P9.3 and P9.4 are fail-closed no-go artifacts with follow-up rows that corrected artifact scope/evidence, not release readiness. | Confirms P9.1-P9.4 do not release-pass. |
| P9.1 smoke command | P9.5 reproduction uses `go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.5-smoke.json`; generated output is ignored local evidence. | Confirms current release evidence remains a local smoke input, not a committed bundle body. |

Do not commit ignored `.idiom/release` outputs. This report stores only relative
paths, commands, counts, statuses, row IDs, and bounded summaries.

## Summary Metrics

| Metric | Current value | Evidence basis | Gate effect |
| --- | ---: | --- | --- |
| Total ledger rows | 148 | MVP Task Ledger parse | Informational. |
| Total implemented rows | 141 | Rows with `Implemented` checked | Scope of P9.5 compliance audit. |
| Ledger-reviewed rows | 140 | Rows with `Reviewed` checked | Status tracker is no longer complete because P9.5 is pending review. |
| Implemented rows with unchecked review | 1: P9.5 | Ledger parse | P9.5 remains a release blocker until separate review returns schema-complete GO or the plan records an approved exception, no-go, or pivot. |
| Rows with durable schema-complete review transcript verified in tracked files | 0 | Tracked-file search for row-level canonical output | Blocks release. |
| Implemented rows missing durable review evidence | 141 | Implemented count minus verified durable transcripts | Blocks release until supplied or explicitly excepted/no-go. |
| Parent rows with follow-up references | 62 | Reviewer cell references to follow-up IDs | Tracker indicates prior no-go/follow-up closure flow. |
| Parent rows whose referenced follow-ups are implemented and reviewed | 62 | Every referenced follow-up row is implemented and reviewed in the ledger | Ledger-level follow-up closure is visible, but transcript-level closure still needs evidence. |
| Unique referenced follow-up rows closed | 63 | Referenced follow-up IDs implemented and reviewed | Informational support for closure discipline. |
| Fail-closed/no-go primary release-gate rows | 3 | P9.2, P9.3, P9.4 | These rows are accepted only as no-go/partial gate artifacts, not release GO. |
| Unimplemented future rows | 7 | P9.6-P9.12 unchecked in the ledger | Release cannot pass while these remain unimplemented. |
| Release readiness status | NO-GO | Missing durable review transcripts, P9.5 pending review, and P9.6-P9.12 unimplemented | Blocks release. |

## Protocol Criteria Check

| Required criterion | Current status | Evidence and blocker |
| --- | --- | --- |
| Separate reviewer subagent | Partial | Ledger records distinct implementer and reviewer names for implemented rows. The tracked files do not include row-level review signoff transcripts proving the reviewer was the separate review subagent for each row. |
| GO/NO-GO decision | Partial/fail-closed | The canonical schema requires `Decision: GO | NO-GO`; tracked row-level review decisions were not found. P9.2/P9.3/P9.4 explicitly remain no-go release artifacts. |
| Completeness | Missing durable evidence | No row-level criteria table was found for all implemented rows. |
| Correctness | Missing durable evidence | No row-level criteria table was found for all implemented rows. |
| North-star alignment | Missing durable evidence | The plan defines the north star and reviewer criterion, but durable per-row reviewer findings are absent. |
| Bugs and regressions | Missing durable evidence | No per-row bug/regression review transcript set was found. |
| Security and privacy | Missing durable evidence | P9.3/P9.4/P9.5 reports preserve metadata-only artifacts, but older row-level review transcripts are absent. |
| Performance | Missing durable evidence | No per-row performance review transcript set was found. |
| Client-classification coverage | Missing durable evidence | The protocol requires all seven rows: finance, healthcare, public-sector, enterprise-saas, tooling-vendor, local-model, and open-source. Ledger status alone does not prove each row was reviewed criterion-by-criterion. |
| Product-success coverage | Missing durable evidence | The protocol requires ICP, activation, retained use, integration proof, security/privacy, competitive proof, client demo proof, packaging/support, and local product-evidence export review. Row-level transcripts were not found. |
| Verification and validation | Partial | Ledger/work-package rows list validation expectations; durable per-row reviewer validation evidence was not found. |
| Recorded follow-up or no-go/pivot for failures | Partial | Ledger reviewer cells reference follow-up IDs for 62 parent rows, and all referenced follow-ups are implemented and reviewed. This proves tracker-level closure, not schema-complete review closure. |

## Ledger Coverage

The ledger currently distinguishes implemented/reviewed rows from future work:

| Phase | Implemented rows | Ledger-reviewed rows | Durable transcript status |
| --- | ---: | ---: | --- |
| P0 | 18 | 18 | Missing row-level canonical review transcripts in tracked files. |
| P1 | 11 | 11 | Missing row-level canonical review transcripts in tracked files. |
| P2 | 15 | 15 | Missing row-level canonical review transcripts in tracked files. |
| P3 | 10 | 10 | Missing row-level canonical review transcripts in tracked files. |
| P4 | 18 | 18 | Missing row-level canonical review transcripts in tracked files. |
| P5 | 21 | 21 | Missing row-level canonical review transcripts in tracked files. |
| P6 | 7 | 7 | Missing row-level canonical review transcripts in tracked files. |
| P7 | 19 | 19 | Missing row-level canonical review transcripts in tracked files. |
| P8 | 12 | 12 | Missing row-level canonical review transcripts in tracked files. |
| P9 implemented | 10 | 9 | P9.1-P9.5 families have fail-closed implementation artifacts; P9.5 is pending review, and row-level canonical transcripts are still incomplete. |
| P9 future | 7 | 0 | P9.6-P9.12 remain unimplemented in the ledger. |

Implemented row IDs by phase:

- P0: P0.1, P0.2, P0.12, P0.13, P0.3, P0.14, P0.4, P0.15,
  P0.16, P0.17, P0.5, P0.6, P0.7, P0.8, P0.9, P0.18, P0.10,
  P0.11.
- P1: P1.1, P1.7, P1.2, P1.3, P1.8, P1.4, P1.5, P1.9, P1.10,
  P1.6, P1.11.
- P2: P2.1, P2.2, P2.5, P2.6, P2.8, P2.7, P2.3, P2.9, P2.4,
  P2.10, P2.11, P2.12, P2.13, P2.14, P2.15.
- P3: P3.1, P3.6, P3.2, P3.7, P3.3, P3.8, P3.9, P3.4, P3.10,
  P3.5.
- P4: P4.1, P4.2, P4.3, P4.7, P4.8, P4.9, P4.10, P4.11, P4.12,
  P4.4, P4.13, P4.14, P4.5, P4.15, P4.16, P4.17, P4.6, P4.18.
- P5: P5.1, P5.5, P5.6, P5.2, P5.7, P5.8, P5.3, P5.9, P5.10,
  P5.4, P5.11, P5.12, P5.13, P5.14, P5.15, P5.16, P5.17, P5.18,
  P5.19, P5.20, P5.21.
- P6: P6.1, P6.2, P6.6, P6.3, P6.4, P6.5, P6.7.
- P7: P7.1, P7.2, P7.12, P7.3, P7.13, P7.4, P7.14, P7.5, P7.6,
  P7.15, P7.7, P7.16, P7.8, P7.9, P7.10, P7.17, P7.11, P7.18,
  P7.19.
- P8: P8.1, P8.2, P8.7, P8.8, P8.3, P8.4, P8.5, P8.9, P8.6,
  P8.10, P8.11, P8.12.
- P9 implemented: P9.1, P9.13, P9.14, P9.15, P9.2, P9.3, P9.16,
  P9.4, P9.17, P9.5.

Implemented rows with unchecked review: P9.5.

Unimplemented future row IDs: P9.6, P9.7, P9.8, P9.9, P9.10, P9.11,
and P9.12.

## Follow-Up Closure State

The tracker records 62 implemented parent rows whose reviewer cell names one or
more follow-up rows. All 63 unique referenced follow-up rows are themselves
implemented and reviewed in the ledger:

P1.7, P1.8, P1.9, P1.10, P1.11, P2.5, P2.6, P2.7, P2.8, P2.9,
P2.10, P2.11, P2.12, P2.14, P2.15, P3.6, P3.7, P3.8, P3.9, P3.10,
P4.7, P4.8, P4.9, P4.10, P4.11, P4.12, P4.13, P4.14, P4.15, P4.16,
P4.17, P4.18, P5.5, P5.6, P5.7, P5.8, P5.9, P5.11, P5.12, P5.15,
P5.17, P5.20, P6.6, P6.7, P7.12, P7.13, P7.14, P7.15, P7.16,
P7.17, P7.18, P7.19, P8.7, P8.8, P8.9, P8.10, P8.11, P8.12,
P9.13, P9.14, P9.15, P9.16, and P9.17.

This is sufficient to distinguish tracker-level no-go/follow-up closure from
missing review evidence. It is not sufficient to infer that each original
NO-GO review and follow-up closure met the full canonical schema.

## P9.1-P9.4 State

| Row or family | Current state | P9.5 interpretation |
| --- | --- | --- |
| P9.1 | Implemented and reviewed in the ledger. Reviewer cell records P9.13/P9.14/P9.15 follow-ups, all implemented and reviewed. Current release evidence is still partial/no-go per P9.2 and later gates. | Tracker-level review/follow-up closure exists; release-readiness and row-level transcript compliance are not proved. |
| P9.2 | Implemented and reviewed in the ledger. `docs/release-decision-p9.2.md` records no-go/continue implementation and requires P9.3-P9.12 reviewed gates before release. | Accepted only as a no-go decision artifact. |
| P9.3 | Implemented and reviewed in the ledger. `docs/release-gate-p9.3.md` records no-go/needs-evidence with open high/medium finding closure blockers. P9.16 is implemented/reviewed and clarifies fail-closed artifact scope. | Accepted only as a fail-closed no-go gate artifact; not release readiness. |
| P9.4 | Implemented and reviewed in the ledger. `docs/release-gate-p9.4.md` records no-go until validation records and independent reviewer signoff exist. P9.17 is implemented/reviewed and grounds the report in current evidence. | Accepted only as a fail-closed no-go coverage artifact; not release readiness. |
| P9.5 | Implemented in the ledger and pending review. Reviewer cell records Plato the 3rd plus the P9.18 follow-up. | This report remains a release blocker until a separate reviewer returns schema-complete GO or the plan records an approved exception, no-go, or pivot. |
| P9.6-P9.12 | Unimplemented and unreviewed. | Release cannot pass while these rows remain future work. |

## Client-Classification Impact

P9.5 applies to all seven client classifications because review protocol is the
gate that prevents unsupported product, security, privacy, and sales claims from
escaping as release evidence.

| Classification | Impact |
| --- | --- |
| finance | NO-GO. Regulated proof-only until every implemented task that affects controls, protected paths, audit, release evidence, or packaging has durable review evidence. |
| healthcare | NO-GO. Missing durable review transcripts block claims about redaction, no raw prompt/file-body exposure, PHI-safe examples, or privacy posture. |
| public-sector | NO-GO. Reproducibility and governance claims require reviewable task evidence, not checkbox-only status. |
| enterprise-saas | NO-GO. Primary paid-beachhead readiness depends on reviewed merge-facing evidence, validation, and product-success gates. |
| tooling-vendor | NO-GO. Embedding/conformance claims need schema-complete review evidence for sidecar, CLI/MCP, and no-strategy-leakage work. |
| local-model | NO-GO. Local readiness, queue, backend, and privacy evidence require durable review verification before release claims. |
| open-source | NO-GO. Transparent community release can use the tracker as orientation, but not as a substitute for canonical review evidence. |

## Required Closure

P9.5 can be reconsidered only after a privacy-safe review-protocol audit records:

- every implemented row ID and its durable review evidence reference, or an
  explicit project-approved exception/no-go/pivot;
- reviewer identity distinct from implementer identity for every row;
- `Decision: GO` or `Decision: NO-GO` for every row, with no unchecked required
  follow-up hidden behind a reviewed checkbox;
- criterion evidence for completeness, correctness, north-star alignment,
  client-classification coverage, product-success coverage, bugs/regressions,
  performance, security/privacy, observability/auditability, verification, and
  maintainability;
- all seven client classifications represented in the impact table;
- P9.2/P9.3/P9.4 preserved as no-go/fail-closed artifacts unless fresh evidence
  changes their release status;
- P9.6-P9.12 implemented and reviewed, or release explicitly pivoted/narrowed;
- no raw prompts, raw protocol bodies, raw file bodies, absolute private paths,
  sentinels, secrets, tokens, credentials, or ignored generated bundle bodies in
  the committed artifact.

## Reproduction Commands

```sh
python3 - <<'PY'
from pathlib import Path
import re
rows = []
inside = False
for line in Path("docs/idiom-implementation-plan.md").read_text().splitlines():
    if line.startswith("| ID | Task | Depends On | Implementer | Implemented | Reviewer | Reviewed |"):
        inside = True
        continue
    if inside:
        if line.startswith("## Work Packages"):
            break
        if not line.startswith("| ") or line.startswith("| ---"):
            continue
        cols = [c.strip() for c in line.strip().strip("|").split("|")]
        if len(cols) == 7:
            rows.append(cols)
rows = [r for r in rows if r[0] != "P9.18"]
status = {r[0]: ("[x]" in r[4], "[x]" in r[6]) for r in rows}
implemented = [r for r in rows if "[x]" in r[4]]
reviewed = [r for r in rows if "[x]" in r[6]]
missing_review = [r[0] for r in implemented if "[x]" not in r[6]]
future = [r[0] for r in rows if "[x]" not in r[4]]
parents = []
open_followups = []
for r in rows:
    ids = sorted(set(re.findall(r"P\d+\.\d+", r[5])))
    ids = [i for i in ids if i != r[0] and i != "P9.18"]
    if ids:
        parents.append(r[0])
        for i in ids:
            if i not in status or status[i] != (True, True):
                open_followups.append((r[0], i, status.get(i)))
print("total_rows", len(rows))
print("implemented", len(implemented))
print("reviewed", len(reviewed))
print("implemented_missing_review", missing_review)
print("future", future)
print("followup_parent_rows", len(parents))
print("open_followups", open_followups)
assert len(rows) == 148
assert len(implemented) == 141
assert len(reviewed) == 140
assert missing_review == ["P9.5"]
assert future == ["P9.6", "P9.7", "P9.8", "P9.9", "P9.10", "P9.11", "P9.12"]
assert open_followups == []
PY

rg -n "Decision: (GO|NO-GO)|Criteria checklist:|Client-classification example tests:|Product-success requirements:|Review signoff:" \
  . --glob '!logs/**' --glob '!transcripts/**' --glob '!.git/**' --glob '!.idiom/**'

go run ./cmd/idiom release evidence --project-root . --output .idiom/release/p9.5-smoke.json
jq -r '.status' .idiom/release/p9.5-smoke.json
jq -r '.client_readiness[] | "\(.classification)\t\(.status)"' .idiom/release/p9.5-smoke.json
git diff --check
```

Expected current result: ledger counts match the metrics above; P9.5 is
implemented with review pending; tracked files do not contain row-level durable
canonical review transcripts for all implemented rows; release evidence remains
privacy-safe local smoke input; and P9.5 remains NO-GO for release readiness.
