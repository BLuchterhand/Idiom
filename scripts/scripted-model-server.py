#!/usr/bin/env python3
"""Tiny OpenAI-compatible chat-completions server for deterministic e2e tests.

The optional request log is deliberately fact-only: it records no prompts,
messages, response schemas, or tool arguments.  ``--emit-events-to`` converts
that private runner log into versioned JSONL events that the SWE evaluator can
consume:

* ``idiom.scripted_model.requests`` preserves the original aggregate event and
  adds ordered request facts plus exact response-consumption counts.
* ``idiom.scripted_model.script_status`` always reports terminal consumption.
* exactly one outcome marker is emitted: ``idiom.scripted_model.`` followed by
  ``script_fully_consumed``, ``script_incomplete``, or ``script_exhausted``.

Each ordered ``request_facts`` entry has ``index``, ``kind`` (``text_only``,
``structured_text``, or ``tool_capable``), transport, and both adapted HTTP and
effective pre-adaptation tool surfaces. The effective surface header contains
no prompts, messages, or tool arguments. Terminal count fields distinguish
responses declared by the script, requests observed by the server, responses
consumed, and requests made after exhaustion.

String response arrays and the legacy object envelope remain supported.
"""

from __future__ import annotations

import argparse
import json
import pathlib
import signal
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


REQUEST_FACTS_SCHEMA = "idiom.scripted_model.request_facts.v1"
REQUEST_SURFACE_HEADER = "x-idiom-request-surface"


def utc_timestamp() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def request_fact(index: int, request: dict, surface_fact: dict | None = None) -> dict:
    raw_tools = request.get("tools")
    tools = raw_tools if isinstance(raw_tools, list) else []
    exposed_tool_names = set()
    for tool in tools:
        if not isinstance(tool, dict):
            continue
        function = tool.get("function")
        if not isinstance(function, dict):
            continue
        name = str(function.get("name") or "").strip()
        if name:
            exposed_tool_names.add(name)

    if tools:
        kind = "tool_capable"
    elif request.get("response_format"):
        kind = "structured_text"
    else:
        kind = "text_only"

    adapted_names = sorted(exposed_tool_names)
    surface_fact = surface_fact if isinstance(surface_fact, dict) else {}
    effective_names = sorted(
        {
            str(name).strip()
            for name in surface_fact.get("allowed_tool_names", [])
            if str(name).strip()
        }
    )
    if not surface_fact:
        effective_names = adapted_names
    transport = str(surface_fact.get("transport") or "").strip()
    return {
        "record_kind": "request",
        "schema_version": REQUEST_FACTS_SCHEMA,
        "ts": utc_timestamp(),
        "index": index,
        "kind": kind,
        "declared_tool_count": len(tools),
        "observed_tool_count": len(adapted_names),
        "adapted_tool_names": adapted_names,
        "effective_tool_count": len(effective_names),
        "effective_tool_names": effective_names,
        # Compatibility alias now reflects the effective runtime-call surface.
        "exposed_tool_names": effective_names,
        "transport": transport,
    }


def evaluation_events(records: list[dict]) -> list[dict]:
    requests = [record for record in records if record.get("record_kind") == "request"]
    requests.sort(key=lambda record: int(record.get("index") or 0))
    summaries = [record for record in records if record.get("record_kind") == "summary"]
    if not summaries:
        raise ValueError("scripted model request log has no terminal summary")
    summary = summaries[-1]

    request_facts = [
        {
            "index": int(record.get("index") or 0),
            "kind": str(record.get("kind") or ""),
            "declared_tool_count": int(record.get("declared_tool_count") or 0),
            "observed_tool_count": int(record.get("observed_tool_count") or 0),
            "adapted_tool_names": list(record.get("adapted_tool_names") or []),
            "effective_tool_count": int(record.get("effective_tool_count") or 0),
            "effective_tool_names": list(record.get("effective_tool_names") or []),
            "exposed_tool_names": list(record.get("exposed_tool_names") or []),
            "transport": str(record.get("transport") or ""),
        }
        for record in requests
    ]
    declared_response_count = int(summary.get("declared_response_count") or 0)
    observed_request_count = int(summary.get("observed_request_count") or 0)
    consumed_response_count = int(summary.get("consumed_response_count") or 0)
    exhaustion_count = int(summary.get("exhaustion_count") or 0)
    fully_consumed = bool(summary.get("fully_consumed"))
    request_indexes = [fact["index"] for fact in request_facts]
    if request_indexes != list(range(len(request_facts))):
        raise ValueError(
            f"scripted model request indexes are not contiguous: {request_indexes}"
        )
    if observed_request_count != len(request_facts):
        raise ValueError(
            "scripted model summary observed_request_count does not match "
            f"request facts: {observed_request_count} != {len(request_facts)}"
        )
    expected_consumed_count = min(observed_request_count, declared_response_count)
    if consumed_response_count != expected_consumed_count:
        raise ValueError(
            "scripted model summary consumed_response_count is inconsistent: "
            f"{consumed_response_count} != {expected_consumed_count}"
        )
    expected_exhaustion_count = max(
        0, observed_request_count - declared_response_count
    )
    if exhaustion_count != expected_exhaustion_count:
        raise ValueError(
            "scripted model summary exhaustion_count is inconsistent: "
            f"{exhaustion_count} != {expected_exhaustion_count}"
        )
    expected_fully_consumed = (
        observed_request_count == declared_response_count and exhaustion_count == 0
    )
    if fully_consumed != expected_fully_consumed:
        raise ValueError(
            "scripted model summary fully_consumed is inconsistent: "
            f"{fully_consumed} != {expected_fully_consumed}"
        )
    status = {
        "schema_version": REQUEST_FACTS_SCHEMA,
        "declared_response_count": declared_response_count,
        "observed_request_count": observed_request_count,
        "consumed_response_count": consumed_response_count,
        "fully_consumed": fully_consumed,
        "exhaustion_count": exhaustion_count,
    }
    now = utc_timestamp()
    exposed_tool_names = sorted(
        {
            name
            for fact in request_facts
            for name in fact["exposed_tool_names"]
        }
    )
    aggregate = {
        "type": "idiom.scripted_model.requests",
        "ts": now,
        **status,
        # Retain the original fields for existing logs and consumers.
        "request_count": len(request_facts),
        "tool_counts": [fact["declared_tool_count"] for fact in request_facts],
        "exposed_tool_names": exposed_tool_names,
        "request_facts": request_facts,
    }
    status_event = {
        "type": "idiom.scripted_model.script_status",
        "ts": now,
        **status,
    }
    if exhaustion_count > 0:
        outcome = "script_exhausted"
    elif fully_consumed:
        outcome = "script_fully_consumed"
    else:
        outcome = "script_incomplete"
    outcome_event = {
        "type": f"idiom.scripted_model.{outcome}",
        "ts": now,
        **status,
    }
    return [aggregate, status_event, outcome_event]


