#!/usr/bin/env python3
"""Build a bounded scripted-contract suite from task IDs in the canonical suite."""

from __future__ import annotations

import argparse
import json
import pathlib
import sys


SELECTED_COVERAGE_KEYS = (
    "required_families",
    "required_initial_lanes",
    "required_followup_lanes",
    "required_cross_products",
)


def selected_suite(
    suite: dict, task_ids: list[str], require_canonical_coverage: bool = False
) -> dict:
    if not task_ids:
        raise ValueError("at least one --task is required")

    tasks = suite.get("tasks")
    if not isinstance(tasks, list):
        raise TypeError("suite tasks must be an array")

    by_id = {
        str(task.get("id") or "").strip(): task
        for task in tasks
        if isinstance(task, dict) and str(task.get("id") or "").strip()
    }
    missing = [task_id for task_id in task_ids if task_id not in by_id]
    if missing:
        raise ValueError("unknown scripted task ID(s): " + ", ".join(missing))
    if len(set(task_ids)) != len(task_ids):
        raise ValueError("duplicate scripted task ID")

    name = str(suite.get("name") or "scripted-contracts").strip()
    coverage = {"min_tasks": len(task_ids)}
    if require_canonical_coverage:
        canonical_coverage = suite.get("coverage")
        if not isinstance(canonical_coverage, dict):
            raise TypeError("suite coverage must be an object")
        for key in SELECTED_COVERAGE_KEYS:
            value = canonical_coverage.get(key)
            if value:
                coverage[key] = value

    return {
        "name": name + "-selected",
        "version": int(suite.get("version") or 1),
        "coverage": coverage,
        "tasks": [by_id[task_id] for task_id in task_ids],
        "defaults": suite.get("defaults") or {},
    }


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--suite", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--task", action="append", dest="task_ids", default=[])
    parser.add_argument("--require-canonical-coverage", action="store_true")
    args = parser.parse_args(argv)

    try:
        suite = json.loads(args.suite.read_text(encoding="utf-8"))
        if not isinstance(suite, dict):
            raise TypeError("suite must be a JSON object")
        selected = selected_suite(
            suite, args.task_ids, args.require_canonical_coverage
        )
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(
            json.dumps(selected, indent=2, sort_keys=False) + "\n",
            encoding="utf-8",
        )
    except (OSError, TypeError, ValueError, json.JSONDecodeError) as exc:
        print(f"select scripted contract tasks: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
