# Citation Cleanup Agreement - 2026-06-24

Supersession note: this is a historical review record. Use
`publication-readiness-tracker.md`, `source-ledger.md`, and
`package-manifest.md` for current beta/proof readiness, citation status, and
package status.

## Scope

Goal: review and clean up citations across the current publication manuscript,
source ledger, bibliography, appendices, exports, and downloadable pilot-kit
artifacts.

Review method:

- Six independent subagent reviewers read assigned source slices directly.
- The main pass verified high-risk sources against primary or official records
  where available.
- Actions were applied only where reviewer findings converged or where a
  mechanical citation-style inconsistency was confirmed.

## Reviewer Slices

| Reviewer | Scope | Consensus Action |
| --- | --- | --- |
| A | Research and guidance sources | Keep cited sources; fix citation keys; soften lifecycle wording where ISO public overview was carrying too much. |
| B | RC1-RC5: Air Canada, Mata, Boeing, CNET, Tessa | Keep all cases; soften Air Canada, Boeing, and Tessa wording. |
| C | RC6-RC10: Ofqual, Horizon, Cruise, MiDAS, Uber/NTSB | Keep all cases; soften Ofqual confidence wording, Cruise layoff causality, and MiDAS human-review wording. |
| D | RC11-RC14: Knight, Waymo, Morgan Stanley, Deloitte | Keep all cases; slightly soften Deloitte source-verification root-cause phrasing. |
| E | Bibliography and author-date mechanics | Normalize citation keys; remove orphan suffixes; remove unused bibliography-only entries. |
| F | Appendices and downloadable artifacts | Clarify illustrative/internal source status in examples, templates, CSV sample row, and workbook sample row. |

## Actions Applied

High-risk claim cleanup:

- Air Canada now says the tribunal held Air Canada liable, and identifies the
  workflow-control lesson as this book's inference rather than a tribunal
  finding.
- Boeing now frames certification-review lessons as an illustration from later
  reviews, not a definitive causal summary of the crashes.
- Tessa now says the case was not a clean generative-LLM case and is mainly a
  task-boundary case.
- Ofqual now says public confidence did not hold, rather than collapsed.
- Cruise now treats job cuts as reported broader operational fallout and uses
  ADS software units.
- MiDAS now describes automated determinations and inadequate human review
  without over-relying on the stronger phrase "without human participation."
- Deloitte now frames source verification as the lesson exposed by the episode,
  not as a formally established root cause.

Citation mechanics:

- Normalized author-date keys for NIST, NHTSA, DEWR, Federal Aviation
  Administration, California Department of Motor Vehicles, The Guardian,
  University of Michigan Science, Technology, and Public Policy Program, Board
  of Governors et al., and International Organization for Standardization.
- Removed orphan suffixes: `NHTSA 2023a` became `NHTSA 2023`; DEWR 2025
  entries became `2025a` and `2025b`.
- Removed uncited `Magesh et al. 2024` and `Goh et al. 2024` from the
  publication bibliography while retaining them in the source ledger as
  source-pool items.

Artifact honesty:

- Marked AI code-review example references as illustrative placeholders.
- Added reference-status columns or explicit source-status notes to filled
  bounded-action and draft-assistance examples.
- Added `Reference Status` and `Caveat / Limit` fields to the reviewer-packet
  template.
- Updated appendix packet guidance to require source status and caveats.
- Renamed the pilot-log CSV and workbook sample row to `SAMPLE-*` values and
  `.example.invalid` email.

## Verification

Completed checks:

- Rebuilt `.book/exports` with `.book/tools/build_exports.py`.
- Validated EPUB archive with `unzip -t`.
- Validated workbook archive with `unzip -t`.
- Parsed all pilot-kit CSV files and confirmed consistent row widths.
- Scanned manuscript, exports, ledger, downloads, and appendices for stale keys
  and phrases: `NHTSA 2023a`, `2025c`, `Federal Reserve et al.`, `FAA 2020`,
  `California DMV 2023`, `University of Michigan STPP`, `ISO 2023`, `public
  confidence collapsed`, `without human participation`, and old pilot-log sample
  identifiers.
- Confirmed no bracket source handles such as `RC1`, `A2`, or `G2` leak into
  publication-facing prose.

Residual note:

- Fast-moving 2026 vehicle-recall sources should still be rechecked immediately
  before publication, as the manuscript already says.
