# Book Package Manifest

Purpose: identify the current local beta/proof package snapshot for handoff and
review. This manifest records package contents and hashes; git staging or any
other delivery channel is a separate handoff step.

Generated: 2026-06-24.

## Regeneration Commands

```bash
KINDLE_PREVIEWER="/path/to/Kindle Previewer 3"
python3 -B .book/tools/build_exports.py
"$KINDLE_PREVIEWER" \
  .book/exports/trust-the-process-not-the-prompt.epub \
  -convert -qualitychecks -output .book/exports/kindle-preview
cp .book/exports/kindle-preview/KPF/trust-the-process-not-the-prompt.kpf \
  .book/exports/trust-the-process-not-the-prompt.kpf
```

Kindle Previewer CSV logs are path-redacted after conversion so they can be
shared without local absolute paths.

The Kindle Previewer detail log begins with generic legend rows explaining error
and notice severities. Those legend rows are not actual conversion errors. The
authoritative conversion summary is `exports/kindle-preview/Summary_Log.csv`,
which currently reports `Success` with `0` errors and `0` quality issues; the
quality report also says no issues were found.

## Current Export Package Hashes

| File | Size | SHA-256 |
| --- | ---: | --- |
| `exports/cover.svg` | 2779 | `5522bc682a70f3c895da4a787596db3313040410cb33044f5525512b072069c3` |
| `exports/kindle-preview/KPF/trust-the-process-not-the-prompt.kpf` | 836777 | `841208d2ed2fead7afbe37ffa6904de55e283440fab5ffd082aefe9d91128b49` |
| `exports/kindle-preview/Logs/trust-the-process-not-the-prompt_QualityReport.csv` | 45 | `610492ee5268b3f8b0854223d8fda69f8bd80490cce23fba85374b4fba1e455b` |
| `exports/kindle-preview/Logs/trust-the-process-not-the-prompt_log.csv` | 322 | `6bdaa0cd5f217c1b0a058f5f884343ed83edfffe000f5a345996c7457bbbb924` |
| `exports/kindle-preview/Summary_Log.csv` | 456 | `14aee75da1fa0acd289ad77fc9b745699ad4b1e135cd3281440201ed8a64a2d9` |
| `exports/trust-the-process-not-the-prompt-cover.jpg` | 185601 | `22bae54eadc2d5f7d07069926dcc4eb73abc6bf6b4c47f02dd730657718f32d4` |
| `exports/trust-the-process-not-the-prompt.epub` | 239389 | `9b31c89f6010315b62e590cda020f1cd06d59d6729105018940cdb5a93e83a7a` |
| `exports/trust-the-process-not-the-prompt.html` | 318599 | `f12715ef2d7d59c2ac0d826a1543907728b7ea2cb806a497dd7cdea0acf81eda` |
| `exports/trust-the-process-not-the-prompt.kpf` | 836777 | `841208d2ed2fead7afbe37ffa6904de55e283440fab5ffd082aefe9d91128b49` |
| `exports/trust-the-process-not-the-prompt.md` | 281427 | `5e051355f8f2e8303f930f9eeeb5798fb7bdf6856c81d8751ee973415d4a5a62` |

## Downloadable Pilot Kit Hashes

File count: 36.

