# Citation And Interpretation Quadruple Check

Date: 2026-06-21

Supersession note: this is a historical review record. Some target-length and
readiness statements below used the then-current planning standard; use
`publication-readiness-tracker.md` and `package-manifest.md` for current
beta/proof readiness status.

Purpose: record the independent citation and interpretation audit requested
after publication-readiness sign-off. The standard for this pass is stricter
than formatting readiness: cited sources must support the claims made from them,
and analogies must be labeled as analogies.

## Review Scope

Four independent GPT-backed reviewer agents audited separate source families:

- Technical, scientific, and governance sources.
- Legal, public-sector, and institutional failure cases.
- Aviation, autonomous-systems, finance, and tool-mediated-action cases.
- Media, health/support, customer operations, positive deployment examples, and
  export/bibliography consistency.

A Reasonix-specific subagent type was not available in the current workspace
tool surface, so the audit used multiple high-effort GPT reviewer agents with
adversarial, domain-separated briefs.

## Material Fixes Applied

- Chapter 1 now limits the cited Brown/Gurnee/Kalai/Farquhar claim to broad
  language work, few-shot behavior, internal representations, hallucination,
  and uncertainty. Tool use and planning-like behavior are no longer implied to
  be proved by those citations.
- OpenAI et al. 2025 was corrected to Kalai et al. 2025.
- Air Canada's case now describes the correct-policy-elsewhere argument as an
  attempted fallback, not as the control failure itself.
- Mata v. Avianca now states the notice requirement more precisely: notice to
  the plaintiff and to the judges falsely identified as authors.
- CNET wording now avoids implying every AI-written article carried disclosure
  from the start, and softens pause causality to "after errors and weeks of
  debate."
- Ofqual wording now says grades were reissued using the higher of the
  centre-assessed grade and calculated grade.
- Post Office Horizon Inquiry was added to the bibliography.
- Cruise wording now separates immediate regulatory/operational consequences
  from broader later fallout and labels the effect-boundary lesson as a
  transferable analogy.
- NHTSA 2024 now cites the consent order rather than only the press release.
- The Uber/NTSB case now explicitly says it is an automation-oversight analogy.
- MiDAS wording now uses court-supported language about determinations without
  human participation instead of relying on a broad "state acknowledged" phrase.
- Deloitte Australia wording now distinguishes citation/reference and
  court-case-summary errors, uses DEWR materials alongside AP reporting, and
  avoids implying AI caused every error or invalidated the report's findings.
- ISO/HDI support-center sources are now used only for general operational
  discipline, not as proof of the composite service-credit scenario or its
  assumed numbers.
- HDI metadata was corrected to 2018 / Support Center Standard v5.3.
- ISO 18295-1 URL was corrected to the 2017 standard page.
- NASA-hosted inspection source was corrected to Shull et al. 2009, with NASA
  NTRS as repository.
- Federal Reserve model-risk guidance was updated to the current 2026 source
  and caveated as finance/model-risk analogy, not LLM or agentic-AI proof.
- Direct report URLs were added for the House 737 MAX report and NTSB Uber
  automated-vehicle report.
- DEWR final report metadata was updated to the 2026 corrected final-report
  page, while the 2025 statement/correspondence remain the correction-event
  sources.

## Verification

Commands:

```bash
python3 .book/tools/build_exports.py
python3 -m py_compile .book/tools/build_exports.py
wc -w .book/exports/trust-the-process-not-the-prompt.md
unzip -t .book/exports/trust-the-process-not-the-prompt.epub
rg -n "OpenAI et al\\. 2025|NASA 2009|HDI 2017|Version 5\\.0|ISO 18295-1.*94393|Federal Reserve et al\\. 2011|Bristol Centre for Multilevel Modelling 2020|Australian DEWR 2025a|NHTSA Announces Consent Order|press-releases/consent-order-cruise|National Aeronautics and Space Administration\\. 2009|tool use, and planning-like behavior|affected parties and judges|using centre-assessed grades|The weak control was policy availability|the articles carried a disclosure|The state later acknowledged" .book || true
```

Result:

- Export rebuilt at 39,259 words, still inside the 38,000-42,000 target.
- EPUB archive integrity check passed.
- Stale-key and stale-claim scan returned no hits.
- Citation-to-bibliography coverage passed for 42 unique export citation keys.

## Reviewer Sign-Off

After remediation and follow-up review:

- Scope A signed off on technical, scientific, and governance sources.
- Scope B signed off on legal, public-sector, and institutional failure cases.
- Scope C signed off on aviation, autonomous-systems, finance, and
  tool-mediated-action cases.
- Scope D signed off on media, health/support, customer operations, positive
  deployment examples, and bibliography/export consistency.
