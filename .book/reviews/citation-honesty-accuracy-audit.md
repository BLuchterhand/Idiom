# Citation, Honesty, And Accuracy Audit

Date: 2026-06-21

Supersession note: this is a historical review record. The
publication-readiness fixes from this audit have been applied, but current
public-launch citation readiness is governed by `publication-readiness-tracker.md`
and `package-manifest.md`, not by the older status language below. Chapter prose
now uses author-date citations instead of case-ID placeholders; `source-ledger.md`
carries source type, citation key, reviewed date, verification method, supported
claim, and caveat fields; the Chapter 3, 5, 7, 12, and 13 wording risks have
been softened in the manuscript. The findings below are retained as the evidence
trail for the remediation.

Scope: `.book/chapters/*.md`, `.book/source-ledger.md`,
`.book/research-sources.md`, `.book/source-gap-backlog.md`,
`.book/real-case-audit.md`, `.book/real-case-map.md`, and existing review
notes.

This pass audits whether the current draft gives proper citation support and
represents sources honestly. It does not convert the manuscript to a final
publisher citation style and does not certify every source body for publication.

## Audit Standard

- A cited case should support the control lesson assigned to it.
- The prose should not state causality beyond what the source supports.
- Composite support-credit material must remain labeled as composite or
  illustrative, not a documented real deployment.
- Fast-moving, legal, regulatory, government, and company-reported sources
  should be checked against primary or official pages before final use.
- Author-date citations in chapter prose must have a matching row in
  `source-ledger.md`.
- A source-pool entry in `research-sources.md` is not citation-ready unless it
  has been promoted into `source-ledger.md`.

## Methods

- Counted all chapter source IDs and compared them with `source-ledger.md`.
- Scanned all chapter drafts for author-date citations, source gaps, TODO-like
  language, overclaiming terms, causality language, and universals.
- Checked current HTTP behavior for the 43 unique URLs in `source-ledger.md`
  with bounded `curl -L -I` requests.
- Spot-checked high-risk and recent claims against live primary or near-primary
  pages where available: NHTSA Waymo recall 26E026, AP Deloitte Australia
  report, Morgan Stanley/OpenAI materials, The Verge CNET report, Michigan AG
  MiDAS settlement, NHTSA Cruise consent-order page, SEC Knight Capital page,
  and related official pages.

## Summary Judgment

The manuscript is citation-aware and mostly honest about evidence limits. After
the remediation pass, the chapter prose is publication-ready at the draft
manuscript level, subject to any publisher-specific conversion from working
Chicago author-date citations to final notes or bibliography style.

Strong points:

- Every previous chapter real-case marker has a matching ledger row.
- No chapter uses a source ID that is missing from `source-ledger.md`.
- The main support-credit storyline is repeatedly labeled as composite in the
  places where that matters most.
- Chapter 5 and Chapter 11 use author-date citations for the broader academic
  and governance claims, and those citations match promoted ledger rows.
- The ledger caveats are generally well aligned with the book's claim limits.

Publication risks:

- Chapter prose now uses author-date citations. The final production step is to
  convert the working Chicago author-date style into the publisher's preferred
  style.
- The real-case ledger now names source type, verification method, and caveat.
  A few web targets may still need browser/manual or archival fallback because
  command-line access returns 403 or timeout behavior.
- The case callouts have been softened where source support is sequence,
  contribution, or reported consequence rather than one clean cause.
- Link reachability is mixed. Most URLs respond, but several returned 403 or
  timeout behavior to command-line checks and should be manually verified or
  backed by an archived/source-stable citation before publication.

## Citation Coverage

Chapter source IDs found:

| Chapter | Source IDs |
| --- | --- |
| 00 | RC1 |
| 01 | RC2 |
| 02 | RC3 |
| 03 | RC4 |
| 04 | RC5 |
| 05 | RC6 |
| 06 | RC7 |
| 07 | RC8 |
| 08 | RC9 |
| 09 | RC10 |
| 10 | RC11 |
| 11 | RC12 |
| 12 | RC13 |
| 13 | RC14 |

Ledger IDs now kept out of chapter prose:

- A2, A3, A6, A7, A8, A9, G1, G2, G3.

This is acceptable because Chapters 5 and 11 cite those sources in author-date
form instead. Before final production, choose one citation system and convert
all case IDs and author-date citations consistently.

