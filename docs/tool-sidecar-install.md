# Tool Sidecar Install

Use this path when an external caller wants Idiom's repository evidence tools
without installing the local model harness, queue daemon, or chat UI workflow.

macOS and Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install-tools.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/BLuchterhand/idiom/main/install-tools.ps1 | iex
```

The installer downloads the normal release archive, verifies `SHA256SUMS`, and
installs sidecar launchers:

- `idiom-tool` / `idiom-tool.cmd` runs `idiom tool ...`
- `idiom-mcp-discovery` / `idiom-mcp-discovery.cmd` runs
  `idiom mcp serve --stdio --profile discovery ...`

It deliberately installs the binary as `idiom-tool-bin` rather than `idiom`.
That keeps the public command surface focused on externally callable tools. The
sidecar still uses the same `idiom.tool.v1` contract as `idiom tool`.

Override knobs match the release installer:

- `IDIOM_VERSION=v0.1.0`
- `IDIOM_INSTALL_DIR=/path/to/bin`
- `IDIOM_REPO=owner/fork`
- `IDIOM_RELEASE_URL_BASE=https://example.invalid/releases`

The sidecar install does not install language backends such as `gopls`,
`ctags`, `semgrep`, `joern`, or `ast-grep`. Tools that depend on optional
external CLIs report unavailable or degraded evidence until those CLIs are on
`PATH`.