def emit_evaluation_events(request_log: pathlib.Path, event_log: pathlib.Path) -> bool:
    records = []
    for line_number, line in enumerate(
        request_log.read_text(encoding="utf-8").splitlines(), start=1
    ):
        if not line.strip():
            continue
        try:
            record = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(
                f"invalid scripted model request log JSON on line {line_number}: {exc}"
            ) from exc
        if not isinstance(record, dict):
            raise TypeError(
                f"scripted model request log line {line_number} must be an object"
            )
        records.append(record)

    event_log.parent.mkdir(parents=True, exist_ok=True)
    with event_log.open("a", encoding="utf-8") as f:
        events = evaluation_events(records)
        for event in events:
            json.dump(event, f, sort_keys=True)
            f.write("\n")
    return events[-1]["type"] == "idiom.scripted_model.script_fully_consumed"


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
        self.exhaustion_count = 0
        self.summary_written = False

    def next_response(self, request: dict, surface_fact: dict | None = None) -> dict:
        with self.lock:
            index = self.index
            self.index += 1
            self.append_request_record(request_fact(index, request, surface_fact))
            if index >= len(self.responses):
                self.exhaustion_count += 1
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

    def append_request_record(self, record: dict) -> None:
        if self.request_log is None:
            return
        self.request_log.parent.mkdir(parents=True, exist_ok=True)
        with self.request_log.open("a", encoding="utf-8") as f:
            json.dump(record, f, sort_keys=True)
            f.write("\n")

    def finalize_request_log(self) -> None:
        with self.lock:
            if self.summary_written:
                return
            self.summary_written = True
            observed_request_count = self.index
            consumed_response_count = min(observed_request_count, len(self.responses))
            summary = {
                "record_kind": "summary",
                "schema_version": REQUEST_FACTS_SCHEMA,
                "ts": utc_timestamp(),
                "declared_response_count": len(self.responses),
                "observed_request_count": observed_request_count,
                "consumed_response_count": consumed_response_count,
                "fully_consumed": (
                    observed_request_count == len(self.responses)
                    and self.exhaustion_count == 0
                ),
                "exhaustion_count": self.exhaustion_count,
            }
            self.append_request_record(summary)


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
                surface_fact = None
                raw_surface_fact = self.headers.get(REQUEST_SURFACE_HEADER, "")
                if raw_surface_fact:
                    decoded = json.loads(raw_surface_fact)
                    if not isinstance(decoded, dict):
                        raise TypeError("request surface fact header must be an object")
                    surface_fact = decoded
                response = state.next_response(request, surface_fact)
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
    parser.add_argument("--script", default="")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=0)
    parser.add_argument("--port-file", default="")
    parser.add_argument("--request-log", default="")
    parser.add_argument("--emit-events-to", default="")
    parser.add_argument("--require-fully-consumed", action="store_true")
    args = parser.parse_args(argv)

    if args.emit_events_to:
        if not args.request_log:
            parser.error("--emit-events-to requires --request-log")
        fully_consumed = emit_evaluation_events(
            pathlib.Path(args.request_log), pathlib.Path(args.emit_events_to)
        )
        if args.require_fully_consumed and not fully_consumed:
            return 1
        return 0
    if not args.script or not args.port_file:
        parser.error("server mode requires --script and --port-file")

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
        state.finalize_request_log()
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
