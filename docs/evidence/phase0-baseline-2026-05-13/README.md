# Phase 0 Baseline Bundle - 2026-05-13

This directory stores the P0.2 baseline bundle for context freshness, tool
surface, behavior audit, and representative SWE suite coverage.

The commands were run from `/workspace/idiom`. Each command
has a metadata file with UTC timestamps, exit status, stdout/stderr artifact
names, and failure/blocker status.

## Command Summary

| Command | Exit | Primary artifact | Baseline summary |
| --- | ---: | --- | --- |
| `idiom tool context_status` | 0 | `01-context-status.stdout.json` | Fresh SQLite graph index; 349 files, 7024 symbols, 38448 edges; dirty worktree recorded. |
| `idiom tool harness_audit --input '{"purpose":"tool_surface"}'` | 0 | `02-harness-audit-tool-surface.stdout.json` | 129 manifest/schema/suggestion tools; 0 drift; 2 findings for unavailable Python precision helpers. |
| `idiom tool harness_audit --input '{"purpose":"behavior"}'` | 0 | `03-harness-audit-behavior.stdout.json` | 251 logs scanned; 14 findings; 11 improvement questions. Prompt/log excerpts in worst-offender rows are redacted. |
| `go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json` | 0 | `04-swe-representative-suite.stdout.json` | Representative suite coverage passed for 44 tasks; lint passed; no task logs loaded in suite-only mode. |

## Files

- `README.md`: this index and reproduction guide.
- `01-context-status.metadata.md`: command, timestamps, exit status, artifact names, and blocker status.
- `01-context-status.stdout.json`: captured `context_status` JSON output.
- `01-context-status.stderr.txt`: captured stderr, or `(no stderr)`.
- `02-harness-audit-tool-surface.metadata.md`: command, timestamps, exit status, artifact names, and blocker status.
- `02-harness-audit-tool-surface.stdout.json`: captured `harness_audit` tool-surface JSON output.
- `02-harness-audit-tool-surface.stderr.txt`: captured stderr, or `(no stderr)`.
- `03-harness-audit-behavior.metadata.md`: command, timestamps, exit status, artifact names, blocker status, and redaction note.
- `03-harness-audit-behavior.stdout.json`: captured `harness_audit` behavior JSON output with prompt/log excerpts redacted.
- `03-harness-audit-behavior.stderr.txt`: captured stderr, or `(no stderr)`.
- `04-swe-representative-suite.metadata.md`: command, timestamps, exit status, artifact names, and blocker status.
- `04-swe-representative-suite.stdout.json`: captured representative SWE suite JSON output.
- `04-swe-representative-suite.stderr.txt`: captured stderr, or `(no stderr)`.

## Reproduce

From the repository root, rerun:

```sh
idiom tool context_status
idiom tool harness_audit --input '{"purpose":"tool_surface"}'
idiom tool harness_audit --input '{"purpose":"behavior"}'
go run ./cmd/idiom-swe-eval -suite testdata/swe/representative-suite.json -suite-only -json
```

Capture stdout, stderr, UTC start/end timestamps, and exit status for each
command. If behavior audit output includes raw prompt or log excerpt fields,
redact those excerpt fields before storing the artifact; keep aggregate counts,
paths, findings, and session identifiers needed for comparison.

## Compare Later

Later Phase 0 and release-evidence tasks should compare new runs against this
bundle on these axes:

- `context_status`: `fresh`, `index_id`, `collector_version`,
  `files_indexed`, `symbols_indexed`, `edges_indexed`, storage backend status,
  collector availability, and dirty-worktree context.
- `harness_audit` tool surface: manifest/schema/suggestion tool counts, drift,
  backend availability findings, and improvement-question count.
- `harness_audit` behavior: finding IDs, severities, hit counts, logs scanned,
  capped-source warnings, stop-reason counts, top tool mix, context-cache
  freshness, and ranker-learning state.
- Representative SWE suite: `passed`, `coverage.passed`, total task count,
  family/tag/task-shape coverage, `summary.lint_passed`, and
  `summary.load_errors`.

Treat intentional drift as product evidence only when the later task explains
the change, points to the new artifact path, and preserves the raw or redacted
stdout/stderr needed to reproduce the comparison.
