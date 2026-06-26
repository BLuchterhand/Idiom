# Publication Readiness Verification

Date: 2026-06-21

Scope: `.book/chapters`, `.book/appendices`, `.book/front-back`, production
plans, source ledger, tracker, and generated exports.

Supersession note: this is a historical checkpoint. The current target range,
generated word count, and launch gates are governed by
`publication-readiness-tracker.md`.

## Build Verification

Command:

```bash
python3 .book/tools/build_exports.py
python3 -m py_compile .book/tools/build_exports.py
unzip -t .book/exports/trust-the-process-not-the-prompt.epub
wc -w .book/exports/trust-the-process-not-the-prompt.md
```

Result:

- Markdown export generated:
  `.book/exports/trust-the-process-not-the-prompt.md`
- Print-review HTML generated:
  `.book/exports/trust-the-process-not-the-prompt.html`
- EPUB generated:
  `.book/exports/trust-the-process-not-the-prompt.epub`
- Cover SVG copied:
  `.book/exports/cover.svg`
- EPUB archive integrity check passed.
- Export word count at this historical checkpoint: about 41,700 words, inside
  the then-current 38,000-42,000 field-guide target.

## Length Reconciliation

The old 65,000-word target was retired. This checkpoint used a 38,000-42,000
word target; the current tracker now governs the accepted range and generated
word count.

## Evidence Verification

The source ledger now includes promoted sources for:

- LLM capability and probabilistic grounding;
- hallucination and uncertainty;
- software verification and review-cost shape;
- customer support operating discipline;
- human review in legal and medical AI contexts;
- governance, internal control, and model-risk management;
- MiDAS court/settlement support;
- Deloitte Australia official department materials.

The source ledger still distinguishes source caveats from proof. Legal,
medical, finance, public-sector, and support sources are used to support
bounded control lessons, not universal claims about every AI workflow.

## Style And Overclaim Scan

Scan commands:

```bash
rg -n "unlock|supercharge|game-changing|seamless|future-proof|revolutionize|transform your organization|The hard part|uncomfortable part|Then things got worse|dashboard told a different story" .book/chapters .book/front-back .book/appendices .book/production-readiness-plan.md .book/amazon-production-readiness-plan.md

rg -n "always|never|must|ensure|prove|proved|caused|everyone|obvious|clearly|usually|many organizations|guarantee|guarantees" .book/chapters .book/front-back .book/appendices
```

Result:

- No sales-language or teaser-cadence hits.
- One high-risk `prove` use in Chapter 2 was softened to `show`.
- Remaining `must` uses are mostly normative contract language in templates,
  stop conditions, and review requirements.
- Remaining `usually` uses are descriptive, scoped, or used in examples rather
  than unsupported universal claims.
- Remaining `never` hits occur in control-retirement questions or support-case
  artifact wording; they do not create empirical universals.

## Production Verification

Workspace-controllable production assets now exist:

- Amazon/production plan:
  `.book/production-readiness-plan.md`
- KDP-specific production plan:
  `.book/amazon-production-readiness-plan.md`
- Beta reader brief:
  `.book/beta-reader-brief.md`
- Downloadable artifact bundle:
  `.book/downloadable-artifacts.md`
- Appendix-ready artifacts under `.book/appendices/`
- Export script:
  `.book/tools/build_exports.py`
- Generated exports under `.book/exports/`

External-only production actions remain:

- KDP upload and current UI category selection;
- KDP upload preview and device/proof inspection;
- paperback print-cost calculation after KDP layout;
- physical proof order and inspection;
- public review collection.

These cannot be completed inside the workspace. They are marked as external in
the tracker and must be completed before public launch.

## Reviewer Status

Early independent review found no final sign-off before length reconciliation,
evidence hardening, export generation, and tighter deferral discipline. Those
items have now been addressed in workspace artifacts.

Final-review blocker remediation:

- Removed internal back-matter placeholder and production-note prose from the
  export source.
- Expanded the bibliography to include cited chapter sources that were missing
  from the generated manuscript, including CNET, NEDA/Tessa, Ofqual, Cruise,
  Knight Capital, Morgan Stanley/OpenAI, and related case sources.
- Changed the landing-page row from `Done` to `External` because a durable URL
  requires external hosting.
- Changed post-launch advertising rows to `Deferred` and removed the undefined
  `Parked` tracker status.
- Rebuilt exports. The generated Markdown was about 41,700 words at this
  checkpoint and the EPUB archive integrity check passed.
- Remediated the final bibliography blocker by adding missing generated-export
  citation entries for Lifshitz and Hung 2024, FAA 2020, U.S. House Committee
  on Transportation and Infrastructure 2020, NIST 2024, Oosting 2024, and
  University of Michigan STPP 2024.

Final blocker check commands:

```bash
python3 .book/tools/build_exports.py
rg -n "This section should|Production Note|Bibliography Note|placeholder|Do not add names" .book/exports/trust-the-process-not-the-prompt.md .book/front-back || true
rg -n "\\| .* \\| (Open|Decision|In progress|Review|Parked) \\|" .book/publication-readiness-tracker.md || true
unzip -t .book/exports/trust-the-process-not-the-prompt.epub
```

Result:

- No internal back-matter placeholder hits.
- No unresolved statuses in the narrow blocker list checked at that time; the
  current publication-readiness tracker remains authoritative for beta/proof and
  public-launch gates.
- Reviewer-flagged missing bibliography entries are present in the source
  bibliography and rebuilt export.
- EPUB integrity check passes.

Post-signoff production additions:

- Added a workflow-readiness preflight appendix.
- Added a vendor/tool evaluation checklist appendix.
- Added an AI workflow antipattern appendix.
- Added a Chapter 6 section on partial authority when the reader does not
  control the whole workflow.
- Expanded Chapter 13 with a closing section on granting the next effect from
  operating evidence rather than model impressiveness alone.