## URL Reachability

Command-line status check:

- Returned `200` or resolved successfully: ACL Anthology, AP Deloitte, AP
  Cruise, arXiv sources, Bridge Michigan, House Boeing investigation page,
  Futurism, NHTSA PDFs, BBC, Bristol, California DMV, FAA pages/PDF, GOV.UK,
  ISO overview, Judiciary PDF, Michigan AG, Morgan Stanley, NIST pages, NTSB,
  Post Office Horizon Inquiry, Guardian, Verge, WHRO, Wired.
- Returned `403` to the command-line check but may still be reachable in a
  browser or by manual access: UK Parliament committee PDF/page, ACM DOI
  targets, Justia Mata page, OpenAI Morgan Stanley page, University of Michigan
  STPP PDF, ABA Business Law Today page, NHTSA Cruise press release, SEC press
  release/PDF.
- Timed out or returned `000` in the command-line check: Washington Post Tessa
  article.

Action: keep the sources, but add a manual verification field or archived
stable link for every 403/timeout source before final bibliography. Paywall or
bot protection is not the same as a bad source, but the book should not depend
on a link that the production workflow cannot re-check.

## Chapter-Level Findings

### Chapter 00, Air Canada / Moffatt

Fit: strong.

Control lesson: customer-facing AI output can become an organizational
representation unless the workflow controls policy source, uncertainty routing,
and acceptance.

Representation: mostly fair. The chapter says the tribunal ordered
compensation and that the chatbot answer became the represented position. That
matches the case summary, but the ledger currently cites an ABA article rather
than the tribunal decision itself.

Source need: add the primary British Columbia Civil Resolution Tribunal
decision or a stable official case link. Keep the caveat that this is a
tribunal decision, not a broad appellate rule.

### Chapter 01, Mata v. Avianca

Fit: strong.

Control lesson: generated legal research is a proposal until checked against
authoritative legal records.

Representation: fair. The chapter does not overgeneralize the sanctions duty
outside legal/professional work.

Source need: the Justia page returned 403 to command-line verification. Add a
second stable court docket, PDF, or legal archive citation for the sanctions
order.

### Chapter 02, Boeing 737 MAX

Fit: usable with caveat.

Control lesson: consequential work needs reviewable process, independent
authority, gates, and failure states.

Representation: the chapter is careful that this is a human-process parallel,
not an AI case. The current prose should avoid compressing the entire MAX
history into "confidence in experienced institutions" as the single failure.

Source need: add a source-specific note for which claims come from FAA, JATR,
and House investigation respectively. Use the case for review visibility,
certification process, and consequence; do not use it for model behavior.

### Chapter 03, CNET AI-Written Articles

Fit: strong.

Control lesson: disclosure, human-review language, warning labels, and
corrections do not substitute for a publishing path that can reject unsupported
work before readers get it.

Representation: supported by The Verge/Futurism reporting. The statement that
CNET corrected 41 of 77 AI-written stories is live-source supported. "Public
credibility damage" is plausible but should be framed as public criticism and
credibility risk unless a source directly measures reputation damage.

Source need: add CNET's own editor note or correction notice if available, so
the case is not entirely journalism-on-journalism.

### Chapter 04, NEDA Tessa

Fit: usable with caveat.

Control lesson: health-adjacent support must separate prevention, education,
triage, crisis routing, and clinical advice before AI enters the path.

Representation: the chapter avoids saying Tessa definitely replaced the
helpline, which is important because sources differ on that framing. It says
the tool was suspended after reports of harmful advice, which is supported.

Source need: keep at least one source that quotes or describes NEDA's own
position and one independent report describing the harmful advice. The
Washington Post link timed out in command-line verification, so do not make it
the only source for the factual core.

### Chapter 05, Ofqual 2020

Fit: strong.

Control lesson: aggregate context can calibrate a system while still being weak
authority for an individual outcome.

Representation: mostly fair. The chapter appropriately avoids saying every
grade was wrong. The "direct" consequence sentence is readable but should be
slightly softened: students' plans were disrupted and the process reversed, but
the source chain should distinguish documented downgrades, public confidence,
policy withdrawal, and chief-regulator resignation.

Source need: add a short ledger split by claim: Ofqual interim report for model
design and inputs; Commons committee for criticism and process; BBC or official
announcement for resignation; GOV.UK/Ofqual for withdrawal/reissue details.

