# Real Case Audit

Purpose: keep the chapter case examples aligned with the book's human-process
parallel. A case should not be decorative. It should show how uncertain output,
human judgment, institutional pressure, and workflow controls interact.

## Case Standard

A strong case should show:

- probabilistic, judgment-heavy, or proxy-based output;
- a human or organizational process that relied on it;
- a concrete consequence;
- a weak control that did not catch or contain the issue;
- a stronger implemented response, or a clearly documented absence of one;
- a clear reader value: what a working team should do differently.

In chapter prose, the case should make that sequence visible without requiring
the reader to infer it from the source ledger. The preferred shape is:

```text
Problem. Weak control. Why it failed. Consequence. Implemented response. Reader
value and chapter control lesson.
```

For positive deployment examples, the case should state that it is not an
incident and should name the reported controls that make the workflow useful.

## Current Audit

| ID | Chapter | Case | Audit Result |
| --- | --- | --- | --- |
| RC1 | 00 | Air Canada chatbot | Keep. Current AI case with reliance, a weak policy-elsewhere control, customer consequence, and organizational responsibility. |
| RC2 | 01 | Mata v. Avianca | Keep. AI-generated legal research crossed a professional filing boundary; the professional gate existed, but verification failed before filing. |
| RC3 | 02 | Boeing 737 MAX certification | Keep. Strong fit because the central lesson is reviewable process over confidence in experienced producers, and the consequence is concrete: deaths, grounding, return-to-service review, and public-trust damage. |
| RC4 | 03 | CNET AI-written articles | Keep. Strong fit because disclosure, claimed human review, warnings, and corrections still did not prevent bad AI-written work from reaching publication. |
| RC5 | 04 | NEDA Tessa chatbot | Better fit because the consequence is clearer: an eating-disorder prevention tool appeared in a support context, produced harmful weight-loss advice, and was taken offline. |
| RC6 | 05 | Ofqual 2020 grading algorithm | Keep. Strong fit because aggregate context was used as authority for individual grades, results were withdrawn, student plans were disrupted, and Ofqual's chief regulator resigned. |
| RC7 | 06 | Post Office Horizon | Keep. Non-AI, but strongly aligned with institutional reliance on computer-generated assertions. |
| RC8 | 07 | Cruise pedestrian-dragging incident | Keep. Strong fit because the effect-boundary failure is explicit: after a serious collision, the vehicle selected a pull-over maneuver instead of a stationary state. The company impact included permit suspensions, a nationwide driverless-operations pause, a 950-unit recall, and broader operational fallout. |
| RC9 | 08 | Michigan MiDAS unemployment fraud determinations | Keep. Strong fit because automated determinations were challenged through allegations of property seizure without due process, refunds and reversals, and a $20 million class settlement. |
| RC10 | 09 | Uber automated-vehicle crash | Keep. Shows why human presence is not useful review without attention, timing, and intervention support. |
| RC11 | 10 | Knight Capital trading incident | Keep. Strong fit because the consequence followed from automated market action without effective action-specific limits: automated orders reached the market, capital-threshold controls did not stop them during the event, and unwinding the resulting positions left the firm with a loss of more than $460 million. |
| RC12 | 11 | Waymo flooded-road recall | Keep as a date-bound 2026 example; recheck Waymo/NHTSA recalls before publication. Shows incident evidence changing operating scope after the prior scope failed to contain the condition. |
| RC13 | 12 | Morgan Stanley internal GPT-4 assistant | Keep with caveat. Positive deployment pattern, not an incident; useful because it reports narrow corpus, sources, feedback, and professional authority. |
| RC14 | 13 | Deloitte Australia report | Keep. Strong professional-work case with apparent AI errors, correction, disclosure, and partial refund. |
