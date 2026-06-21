#!/usr/bin/env python3
"""End-to-end discovery dependency smoke for installed idiom binaries.

This script exercises every discovery tool declared in
scripts/install/manifest.json against small temporary fixture projects. It is
cross-platform so CI can run the same contract after macOS, Linux, and Windows
dependency installation.
"""

from __future__ import annotations

import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
MANIFEST_PATH = REPO_ROOT / "scripts" / "install" / "manifest.json"
GLOBAL_CONFIG_REQUIRED_FILES = [
    "config.json",
    "backends.json",
    "external_tools/README.md",
    "postflight/checks/README.md",
    "prompts/base.md",
    "prompts/lanes/README.md",
    "prompts/rules/README.md",
    "prompts/profiles/coding.md",
    "prompts/profiles/claude-qwen-worker.md",
]

COVERED_MANIFEST_TOOLS = {
    "git_status",
    "git_diff",
    "history_range",
    "history_symbol",
    "blame_range",
    "search_content",
    "structural_search",
    "ctags_symbols",
    "semgrep_scan",
    "joern_scan",
    "go_definition",
    "go_references",
    "go_calls_from",
    "go_calls_into",
    "go_implementations",
    "python_definition",
    "python_references",
    "python_diagnostics",
    "ts_definition",
    "ts_references",
    "ts_diagnostics",
    "rust_definition",
    "rust_references",
    "rust_diagnostics",
    "java_definition",
    "java_references",
    "java_diagnostics",
    "cpp_definition",
    "cpp_references",
    "cpp_calls_from",
    "cpp_calls_into",
    "dependency_graph",
}


