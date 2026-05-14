# Product Evidence Export

`idiom product-evidence export` writes a local P7.11 product-evidence report for
go/no-go review. The export is evidence-only: it reports contracts, IDs,
hashes, counts, statuses, class coverage, redaction status, limitations, and
release-gate linkage. It does not prescribe the caller's next action.

Generate a local export:

```bash
idiom product-evidence export --project-root . --output .idiom/product-evidence/export.json
```

Fold in a generated local-ops benchmark and privacy-safe validation records:

```bash
idiom local-ops benchmark --json --quickstart-smoke --output .idiom/local-ops/report.json
idiom product-evidence export \
  --project-root . \
  --local-ops-report .idiom/local-ops/report.json \
  --validation-records .idiom/product-evidence/validation-records.json \
  --output .idiom/product-evidence/export.json
```

Validate the export behavior:

```bash
go test ./cmd/idiom -run 'TestP711|TestRunProductEvidence' -count=1
```

Privacy defaults:

- No prompt bodies, chat transcript bodies, file bodies, secrets, credentials,
  private/temp absolute paths, private repo URLs, partner identifiers, or
  unredacted telemetry paths are included by default.
- Local paths are reduced to path IDs. Source artifacts are referenced by IDs
  and hashes.
- Retained-use and adoption signals are not inferred. They appear only when
  privacy-safe validation records are supplied.
- MCP stdio integration remains a P8.6 parity item when the export sees only
  the P7.8 placeholder proof.
