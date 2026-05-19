# Product Evidence Export

`harness product-evidence export` writes a local review milestone product-evidence report for
go/no-go review. The export is evidence-only: it reports contracts, IDs,
hashes, counts, statuses, class coverage, redaction status, limitations, and
release review linkage. It also includes a metadata-only recovery rollup from
local `.harness/recovery` artifacts so review can see open, recovered, blocked,
and malformed recovery state without loading raw prompts, transcripts, or file
bodies. It does not prescribe the caller's next action.

Generate a local export:

```bash
harness product-evidence export --project-root . --output .harness/product-evidence/export.json
```

Fold in a generated local-ops benchmark and privacy-safe validation records:

```bash
harness local-ops benchmark --json --quickstart-smoke --output .harness/local-ops/report.json
harness product-evidence export \
  --project-root . \
  --local-ops-report .harness/local-ops/report.json \
  --validation-records .harness/product-evidence/validation-records.json \
  --output .harness/product-evidence/export.json
```

Validate the export behavior:

```bash
go test ./cmd/harness -run 'Testreview milestone|TestRunProductEvidence' -count=1
```

Privacy defaults:

- No prompt bodies, chat transcript bodies, file bodies, secrets, credentials,
  private/temp absolute paths, protected workspace URLs, partner identifiers, or
  unredacted telemetry paths are included by default.
- Local paths are reduced to path IDs. Source artifacts are referenced by IDs
  and hashes.
- Retained-use and adoption signals are not inferred. They appear only when
  privacy-safe validation records use the strict allowlisted schema and qualify
  with `evidence_status:"complete"` plus redacted/pass privacy status.
- Validation-record input that includes unknown fields, raw prompt/file-body
  fields, protected paths, secrets, credentials, deployment identifiers,
  unredacted log or telemetry paths, forbidden capture flags, or absolute paths
  embedded after separators such as `repo=/...` or `path:/...` is blocked before
  records are counted.
- MCP stdio integration is represented through the review milestone integration proof
  records when the review milestone replay fixture is present and executable. The export
  includes proof IDs, counts, statuses, release review linkage, and metadata-only
  limitations, not raw protocol output or file bodies.
- Recovery evidence is represented by counts, failure classes, next actions,
  reconciliation counts, release-impact text, blockers, artifact IDs, and
  aggregate hashes. Open packets downgrade the export to partial when it would
  otherwise pass; malformed, missing, or stale recovery artifacts block the
  export.
