# CLI Reference

This page lists the common public CLI surfaces. Run `idiom help` or
`idiom <command> --help` for the complete flag set.

## Agent Runs

```bash
idiom run --prompt "inspect this repository" --json
```

## Lanes

```bash
idiom lane plan-execute --title fix-parser path/to/brief.md
idiom lane implement --title small-change path/to/brief.md
```

`plan-execute` separates planning from execution. The planner produces a
bounded edit plan; the executor applies accepted edits through workspace tools.

## Workspace Tools

```bash
idiom tool list
idiom tool context_pack --input '{"task":"find parser code","mode":"plan","limit":8}'
idiom tool context_status --input '{"refresh":true,"limit":20}'
```

Useful discovery tools include `context_pack`, `context_query`,
`context_status`, `dependency_graph`, `history_range`, `history_symbol`,
`blame_range`, `semgrep_scan`, and `harness_audit`.

## MCP

```bash
idiom mcp serve --stdio --profile discovery
```

## Queue

```bash
idiom queue serve
idiom queue status
idiom queue run --pool default -- idiom lane plan-execute path/to/brief.md
```

The queue is useful when several local jobs share limited backend capacity.
