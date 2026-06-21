#!/usr/bin/env python3
"""Post-install harness smoke using installer-created global config.

This test proves more than dependency availability. It requires the installer
to have initialized ~/.idiom, temporarily points ~/.idiom/config.json at a local
OpenAI-compatible mock, runs `idiom run` without --config from a clean workspace,
and verifies that the installed binary reaches the mock model and writes a
completion record. The original global config is restored before exit.
"""

from __future__ import annotations

import json
import os
import shlex
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


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


class MockState:
    def __init__(self) -> None:
        self.requests: list[dict[str, object]] = []


def idiom_command() -> list[str]:
    value = os.environ.get("IDIOM_BIN", "idiom")
    if Path(value).exists():
        return [value]
    return shlex.split(value)


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


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


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


def start_mock_server(port: int, state: MockState) -> ThreadingHTTPServer:
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args: object) -> None:
            return

        def do_GET(self) -> None:
            state.requests.append({"method": "GET", "path": self.path})
            body = json.dumps(
                {
                    "object": "list",
                    "data": [
                        {
                            "id": "install-smoke-model",
                            "object": "model",
                            "created": 1,
                            "owned_by": "idiom-install-smoke",
                        }
                    ],
                }
            ).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_POST(self) -> None:
            length = int(self.headers.get("Content-Length", "0") or "0")
            raw = self.rfile.read(length)
            try:
                payload: object = json.loads(raw.decode("utf-8"))
            except Exception:
                payload = {"decode_error": raw.decode("utf-8", errors="replace")[:512]}
            state.requests.append({"method": "POST", "path": self.path, "payload": payload})
            body = json.dumps(
                {
                    "id": "chatcmpl-install-smoke",
                    "object": "chat.completion",
                    "created": 1,
                    "model": "install-smoke-model",
                    "choices": [
                        {
                            "index": 0,
                            "message": {
                                "role": "assistant",
                                "content": "install-smoke-ok",
                            },
                            "finish_reason": "stop",
                        }
                    ],
                    "usage": {
                        "prompt_tokens": 8,
                        "completion_tokens": 3,
                        "total_tokens": 11,
                    },
                }
            ).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


def wait_for_mock(port: int) -> None:
    deadline = time.time() + 10
    while time.time() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.5):
                return
        except OSError:
            time.sleep(0.1)
    raise SystemExit(f"mock model server did not start on 127.0.0.1:{port}")


def patch_global_config_for_mock(config_path: Path, port: int) -> str:
    original = config_path.read_text(encoding="utf-8")
    cfg = json.loads(original)
    cfg["llama_server"] = {
        "kind": "llama",
        "host": "127.0.0.1",
        "port": port,
        "model": "install-smoke-model",
        "ctx_size": 8192,
        "log_file": "logs/install-smoke-llama.log",
    }
    chat = cfg.setdefault("chat", {})
    chat["auto_start_server"] = False
    chat["request_timeout_seconds"] = 20
    chat["server_start_timeout_seconds"] = 1
    chat["max_model_calls"] = 1
    chat["max_duration_seconds"] = 20
    chat.setdefault("system_prompt_files", ["prompts/base.md"])
    chat.setdefault("profiles_dir", "prompts/profiles")
    chat.setdefault("default_profiles", ["coding"])

    route_classifier = cfg.setdefault("route_classifier", {})
    route_classifier["enabled"] = False

    workspace = cfg.setdefault("workspace", {})
    workspace["enable_shell"] = True
    workspace["enable_writes"] = True
    workspace["command_timeout_seconds"] = 30
    workspace["max_output_bytes"] = 65536
    workspace["max_file_bytes"] = 65536

    config_path.write_text(json.dumps(cfg, indent=2) + "\n", encoding="utf-8")
    return original


def main() -> None:
    prefix = idiom_command()
    if not prefix:
        raise SystemExit("IDIOM_BIN resolved to an empty command")
    if shutil.which(prefix[0]) is None and not Path(prefix[0]).exists():
        raise SystemExit(f"missing installed idiom binary: {prefix[0]}")

    run(prefix + ["version"])
    config_path = require_global_config_artifacts()
    port = free_port()
    state = MockState()
    server = start_mock_server(port, state)
    wait_for_mock(port)

    original = patch_global_config_for_mock(config_path, port)
    try:
        with tempfile.TemporaryDirectory(prefix="idiom-post-install-smoke-") as tmp:
            workspace = Path(tmp)
            (workspace / "README.md").write_text("# Idiom install smoke\n", encoding="utf-8")
            proc = run(
                prefix
                + [
                    "run",
                    "--output",
                    "json",
                    "--session",
                    "install-smoke",
                    "--timeout",
                    "20s",
                    "--prompt",
                    "Read the project context if needed, then reply exactly: install-smoke-ok",
                ],
                cwd=workspace,
            )
            try:
                payload = json.loads(proc.stdout)
            except json.JSONDecodeError as exc:
                raise SystemExit(f"idiom run did not emit JSON: {exc}\n{proc.stdout}") from exc
            answer = str(payload.get("answer", ""))
            if "install-smoke-ok" not in answer:
                raise SystemExit(f"idiom run did not return mock answer; payload={payload!r}")
            completion = workspace / ".idiom" / "runs" / "install-smoke.completion.json"
            if not completion.exists():
                raise SystemExit(f"idiom run did not write completion artifact: {completion}")

        post_paths = [str(req.get("path", "")) for req in state.requests if req.get("method") == "POST"]
        if not any(path.endswith("/v1/chat/completions") for path in post_paths):
            raise SystemExit(f"mock server did not receive chat completion POST; requests={state.requests!r}")
        print("ok: installed harness ran through global config and mock model")
    finally:
        config_path.write_text(original, encoding="utf-8")
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