### Chapter 06, Post Office Horizon

Fit: strong.

Control lesson: accountability without control over evidence, systems,
disclosure, and challenge rights becomes blame.

Representation: fair as a non-AI evidence/control case. It should not imply the
entire scandal is exhausted by software reliability; the legal and institutional
story includes disclosure, investigation, prosecution, organizational pressure,
and remediation.

Source need: the High Court judgment is strong. Keep the Inquiry homepage as
context, but cite the judgment or inquiry evidence for specific claims.

### Chapter 07, Cruise Pedestrian-Dragging Incident

Fit: strong with one causality caution.

Control lesson: physical-effect systems need states where action authority is
removed, not merely reduced.

Representation: the NHTSA recall fact pattern supports the collision
classification and pull-over maneuver. Permit suspension, operations pause, and
recall are well supported. The later 900-plus job cuts are a reported company
consequence, but the prose should not imply they were caused solely by the
software defect or by the single incident.

Source need: keep NHTSA recall and California DMV as primary anchors. Use AP or
Guardian for layoffs, explicitly as reported subsequent business impact.

### Chapter 08, Michigan MiDAS

Fit: strong.

Control lesson: automated determinations are not accepted verified output
without evidence, review, due process, reversals, refunds, and stop conditions.

Representation: mostly fair. The Michigan AG source supports the $20 million
settlement and false-accusation allegations. Be careful with "property seizure"
language: keep it tied to the settlement allegations unless quoting a court or
settlement document.

Source need: add the court settlement/final order if available, or keep the
state AG press release plus Bridge Michigan/University of Michigan explainer
with explicit caveats.

### Chapter 09, Uber Automated-Vehicle Crash

Fit: strong with domain caveat.

Control lesson: human presence is not oversight without attention,
information, authority, timing, interface support, and safety culture.

Representation: fair as an autonomous-vehicle oversight case, not an LLM case.

Source need: NTSB report is the correct primary anchor. Add a short ledger note
for which chapter statements come from NTSB findings: automation complacency,
oversight, risk assessment, and intervention conditions.

### Chapter 10, Knight Capital

Fit: strong.

Control lesson: tool-mediated market action needs action-specific limits,
automated stops, capital thresholds, incident authority, and disconnect
conditions.

Representation: fair. SEC source supports market-access rule violations and
the trading incident. The commonly cited loss amount and time window should be
anchored to the SEC order or a second source if used in final prose.

Source need: SEC pages returned 403 to command-line checks. Add an accessible
archived SEC order or a second stable copy for production verification.

### Chapter 11, Waymo Flooded-Road Recall

Fit: strong.

Control lesson: incident evidence should change operating scope, maps,
controls, or release conditions.

Representation: supported by NHTSA recall 26E026. The official recall
acknowledgment says the software may allow vehicles to slow and then drive into
standing water on higher-speed roadways; interim remedy included increased
weather-related constraints and map updates; affected vehicles received interim
updates by April 20, 2026.

Source need: the ledger points to the Part 573 report. Also add the NHTSA
acknowledgment PDF because it clearly supports the chapter's operating-scope
and interim-remedy language.

### Chapter 12, Morgan Stanley / OpenAI

Fit: usable with caveat.

Control lesson: narrow corpus, source links, internal use, feedback, and
professional authority can form a positive deployment pattern.

Representation: honest. The chapter explicitly says the case is
company-reported and not independent proof of outcomes. That caveat should stay.

Source need: cite Morgan Stanley's 2023 press release for internal capability,
MSWM content, source-document links, feedback, and financial-advisor centrality.
Use OpenAI's case page only for OpenAI/Morgan Stanley framing and eval claims,
and keep it labeled as vendor/company-reported.

### Chapter 13, Deloitte Australia

Fit: strong.

Control lesson: professional review does not make probabilistic output
authoritative without claim, source, and citation verification.

Representation: fair with one wording caution. AP supports partial refund,
incorrect references, nonexistent academic papers, fabricated court quote, and
the department saying Deloitte confirmed some footnotes and references were
incorrect. AP also says Deloitte did not respond when asked if the errors were
generated by AI.

Source need: keep the current wording "apparent AI-generated errors" and avoid
stating that AI caused every error unless using a source that directly supports
that. Add the revised report or department statement if available.

