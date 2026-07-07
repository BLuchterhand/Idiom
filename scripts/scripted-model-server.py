#!/usr/bin/env python3
"""Tiny OpenAI-compatible chat-completions server for deterministic e2e tests."""

from __future__ import annotations

import argparse
import json
import pathlib
import signal
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class ScriptedState:
    def __init__(self, script: object, request_log: pathlib.Path | None):
        if isinstance(script, list):
            self.model = "scripted-model"
            self.responses = script
        elif isinstance(script, dict):
            self.model = str(script.get("model") or "scripted-model")
            responses = script.get("responses") or []
            if not isinstance(responses, list):
                raise TypeError("script responses must be an array")
            self.responses = responses
        else:
            raise TypeError("script must be an object or an array")
        self.request_log = request_log
        self.lock = threading.Lock()
        self.index = 0

    def next_response(self, request: dict) -> dict:
        with self.lock:
            index = self.index
            self.index += 1
        self.log_request(index, request)
        if index >= len(self.responses):
            raise IndexError(f"script exhausted after {index} chat request(s)")
        raw_response = self.responses[index]
        if isinstance(raw_response, str):
            response = {"content": raw_response}
        elif isinstance(raw_response, dict):
            response = dict(raw_response)
        else:
            raise TypeError("script response must be an object or a string")
        response.setdefault("role", "assistant")
        response.setdefault("content", "")
        return response

    def log_request(self, index: int, request: dict) -> None:
        if self.request_log is None:
            return
        record = {
            "ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
            "index": index,
            "model": request.get("model", ""),
            "message_count": len(request.get("messages") or []),
            "tool_count": len(request.get("tools") or []),
            "tool_choice": request.get("tool_choice", ""),
            "response_format": request.get("response_format", {}),
        }
        self.request_log.parent.mkdir(parents=True, exist_ok=True)
        with self.request_log.open("a", encoding="utf-8") as f:
            json.dump(record, f, sort_keys=True)
            f.write("\n")


def make_handler(state: ScriptedState):
    class Handler(BaseHTTPRequestHandler):
        server_version = "idiom-scripted-model/1"

        def log_message(self, _format: str, *_args) -> None:
            return

        def do_GET(self) -> None:
            if self.path.rstrip("/") != "/v1/models":
                self.send_error(404, "not found")
                return
            self.write_json({"data": [{"id": state.model}]})

        def do_POST(self) -> None:
            if self.path.rstrip("/") != "/v1/chat/completions":
                self.send_error(404, "not found")
                return
            try:
                length = int(self.headers.get("content-length") or "0")
                body = self.rfile.read(length)
                request = json.loads(body.decode("utf-8") or "{}")
                response = state.next_response(request)
            except Exception as exc:  # deterministic diagnostics for harness failures
                self.write_json({"error": {"message": str(exc)}}, status=500)
                return

            message = {
                "role": response.get("role", "assistant"),
                "content": response.get("content", ""),
            }
            if response.get("reasoning_content"):
                message["reasoning_content"] = response["reasoning_content"]
            if response.get("tool_calls"):
                message["tool_calls"] = response["tool_calls"]
            finish_reason = response.get("finish_reason")
            if not finish_reason:
                finish_reason = "tool_calls" if response.get("tool_calls") else "stop"
            self.write_json(
                {
                    "choices": [{"message": message, "finish_reason": finish_reason}],
                    "usage": {
                        "prompt_tokens": int(response.get("prompt_tokens") or 1),
                        "completion_tokens": int(response.get("completion_tokens") or 1),
                        "total_tokens": int(response.get("total_tokens") or 2),
                    },
                }
            )

        def write_json(self, payload: dict, status: int = 200) -> None:
            data = json.dumps(payload).encode("utf-8")
            self.send_response(status)
            self.send_header("content-type", "application/json")
            self.send_header("content-length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    return Handler


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--script", required=True)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=0)
    parser.add_argument("--port-file", required=True)
    parser.add_argument("--request-log", default="")
    args = parser.parse_args(argv)

    script_path = pathlib.Path(args.script)
    with script_path.open("r", encoding="utf-8") as f:
        script = json.load(f)
    request_log = pathlib.Path(args.request_log) if args.request_log else None
    state = ScriptedState(script, request_log)
    server = ThreadingHTTPServer((args.host, args.port), make_handler(state))
    host, port = server.server_address
    port_file = pathlib.Path(args.port_file)
    port_file.parent.mkdir(parents=True, exist_ok=True)
    port_file.write_text(f"http://{host}:{port}\n", encoding="utf-8")

    stop = threading.Event()

    def shutdown(_signum, _frame):
        stop.set()
        threading.Thread(target=server.shutdown, daemon=True).start()

    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)
    try:
        server.serve_forever(poll_interval=0.2)
    finally:
        stop.set()
        server.server_close()
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