| File | Size | SHA-256 |
| --- | ---: | --- |
| `downloads/ai-workflow-pilot-kit/00-start-here.md` | 2386 | `aeec2ab54ead120fab3e79f59c73433d75de93c5721bb0ce2d2e50f3af946e88` |
| `downloads/ai-workflow-pilot-kit/01-90-minute-workshop-guide.md` | 4493 | `5b8630292d5eedead972a99bbf7db7aab91b81caedb10c7d63b5598f15c0c458` |
| `downloads/ai-workflow-pilot-kit/02-pilot-operating-guide.md` | 2936 | `2785dae46a69f35f1f646abfcf991f36a448b13669e7b22e89f4393c510ad5cb` |
| `downloads/ai-workflow-pilot-kit/03-pilot-runbook.md` | 4382 | `9f8cc27c6584eaefb4420a329ff389fa5ac0576e61e35479d276bf86c38a7424` |
| `downloads/ai-workflow-pilot-kit/04-threshold-setting-guide.md` | 3074 | `50d6718dbd4e4b0333ab93e6b41aa10efbb199f81b0f8020b9330178b1364665` |
| `downloads/ai-workflow-pilot-kit/README.md` | 5797 | `0a93b7b696460a05e3c1a6d328d6e72768beeb829fa8b91babbebb85c654fcbd` |
| `downloads/ai-workflow-pilot-kit/examples/ai-code-review-filled.md` | 6102 | `f00884da53e889400483ad1a0ac66e3b0e606d672abca891055f0e3fac6f501a` |
| `downloads/ai-workflow-pilot-kit/examples/bounded-action-filled.md` | 3545 | `a15b9e62055be078d1f012377160704473246c454af2466e7730175e4a7d7dd3` |
| `downloads/ai-workflow-pilot-kit/examples/discovery-only-source-ownership-filled.md` | 1965 | `2972f3dcaaf07f8ae76594f1d6778404c9c084d3dfc42afa58d2834a5532d331` |
| `downloads/ai-workflow-pilot-kit/examples/draft-assistance-filled.md` | 2670 | `5848786105bccc287054b21a27b1b7c6edb09ee5c0e5db0467622c12a867791a` |
| `downloads/ai-workflow-pilot-kit/examples/support-credit-filled.md` | 6456 | `596f9ae9e728395683f3ef58e468974d7892fa0d8b2aebb3e833ec3d5df2eac4` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/ai-workflow-pilot-workbook.xlsx` | 12806 | `7aecb56bf4a2c78b3e448439e6b2776efb6088435bce31ce175143ab54516302` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/authority-pace-checklist.csv` | 648 | `e9f4b39bc2cbb59ef369f8009ab73333ddf3f0fcf603b1accbee8775b1a00fe8` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/citation-honesty-ledger.csv` | 192 | `4306c4fd0988e7797afb6f82ae773fcea7e4461bb555add296bf7904f3b7472a` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/claim-to-evidence-matrix.csv` | 338 | `bdf91439a4f8ce96cfa76a3e4c868e7fe2da920b9694ddc4ea9bc7456fa88ba8` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/control-validation-loop.csv` | 272 | `b28810a0271965d12d69a1657a94915e13214aa5492dc48e5dc2036e54e81f35` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/first-workflow-design-canvas.csv` | 1250 | `c50408bdf77ead46c732ff50f5d42adbe762211ce3ab45f7546294dbec367914` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/pilot-log.csv` | 241 | `b5702b9c7afe713f7c257dea3557494b80a511a1518334b93a0f312c847af32f` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/responsibility-incentive-map.csv` | 140 | `25bb216908424cf08bf0397d7a9dc48704debe2bc2c4f060a4234c1a3142e006` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/review-capacity-scorecard-sample.csv` | 1166 | `6b6b55592b350713e039db1d6e59a181f5ad5605ba46ae1f12a165772c1f6ee5` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/review-capacity-scorecard.csv` | 1361 | `76b4242e421287685c949686d341b7c79410a9ef7a26669a324d15e937593696` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/source-freshness-map.csv` | 172 | `fff09138b11ee5bfe225e19bf57fe4819408a5fda26285a33d928846bfac37fe` |
| `downloads/ai-workflow-pilot-kit/spreadsheets/workflow-readiness-preflight.csv` | 1386 | `f428c091ac14dfa19d8e01584bea2b7af8ecb574cfb10df72fea0dca0b07f379` |
| `downloads/ai-workflow-pilot-kit/templates/authority-pace-checklist.md` | 1323 | `d5227c3a3a814761e1ca67b17ad6709a01eb75786a367de8dad1bf27c9bbc517` |
| `downloads/ai-workflow-pilot-kit/templates/citation-honesty-ledger.md` | 2330 | `5421980eca593a6521da8975c4afc1f01add838ceb8bbf30533601dd1f3cac63` |
| `downloads/ai-workflow-pilot-kit/templates/claim-to-evidence-matrix.md` | 3109 | `a1054f3e476e74d6bae0b571930d9a1979dadad9293b4dda8ddc399d255db03e` |
| `downloads/ai-workflow-pilot-kit/templates/control-validation-loop.md` | 1687 | `a2da244d424918132367b94600f49c1fdaac588c32e9485ec150ebdc72b5f3fb` |
| `downloads/ai-workflow-pilot-kit/templates/decision-acceptance-contract.md` | 2794 | `2f5baca3084b1aeda1a46edacf5ff6f47b2c5425c5cc433282054f269851a175` |
| `downloads/ai-workflow-pilot-kit/templates/effect-boundary-spec.md` | 2164 | `c7d41461aa926a43702d1a2503c1f17b3d66f79df8714063376bbf68236cac99` |
| `downloads/ai-workflow-pilot-kit/templates/first-workflow-design-canvas.md` | 1269 | `91b028d63c097b9d59c00f8a76bd15ec741bba4ea619a8fe14213e4542c5e363` |
| `downloads/ai-workflow-pilot-kit/templates/responsibility-incentive-map.md` | 1407 | `36b0c2db90791af020e20251899ca77488bfae631ae613a082b02c47ba9b33a7` |
| `downloads/ai-workflow-pilot-kit/templates/review-capacity-scorecard.md` | 1549 | `80e349d7296786054f01b28657b2309cc4d68e610a611831027b4b0b6b7bb858` |
| `downloads/ai-workflow-pilot-kit/templates/reviewer-packet.md` | 2351 | `85cfa748f814fa4576c15e69c061dd738a915878db919761024bc7a2065c03e6` |
| `downloads/ai-workflow-pilot-kit/templates/source-freshness-map.md` | 1479 | `fbd10d9e7ac79f802e6e3e62aaa9d48a19e5578cd4e430a552c2ab2252e67bd9` |
| `downloads/ai-workflow-pilot-kit/templates/vendor-tool-evaluation-checklist.md` | 2548 | `219fdd637bb1fe7e340009bb65e8d754ec0d06a2624b2926e4c5dd0b8b84caad` |
| `downloads/ai-workflow-pilot-kit/templates/workflow-readiness-preflight.md` | 2633 | `c65480e68a1f9762bb12ed801a3ebc0cc173bea60dfca9453c331ba4375e57cb` |

## Current Export Status

- Kindle Previewer conversion: success.
- Kindle Previewer errors: 0.
- Kindle Previewer quality issues: 0.
- Public launch still depends on the external gates listed in
  `external-launch-gates.md`.