def run(args: list[str], cwd: Path | None = None, check: bool = True) -> subprocess.CompletedProcess[str]:
    proc = subprocess.run(
        args,
        cwd=str(cwd) if cwd else None,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if check and proc.returncode != 0:
        raise SystemExit(
            f"command failed ({proc.returncode}): {' '.join(args)}\n"
            f"--- stdout ---\n{proc.stdout}\n--- stderr ---\n{proc.stderr}"
        )
    return proc


def add_path_entry(path: Path | str) -> None:
    value = str(path)
    if not value or not Path(value).exists():
        return
    parts = [part for part in os.environ.get("PATH", "").split(os.pathsep) if part]
    if value not in parts:
        os.environ["PATH"] = value + os.pathsep + os.environ.get("PATH", "")


def refresh_installer_path() -> None:
    home = Path.home()
    add_path_entry(home / ".local" / "bin")
    add_path_entry(home / ".cargo" / "bin")
    add_path_entry(home / "go" / "bin")
    for version in ("3.12", "3.11", "3.10"):
        add_path_entry(home / "Library" / "Python" / version / "bin")

    if os.name == "nt":
        for raw in (
            os.environ.get("ProgramData", r"C:\ProgramData") + r"\chocolatey\bin",
            os.environ.get("ProgramFiles", r"C:\Program Files") + r"\Git\cmd",
            os.environ.get("ProgramFiles", r"C:\Program Files") + r"\Git\bin",
            os.environ.get("ProgramFiles", r"C:\Program Files") + r"\nodejs",
            os.environ.get("ProgramFiles", r"C:\Program Files") + r"\Go\bin",
            os.environ.get("APPDATA", "") + r"\npm",
            os.environ.get("ProgramFiles", r"C:\Program Files") + r"\LLVM\bin",
        ):
            add_path_entry(raw)

    python = shutil.which("python3") or shutil.which("python")
    if python:
        proc = run([python, "-c", "import site; print(site.USER_BASE)"], check=False)
        if proc.returncode == 0 and proc.stdout.strip():
            scripts_dir = "Scripts" if os.name == "nt" else "bin"
            add_path_entry(Path(proc.stdout.strip()) / scripts_dir)

    go = shutil.which("go")
    if go:
        proc = run([go, "env", "GOPATH"], check=False)
        if proc.returncode == 0 and proc.stdout.strip():
            add_path_entry(Path(proc.stdout.strip()) / "bin")


def require_command(name: str) -> None:
    if shutil.which(name) is None:
        raise SystemExit(f"missing required command: {name}")


def require_any(label: str, names: list[str]) -> None:
    if not any(shutil.which(name) for name in names):
        raise SystemExit(f"missing required command group: {label} ({', '.join(names)})")


def require_java_17() -> None:
    require_command("java")
    proc = run(["java", "-version"], check=False)
    text = proc.stderr + proc.stdout
    match = re.search(r'version "([^"]+)"', text)
    version = match.group(1) if match else ""
    major = version.split(".", 1)[0]
    if major == "1" and "." in version:
        major = version.split(".")[1]
    if not major.isdigit() or int(major) < 17:
        raise SystemExit(f"java must be 17+, found {version or 'unknown'}")


def require_manifest_commands() -> None:
    for name in ["git", "rg", "ctags", "semgrep", "joern-scan", "go", "gopls", "pyright-langserver", "node", "npm", "typescript-language-server", "tsc", "cargo", "rust-analyzer", "jdtls", "clangd", "madge", "pydeps"]:
        require_command(name)
    require_any("ast_grep", ["ast-grep", "sg"])
    require_java_17()

    ctags = run(["ctags", "--version"])
    if "universal ctags" not in (ctags.stdout + ctags.stderr).lower():
        raise SystemExit("ctags is present but is not Universal Ctags")


def require_global_config_artifacts() -> Path:
    global_dir = Path.home() / ".idiom"
    missing = [rel for rel in GLOBAL_CONFIG_REQUIRED_FILES if not (global_dir / rel).exists()]
    if missing:
        raise SystemExit(
            "missing installer-initialized global config artifact(s) under "
            f"{global_dir}: " + ", ".join(missing)
        )
    print(f"ok: global config artifacts exist under {global_dir}")
    return global_dir / "config.json"


def assert_manifest_coverage() -> None:
    manifest = json.loads(MANIFEST_PATH.read_text(encoding="utf-8"))
    expected = {
        tool
        for dep in manifest["dependencies"]
        for tool in dep.get("tools", [])
    }
    missing = sorted(expected - COVERED_MANIFEST_TOOLS)
    extra = sorted(COVERED_MANIFEST_TOOLS - expected)
    if missing:
        raise SystemExit("discovery E2E does not exercise manifest tool(s): " + ", ".join(missing))
    if extra:
        raise SystemExit("discovery E2E lists non-manifest tool(s): " + ", ".join(extra))
    print("ok: discovery e2e manifest tool list is complete")


def write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def seed_workspace(workspace: Path) -> None:
    write(workspace / "go.mod", "module example.com/discover\n\ngo 1.22\n")
    write(
        workspace / "goapp" / "pkg" / "target.go",
        """package pkg

type GoRunner interface {
\tRun() string
}

type GoImpl struct{}

func (GoImpl) Run() string {
\treturn GoTarget()
}

func GoTarget() string {
\treturn goHelper()
}

func goHelper() string {
\treturn "go"
}

func UseGoTarget() string {
\treturn GoTarget()
}
""",
    )

    write(
        workspace / "pyapp" / "pkg" / "app.py",
        """from pkg.helper import py_helper

def PyTarget() -> str:
    return py_helper()

def UsePython() -> str:
    return PyTarget()
""",
    )
    write(workspace / "pyapp" / "pkg" / "__init__.py", "")
    write(workspace / "pyapp" / "pkg" / "helper.py", "def py_helper() -> str:\n    return \"python\"\n")

    write(workspace / "tsapp" / "package.json", '{"name":"idiom-discovery-smoke","version":"0.0.0","type":"module","dependencies":{},"devDependencies":{}}\n')
    write(workspace / "tsapp" / "tsconfig.json", '{"compilerOptions":{"target":"ES2020","module":"ESNext","moduleResolution":"Node","strict":true},"include":["src/**/*.ts"]}\n')
    write(
        workspace / "tsapp" / "src" / "index.ts",
        """import { tsHelper } from "./lib";

export function TsTarget(): string {
  return tsHelper();
}

export const tsValue = TsTarget();
""",
    )
    write(workspace / "tsapp" / "src" / "lib.ts", 'export function tsHelper(): string {\n  return "typescript";\n}\n')

    write(workspace / "rustapp" / "Cargo.toml", '[package]\nname = "idiom_discovery_smoke"\nversion = "0.0.0"\nedition = "2021"\n')
    write(
        workspace / "rustapp" / "src" / "lib.rs",
        """pub fn rust_target() -> &'static str {
    rust_helper()
}

pub fn use_rust() -> &'static str {
    rust_target()
}

fn rust_helper() -> &'static str {
    "rust"
}
""",
    )

    write(
        workspace / "javaapp" / "src" / "main" / "java" / "com" / "example" / "App.java",
        """package com.example;

public class App {
    public static String JavaTarget() {
        return JavaHelper.value();
    }

    public static String useJava() {
        return JavaTarget();
    }
}
""",
    )
    write(
        workspace / "javaapp" / "src" / "main" / "java" / "com" / "example" / "JavaHelper.java",
        """package com.example;

public class JavaHelper {
    public static String value() {
        return "java";
    }
}
""",
    )

    cppapp = workspace / "cppapp"
    compile_commands = [{
        "directory": str(cppapp),
        "command": "clang++ -std=c++17 -c src/main.cpp",
        "file": "src/main.cpp",
    }]
    write(cppapp / "compile_commands.json", json.dumps(compile_commands, indent=2))
    write(
        cppapp / "src" / "main.cpp",
        """int cpp_helper() {
  return 41;
}

int CppTarget() {
  return cpp_helper() + 1;
}

int use_cpp() {
  return CppTarget();
}
""",
    )

    run(["git", "init", "-q"], cwd=workspace)
    run(["git", "config", "user.email", "ci@example.invalid"], cwd=workspace)
    run(["git", "config", "user.name", "CI Discovery Smoke"], cwd=workspace)
    run(["git", "add", "."], cwd=workspace)
    run(["git", "commit", "-q", "-m", "seed discovery smoke workspace"], cwd=workspace)
    with (workspace / "goapp" / "pkg" / "target.go").open("a", encoding="utf-8") as f:
        f.write("\n// dirty smoke line\n")


def command_prefix() -> list[str]:
    idiom_bin = os.environ.get("IDIOM_BIN", "idiom")
    tool_bin = os.environ.get("IDIOM_TOOL_BIN", "")
    if tool_bin:
        return [tool_bin]
    if shutil.which("idiom-tool"):
        return ["idiom-tool"]
    return [idiom_bin, "tool"]


def tool_json(prefix: list[str], workspace: Path, config_path: Path, tool: str, request: dict[str, object]) -> str:
    print(f"[tool] {tool} {json.dumps(request, sort_keys=True)}", file=sys.stderr)
    args = prefix + [tool, "--profile", "discovery", "-config", str(config_path), "--input", json.dumps(request)]
    proc = run(args, cwd=workspace)
    try:
        payload = json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        raise SystemExit(f"{tool}: failed to decode JSON output: {exc}\n{proc.stdout}") from exc
    if payload.get("Tool") != tool:
        raise SystemExit(f"{tool}: unexpected Tool field {payload.get('Tool')!r}")
    if payload.get("Error"):
        raise SystemExit(f"{tool}: Error field was set: {payload['Error']}\nOutput:\n{payload.get('Output', '')}")
    sidecar = payload.get("sidecar")
    if isinstance(sidecar, dict) and sidecar.get("error"):
        raise SystemExit(f"{tool}: sidecar error was set: {sidecar['error']}")
    return str(payload.get("Output", ""))


def tool_contains(prefix: list[str], workspace: Path, config_path: Path, tool: str, request: dict[str, object], expected: str) -> None:
    output = tool_json(prefix, workspace, config_path, tool, request)
    if expected not in output:
        raise SystemExit(f"{tool} output did not contain expected text: {expected}\n--- output ---\n{output}")
    print(f"ok: {tool} contains {expected}")


def tool_nonempty(prefix: list[str], workspace: Path, config_path: Path, tool: str, request: dict[str, object]) -> None:
    output = tool_json(prefix, workspace, config_path, tool, request)
    if not output.strip():
        raise SystemExit(f"{tool} output was empty for input: {request}")
    print(f"ok: {tool} returned output")


def tool_runs(prefix: list[str], workspace: Path, config_path: Path, tool: str, request: dict[str, object]) -> None:
    tool_json(prefix, workspace, config_path, tool, request)
    print(f"ok: {tool} ran")


def main() -> None:
    assert_manifest_coverage()
    refresh_installer_path()
    config_path = require_global_config_artifacts()
    require_manifest_commands()

    idiom_bin = os.environ.get("IDIOM_BIN", "idiom")
    run([idiom_bin, "version"])
    prefix = command_prefix()
    run(prefix + ["list", "--profile", "discovery"])

    with tempfile.TemporaryDirectory(prefix="idiom-discovery-smoke-") as tmp:
        workspace = Path(tmp) / "workspace"
        workspace.mkdir(parents=True)
        seed_workspace(workspace)

        c = lambda tool, req, expected: tool_contains(prefix, workspace, config_path, tool, req, expected)
        n = lambda tool, req: tool_nonempty(prefix, workspace, config_path, tool, req)
        r = lambda tool, req: tool_runs(prefix, workspace, config_path, tool, req)

        c("search_content", {"path": ".", "pattern": "GoTarget"}, "GoTarget")
        c("structural_search", {"path": "goapp", "language": "go", "pattern": "func $NAME() string { $$$BODY }"}, "GoTarget")
        c("ctags_symbols", {"path": ".", "pattern": "GoTarget"}, "GoTarget")

        c("git_status", {}, "target.go")
        c("git_diff", {"path": "goapp/pkg/target.go"}, "dirty smoke line")
        c("git_diff", {}, "dirty smoke line")

        c("go_find_symbol", {"path": "goapp", "pattern": "GoTarget"}, "GoTarget")
        c("go_definition", {"path": "goapp", "pattern": "GoTarget"}, "GoTarget")
        c("go_references", {"path": "goapp", "pattern": "GoTarget"}, "UseGoTarget")
        c("go_calls_from", {"path": "goapp", "pattern": "GoTarget"}, "goHelper")
        c("go_calls_into", {"path": "goapp", "pattern": "goHelper"}, "GoTarget")
        c("go_implementations", {"path": "goapp", "pattern": "GoRunner"}, "GoImpl")

        c("python_find_symbol", {"path": "pyapp", "pattern": "PyTarget"}, "PyTarget")
        c("python_definition", {"path": "pyapp", "pattern": "PyTarget"}, "PyTarget")
        c("python_references", {"path": "pyapp", "pattern": "PyTarget"}, "UsePython")
        n("python_diagnostics", {"path": "pyapp"})

        c("ts_find_symbol", {"path": "tsapp", "pattern": "TsTarget"}, "TsTarget")
        c("ts_definition", {"path": "tsapp", "pattern": "TsTarget"}, "TsTarget")
        c("ts_references", {"path": "tsapp", "pattern": "TsTarget"}, "tsValue")
        n("ts_diagnostics", {"path": "tsapp"})

        c("rust_find_symbol", {"path": "rustapp", "pattern": "rust_target"}, "rust_target")
        c("rust_definition", {"path": "rustapp", "pattern": "rust_target"}, "rust_target")
        c("rust_references", {"path": "rustapp", "pattern": "rust_target"}, "use_rust")
        n("rust_diagnostics", {"path": "rustapp"})

        c("java_find_symbol", {"path": "javaapp", "pattern": "JavaTarget"}, "JavaTarget")
        c("java_definition", {"path": "javaapp", "pattern": "JavaTarget"}, "JavaTarget")
        c("java_references", {"path": "javaapp", "pattern": "JavaTarget"}, "useJava")
        n("java_diagnostics", {"path": "javaapp"})

        c("cpp_find_symbol", {"path": "cppapp", "pattern": "CppTarget"}, "CppTarget")
        c("cpp_definition", {"path": "cppapp", "pattern": "CppTarget"}, "CppTarget")
        c("cpp_references", {"path": "cppapp", "pattern": "CppTarget"}, "use_cpp")
        c("cpp_calls_from", {"path": "cppapp", "pattern": "CppTarget"}, "cpp_helper")
        c("cpp_calls_into", {"path": "cppapp", "pattern": "cpp_helper"}, "CppTarget")

        c("dependency_graph", {"path": "goapp", "language": "go"}, "example.com/discover")
        c("dependency_graph", {"path": "pyapp", "language": "python"}, "pkg.helper")
        c("dependency_graph", {"path": "tsapp", "language": "typescript"}, "./lib")
        c("dependency_graph", {"path": "rustapp", "language": "rust"}, "idiom_discovery_smoke")

        c("history_range", {"path": "goapp/pkg/target.go", "start_line": 3, "end_line": 5}, "seed discovery smoke workspace")
        c("history_symbol", {"path": "goapp/pkg/target.go", "pattern": "GoTarget"}, "seed discovery smoke workspace")
        c("blame_range", {"path": "goapp/pkg/target.go", "start_line": 3, "end_line": 3}, "GoTarget")

        r("semgrep_scan", {"path": "goapp", "pattern": "auto"})
        r("joern_scan", {"path": "goapp"})

        r("context_index", {"path": ".", "language": "go", "mode": "standard"})
        c("context_status", {"refresh": True, "limit": 20}, "fresh_for_scope")
        c("context_query", {"query": "GoTarget", "resolution": "symbol"}, "GoTarget")

    print(f"ok: discovery tool e2e smoke covered all manifest dependencies on {platform.platform()}")


if __name__ == "__main__":
    main()
