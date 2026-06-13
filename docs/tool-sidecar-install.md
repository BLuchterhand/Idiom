# Tool Sidecar Install

Use this path when an external caller wants Idiom's repository evidence tools
without installing the local model harness, queue daemon, or chat UI workflow.

## Supported Release Assets

- Unix sidecar install is release-targeted for macOS and Linux on `amd64` and
  `arm64`.
- Windows sidecar install is release-targeted for `windows/amd64`.
- Windows full CLI install and other OS/architecture combinations are not yet
  release-proven by these installers.

## Install

macOS/Linux (`amd64` or `arm64`), consolidated installer:

```bash
curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install.sh | sh -s -- --profile tools
```

Compatibility entrypoint for existing Unix scripts:

```bash
curl -fsSL https://raw.githubusercontent.com/BLuchterhand/idiom/main/install-tools.sh | sh
```

Windows PowerShell (`windows/amd64` sidecar only):

```powershell
irm https://raw.githubusercontent.com/BLuchterhand/idiom/main/install-tools.ps1 | iex
```

The Unix compatibility wrapper routes to `install.sh --profile tools`. The
installers download the normal release archive, verify `SHA256SUMS`, and install
sidecar launchers:

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

The sidecar install does not install language discovery dependencies. Use
`idiom doctor`, `idiom doctor --profile sidecar --json`, and
`idiom tool schemas` to inspect the current dependency profile, tool manifest,
and degraded/unavailable evidence on the target machine instead of relying on a
duplicated static dependency list in this document.
