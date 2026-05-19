# Tool Sidecar Install

Use this path when an external caller wants Agent Harness's repository evidence tools
without installing the local model harness, queue daemon, or chat UI workflow.

macOS and Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/owner/harness/main/install-tools.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/owner/harness/main/install-tools.ps1 | iex
```

The installer downloads the normal release archive, verifies `SHA256SUMS`, and
installs sidecar launchers:

- `harness-tool` / `harness-tool.cmd` runs `harness tool ...`
- `harness-mcp-discovery` / `harness-mcp-discovery.cmd` runs
  `harness mcp serve --stdio --profile discovery ...`

It deliberately installs the binary as `harness-tool-bin` rather than `harness`.
That keeps the public command surface focused on externally callable tools. The
sidecar still uses the same `harness.tool.v1` contract as `harness tool`.

Override knobs match the release installer:

- `HARNESS_VERSION=v0.1.0`
- `HARNESS_INSTALL_DIR=/path/to/bin`
- `HARNESS_REPO=owner/fork`
- `HARNESS_RELEASE_URL_BASE=https://example.invalid/releases`

The sidecar install does not install language backends such as `gopls`,
`ctags`, `semgrep`, `joern`, or `ast-grep`. Tools that depend on optional
external CLIs report unavailable or degraded evidence until those CLIs are on
`PATH`.
