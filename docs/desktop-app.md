# Idiom Desktop App

Status: initial desktop surface scaffold.

Idiom Desktop is a dedicated cross-platform application shell built with Tauri
2, React, and TypeScript. It is additive: the existing `idiom` CLI and Bubble
Tea TUI remain the authoritative terminal surfaces and are not imported by the
desktop app.

## Product Shape

The desktop app should feel familiar to users of Codex and Claude Code while
making Idiom's own substrate visible:

- runs and lanes are first-class, not just chat bubbles
- repository memory, freshness, contracts, audit, and eval signals sit beside
  the conversation
- host-owned contracts remain visible without taking strategy away from the
  model
- every side effect crosses an explicit local bridge into the existing harness

## Hook Points

The frontend depends on `IdiomBridge` in `desktop/src/core/idiom.ts`.
That interface is the desktop-facing port:

- `appManifest` reports the active binary, version, and workspace
- `runCommand` invokes an allowlisted top-level `idiom` command with explicit
  argument arrays. The allowlist mirrors the current noninteractive root CLI
  namespace, including `tool`, `lane`, `recover`, `product`, `audit`, `doctor`,
  `backend`, `queue`, `brief`, `profile`, `skills`, and related workflow
  commands.
- `runTool` invokes a single sidecar-style `idiom tool` request
- `startLane` starts a lane and streams structured events back to the UI
- `subscribe` attaches UI listeners to lane and tool events

Current adapters:

- `DemoBridge` keeps the premium UI usable in a normal Vite browser session.
- `TauriBridge` uses Tauri commands backed by the Rust process adapter.

This boundary keeps future transport choices local. MCP, a local socket, a
queue daemon, or an in-process Go bridge can implement the same port without
forcing UI components to know how the harness is reached.

## Parity Surface

The first desktop pass maps the existing terminal surface into five app areas:

| Desktop area | Current hook | Terminal parity |
| --- | --- | --- |
| Runs | `startLane`, `runCommand` | `idiom lane plan-execute`, `idiom lane implement`, `idiom lane atom-spec`, `idiom doctor`, `idiom backend status`, `idiom queue status` |
| Memory | `runTool`, `runCommand` | `idiom tool context_status`, `idiom tool list`, `idiom tool schemas` |
| Audit | `runCommand` | `idiom tool harness_audit`, `idiom audit`, `idiom probe lane` |
| Recover | `runCommand` | `idiom recover`, `idiom product efforts`, `idiom product` |
| Settings | `runCommand` | `idiom version`, `idiom profile`, `idiom skills` |

The UI intentionally shows raw command output for these parity hooks. That keeps
new desktop affordances honest while the app evolves toward richer presenters.

## Process Boundary

The initial Rust adapter shells out to the existing `idiom` binary with explicit
argument arrays:

- `idiom tool <name> --input <json>`
- `idiom lane <lane> --json <prompt>`

Set `IDIOM_DESKTOP_BINARY` to point the app at a source-built or release-built
binary. If unset, the adapter resolves `idiom` from `PATH`.

## Commands

```bash
make desktop-install
make desktop-dev
make desktop-build
```

The normal repository commands remain unchanged:

```bash
make build
make test
idiom
```