## Cross-Cutting Findings

### 1. Citation System Was Draft-Internal

Remediation: chapter prose now uses author-date citations for case callouts and
research/governance sources. `RC*` handles remain only in internal ledgers and
maps.

Remaining action: convert the working author-date citations to the publisher's
chosen final style if needed.

### 2. Primary Sources Should Replace Several Secondary Anchors

The book's evidence standard says to prefer primary sources, regulator records,
court records, academic papers, and company filings. Current ledger quality is
mixed.

Highest-priority replacements or additions:

- RC1: primary BCCRT decision for Moffatt v. Air Canada.
- RC3: claim-by-claim split across FAA, JATR, and House report.
- RC4: CNET's own correction/editor note if available.
- RC5: NEDA/Cass position source plus independent reporting.
- RC8: NHTSA recall and California DMV as anchors; layoffs only from reporting.
- RC9: settlement/court documents for due process/property-seizure language.
- RC10: NTSB report findings by claim.
- RC11: stable SEC order copy.
- RC14: department statement or revised report, not only AP.

### 3. Composite Scenario Is Properly Labeled

Most chapters are explicit that the support-credit workflow is composite.
Chapter 12 previously used wording that read like a real team's experience
rather than a composite worked example.

Remediation: Chapter 12 now says "uses a recognizable support exception."

### 4. Some General Organizational Claims Are Sensible But Unsourced

The manuscript often uses "often," "usually," and "many organizations" for
professional pattern claims. Many are acceptable practitioner observations, but
the highest-impact ones should either be sourced or reframed as design
heuristics.

Examples to consider:

- Chapter 00: "AI adoption often inherits an old organizational flaw."
- Chapter 04: "The first good uses of AI in a workflow are usually..."
- Chapter 06: "Organizations often assign ownership faster than they assign
  control."
- Chapter 09: "Late-stage compliance review often becomes contentious..."
- Chapter 11: "Standards and risk-management guidance increasingly treat..."

Action: keep practitioner-observation language where it is clearly advisory;
cite or soften when it sounds empirical.

### 5. Causality Is Mostly Careful, But A Few Sentences Need Softer Links

No obvious fabricated causal claim appears in the chapters, but these should be
reviewed before final:

- Chapter 03: the CNET consequence wording now names public criticism and
  credibility risk rather than measured reputation damage.
- Chapter 05: the Ofqual consequence wording now distinguishes the records
  supporting disruption, process reversal, and resignation.
- Chapter 07: Cruise layoffs now appear as broader operational fallout rather
  than an implied direct result of the recall alone.
- Chapter 13: Deloitte should retain "apparent AI-generated errors" and avoid
  a stronger AI-causation claim.

### 6. Research Source Ledger Is Narrow

Promoted research/governance rows currently cover hallucination/factuality,
retrieval, model cards, datasheets, FactSheets, NIST AI RMF, NIST GenAI
Profile, and ISO/IEC 42001 overview. That supports Chapters 5 and 11, but does
not yet support all backlog claims around verification cost, review bottlenecks,
customer support operations, human-review failure in modern GenAI contexts, or
organizational incentives.

Action: do not add stronger empirical claims in those areas until the backlog
sources are promoted into `source-ledger.md`.

## Recommended Repair Order

1. Done: converted Chapter 12's real-team-sounding phrasing.
2. Done where available: added primary or official anchors for RC1, RC2, RC10,
   RC11, and RC12; retained journalism where it is the best public source for
   current public consequence or disclosure facts.
3. Done: expanded ledger caveats and verification methods for claim support.
4. Done: converted case-ID markers in chapter prose to author-date citations.
5. Done: added source type and verification method columns to
   `source-ledger.md`; use the caveat field for archive/manual fallback notes.
6. Still policy: keep `research-sources.md` as a source pool only and promote
   new sources to `source-ledger.md` before citing them in chapter prose.

## Overall Status

Draft citation integrity: good.

Publication citation readiness: ready at manuscript-draft level; final
publisher style conversion may still be required.

Honesty of representation: generally strong, with targeted softening needed
around consequence and causality in Chapters 3, 5, 7, 12, and 13.

Primary-source readiness: uneven. The core case map is sound, but final
publication should not rely on command-line-blocked links, journalism-only
anchors, or source-pool entries that have not been promoted into the ledger.
