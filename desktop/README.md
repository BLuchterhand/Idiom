# Idiom Desktop

Idiom Desktop is an additive Tauri shell around the existing `idiom` binary.
It is intentionally separate from the Go CLI and Bubble Tea TUI so the current
terminal workflow remains unchanged.

## Architecture

The frontend talks to an `IdiomBridge` interface in `src/core/idiom.ts`.
Adapters live under `src/adapters/`:

- `demoBridge.ts` powers the premium static experience in a normal browser.
- `tauriBridge.ts` calls Rust commands when the app runs inside Tauri.

The Rust command layer in `src-tauri/src/main.rs` shells out to the existing
`idiom` binary with explicit argument arrays. Set `IDIOM_DESKTOP_BINARY` to
override the binary path. Otherwise the app resolves `idiom` from `PATH`.

## Development

```bash
make desktop-install
make desktop-dev
```

For a distributable desktop bundle:

```bash
make desktop-build
```

The desktop app requires Node/npm and the Tauri platform prerequisites. The
repository's normal `make build`, `make test`, CLI, and TUI flows do not depend
on this workspace.
