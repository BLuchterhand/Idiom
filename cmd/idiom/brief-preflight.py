#!/usr/bin/env python3
"""Pre-dispatch checks for idiom implementation briefs.

This intentionally enforces only the highest-signal lane-loop gates:
single-purpose decomposition, no prompt-magic contract triggers, safe content
fences, test-anchor preload presence, and exact-anchor specificity for every
required edit path.

Originally adapted from Colony's qwen_brief_preflight.py; generalized so any
project that dispatches multi-file briefs through `idiom implement` can run
the same lint before handoff.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path


MAGIC_PROMPT_PATTERNS = [
    "Contract: implementation_result",
    "Task family: implementation",
    "FIRST ACTION MUST",
    "Required first action:",
]

BAD_CONTENT_PATTERNS = [
    "===CONTENT===",
    "===END CONTENT===",
]

COMMAND_FENCE_TAGS = {"", "text", "bash", "sh", "shell", "console"}
ALLOWLIST_HEADINGS = {
    "files to modify",
    "files to edit",
    "files you may edit",
    "files you may modify",
    "files you can edit",
    "edit files",
    "editable files",
    "write scope",
    "allowed files",
    "allowed edit files",
    "allowed edit paths",
    "allowed source files",
    "edit allowlist",
    "file allowlist",
    "files allowlist",
    "source allowlist",
}
WORKSPACE_PATH_RE = re.compile(
    r"`?([A-Za-z0-9_.@+%=-][A-Za-z0-9_.@+%=/:-]*\."
    r"(?:c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|tscn|tres|"
    r"py|pyi|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|kts|sh|bash|zsh|"
    r"md|mdx|json|jsonl|yaml|yml|toml|xml|ini|cfg|conf|txt))`?"
)


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: brief-preflight.py <brief.md>", file=sys.stderr)
        return 2

    path = Path(sys.argv[1])
    if not path.is_file():
        print(f"idiom brief preflight: missing brief: {path}", file=sys.stderr)
        return 2

    text = path.read_text(encoding="utf-8")
    failures: list[str] = []
    warnings: list[str] = []

    rows = files_to_modify_rows(text)
    files = sorted({row[0] for row in rows})
    # Decomposition: hard fail only when the brief is genuinely sprawling
    # (>=4 distinct edits AND >=3 files). A header+impl+test triplet for one
    # feature (3 rows / 3 files) is allowed — the new idiom build's
    # planner-owned-verification and read-budget contracts handle these
    # cleanly. The hard-fail threshold was raised after the May-2026 idiom
    # repairs landed (apply_patch tolerance + pre-flight, full-required-set
    # read scope, post-completion verification cutoff, running progress
    # checklist injection): with 256K ctx + those repairs, atomic 5-7 file
    # atoms run cleanly in ~80 s at <10% peak ctx. The conservative
    # 4-row/3-file threshold remains as a WARN so drafters notice
    # multi-file work and confirm tight coupling.
    if len(rows) >= 8 and len(files) >= 6:
        failures.append(
            "single-purpose decomposition failed: "
            f"{len(rows)} file-allowlist row(s) across {len(files)} files. "
            "Decompose into ADD-only / MODIFY-only / TEST-only sub-atoms before dispatch."
        )
    elif len(rows) >= 4 and len(files) >= 3:
        warnings.append(
            f"{len(rows)} file-allowlist row(s) across {len(files)} files. "
            "Multi-file atom: confirm tight coupling (e.g. one feature shipped header + impl + bindings + tests + e2e), "
            "otherwise decompose into sub-atoms."
        )
    elif len(rows) >= 3 and len(files) >= 2 and not looks_like_header_impl_test_triplet(files):
        warnings.append(
            f"{len(rows)} file-allowlist row(s) across {len(files)} files. "
            "Confirm tight coupling; otherwise consider decomposing into sub-atoms."
        )
    for file_path, action in rows:
        if action and looks_like_combined_action(action):
            failures.append(
                "file-allowlist row combines multiple actions: "
                f"{file_path} -> {action!r}. Split includes, declarations, bodies, callsites, "
                "tests, and tracker stamping into separate rows/sub-atoms."
            )

    for pattern in MAGIC_PROMPT_PATTERNS:
        if pattern in text:
            failures.append(
                f"prompt-magic contract trigger found: {pattern!r}. "
                "Use `idiom implement`/structured task-family metadata, not magic phrases in briefs."
            )

    for pattern in BAD_CONTENT_PATTERNS:
        if pattern in text:
            failures.append(
                f"unsafe inlined-content fence found: {pattern!r}. "
                "Use fenced ```text blocks for content the lane must write."
            )

    # Language-tagged fences (cpp/python/etc.) historically wedged some
    # tool-call parsers. Modern lanes handle them cleanly, so this is a WARN —
    # briefs that paste reference code in language-tagged fences pass preflight
    # but the signal is preserved for downstream lanes that still suffer.
    for tag in language_tagged_content_fences(text):
        warnings.append(
            f"language-tagged content fence found: ```{tag}. "
            "Prefer ```text for content the lane writes verbatim; cpp/etc. fences are tolerated by current lanes but can still confuse stricter parsers."
        )

    if re.search(r"(?m)^>\s*(//|\*)", text):
        failures.append(
            "unsafe blockquoted code/content found. Idiom brief guidance forbids blockquote "
            "wrappers around content the lane may copy or edit."
        )

    # Test-anchor preload was a hard fail historically because lanes would
    # pattern-match against fixtures from other files. The current idiom build
    # can spend its read budget on the test file directly, so this is a WARN;
    # brief drafters still benefit from inlining the canonical setup, but it's
    # not required.
    test_files = [f for f in files if f.startswith("native/tests/") or "/tests/" in f]
    if test_files and not has_test_anchor_pack(text):
        warnings.append(
            "test-anchor preload missing for test file(s): "
            + ", ".join(test_files)
            + ". Inlining fixture inventory + nearest existing TEST_CASE reduces lane discovery cost; the new idiom build can recover without it but the brief is then larger."
        )

    if rows and not has_verified_current_state(text):
        warnings.append(
            "no obvious verified-current-state section found. Idiom briefs expect verified anchors/current code "
            "inlined before dispatch."
        )

    # Exact-anchor specificity: every required edit path must carry at least
    # one path:line reference, "at line N" mention, or quoted unique snippet
    # so the lane has a deterministic insertion point. Relational anchors
    # ("near other small predicates", "find the closing brace") force
    # orientation reads that burn the pre-write budget — this is the gap that
    # caused VL-13.3b to leave 2 of 3 required edits unwritten.
    paths_without_anchors = [
        file_path for file_path in files
        if not has_exact_anchor_for_path(text, file_path)
    ]
    if paths_without_anchors:
        failures.append(
            "exact-anchor specificity failed for required edit path(s): "
            + ", ".join(paths_without_anchors)
            + ". Each required edit path must carry at least one `<path>:<line>` reference, "
            "an explicit `line N` / `lines N-M` mention, or a quoted unique snippet from the file. "
            "Relational anchors (\"near X\", \"find the closing Y\") force the lane to spend reads "
            "locating the insertion site and frequently exhaust the pre-write budget."
        )

    # Structure-describing anchors: when a brief mentions two non-adjacent
    # line numbers in the same file (e.g., "Line 247 is X. Line 256 is Y.")
    # the planner tends to combine both insertion sites into a single
    # multi-line `old_text` for `submit_plan`, which fails because the
    # lines aren't actually adjacent in the file. The donkey atom
    # 13.4 wedged on this twice (2026-05-09): the brief had
    # "constants/animals.h:246 ... line 247 is blank. Similarly
    # constants/animals.h:255 ... line 256 is blank." and the planner
    # built an old_text spanning both line ranges. Suggest single-line
    # unique anchors per insertion site instead.
    structure_anchor_paths = sorted(structure_describing_anchor_paths(text, files))
    if structure_anchor_paths:
        warnings.append(
            "structure-describing anchors found for required edit path(s): "
            + ", ".join(structure_anchor_paths)
            + ". The brief describes multiple non-adjacent line numbers in "
            "the same file (e.g., \"Line N is X. Line M is Y.\"). Planners tend "
            "to combine these into a single multi-line `old_text` that fails the "
            "executor's substring match. Replace with separate single-line unique "
            "anchors per insertion site (e.g., \"Use line N's exact text as "
            "old_text for the first edit; use line M's exact text for the second\")."
        )

    if failures:
        print(f"idiom brief preflight: FAIL {path}", file=sys.stderr)
        for failure in failures:
            print(f"- {failure}", file=sys.stderr)
        for warning in warnings:
            print(f"- WARN: {warning}", file=sys.stderr)
        return 1

    print(
        f"idiom brief preflight: OK {path} "
        f"({len(rows)} file-allowlist row(s), {len(files)} file(s))"
    )
    for warning in warnings:
        print(f"idiom brief preflight: WARN {warning}", file=sys.stderr)
    return 0


def files_to_modify_rows(text: str) -> list[tuple[str, str]]:
    lines = text.splitlines()
    in_section = False
    rows: list[tuple[str, str]] = []
    for line in lines:
        stripped = line.strip()
        if is_allowlist_heading(stripped):
            in_section = True
            continue
        if in_section and stripped.startswith("## "):
            break
        if not in_section:
            continue
        parsed = parse_allowlist_table_row(stripped) or parse_allowlist_bullet(stripped)
        if not parsed:
            continue
        rows.append(parsed)
    return rows


def is_allowlist_heading(line: str) -> bool:
    if not line.startswith("#"):
        return False
    normalized = normalize_heading(line)
    return normalized in ALLOWLIST_HEADINGS


def normalize_heading(line: str) -> str:
    value = line.strip().strip("#*: `\"'")
    value = value.rstrip(":")
    value = re.sub(r"[-_/]+", " ", value.lower())
    return " ".join(value.split())


def parse_allowlist_table_row(stripped: str) -> tuple[str, str] | None:
    if not stripped.startswith("|"):
        return None
    cells = [cell.strip() for cell in stripped.strip("|").split("|")]
    if len(cells) < 2:
        return None
    first = cells[0].lower()
    if first in {"file", "---"} or set(first) <= {"-", ":"}:
        return None
    file_path = cells[0].strip("` ")
    if not looks_like_workspace_path(file_path):
        return None
    return (file_path, cells[1])


def parse_allowlist_bullet(stripped: str) -> tuple[str, str] | None:
    if not re.match(r"^[-*+]\s+", stripped):
        return None
    body = re.sub(r"^[-*+]\s+(?:\[[ xX]\]\s+)?", "", stripped).strip()
    match = WORKSPACE_PATH_RE.search(body)
    if not match:
        return None
    file_path = match.group(1).strip("` ")
    if not looks_like_workspace_path(file_path):
        return None
    action = (body[: match.start()] + body[match.end() :]).strip(" :-|")
    return (file_path, action)


def looks_like_workspace_path(file_path: str) -> bool:
    if not file_path or file_path.startswith("/") or "://" in file_path:
        return False
    if " " in file_path and "/" not in file_path:
        return False
    return bool(WORKSPACE_PATH_RE.fullmatch(file_path) or "/" in file_path)


def has_test_anchor_pack(text: str) -> bool:
    lower = text.lower()
    return (
        "fixture_inventory" in lower
        and (
            "nearest existing test_case" in lower
            or "canonical setup pattern" in lower
            or "helper declaration" in lower
            or "helper body" in lower
        )
    )


def looks_like_header_impl_test_triplet(files: list[str]) -> bool:
    """A clean (header, impl, test) triplet for one feature is allowed even at 3 rows.

    Match when files are 3 of: one .h/.hpp, one .cpp/.cc, one in tests/. The
    header+impl share a stem (same module) and the test references the same
    domain. We approximate by stem overlap on the first two and a tests/ path
    on the third.
    """
    if len(files) != 3:
        return False
    tests = [f for f in files if f.startswith("native/tests/") or "/tests/" in f]
    non_tests = [f for f in files if f not in tests]
    headers = [f for f in non_tests if f.endswith((".h", ".hpp", ".hh", ".hxx"))]
    impls = [f for f in non_tests if f.endswith((".cpp", ".cc", ".cxx"))]
    if not (len(headers) == 1 and len(impls) == 1 and len(tests) == 1):
        return False
    header_stem = Path(headers[0]).stem
    impl_stem = Path(impls[0]).stem
    return header_stem == impl_stem


def looks_like_combined_action(action: str) -> bool:
    plain = re.sub(r"`[^`]*`", "", action)
    lower = plain.lower()
    if " + " in plain or ";" in plain:
        return True
    verbs = [
        "add",
        "insert",
        "modify",
        "wire",
        "update",
        "remove",
        "replace",
        "create",
    ]
    hits = sum(1 for verb in verbs if re.search(rf"\b{verb}\b", lower))
    return hits >= 2 and " and " in lower


def language_tagged_content_fences(text: str) -> list[str]:
    tags: list[str] = []
    for match in re.finditer(r"(?m)^```\s*([A-Za-z0-9_+.#-]+)\s*$", text):
        tag = match.group(1).strip().lower()
        if tag not in COMMAND_FENCE_TAGS:
            tags.append(tag)
    return tags


def has_verified_current_state(text: str) -> bool:
    lower = text.lower()
    return (
        "verified current" in lower
        or "current state" in lower
        or "verified anchors" in lower
        or "inline canonical pattern" in lower
    )


def has_exact_anchor_for_path(text: str, file_path: str) -> bool:
    """A required edit path needs a deterministic insertion site.

    Three accepted forms — any one is sufficient:
      1. `<path>:<line>` or `<basename>:<line>` (e.g. `energy.cpp:805`).
      2. `at line N`, `line N`, or `lines N-M` within ~120 chars of the path.
      3. A fenced ```text/```cpp/```etc. snippet that includes the basename
         OR a quoted line that includes the basename — these typically carry
         a unique-enough anchor for an old_text/new_text edit.
    """
    if not file_path:
        return False
    basename = file_path.rsplit("/", 1)[-1]
    # Form 1: path:line — the strongest, deterministic.
    if re.search(rf"`?{re.escape(file_path)}`?\s*:\s*\d+", text):
        return True
    if re.search(rf"`?{re.escape(basename)}`?\s*:\s*\d+", text):
        return True
    # Form 2: "at line N" / "line N" / "lines N-M" near a path mention.
    path_re = re.compile(rf"`?{re.escape(file_path)}`?|`?{re.escape(basename)}`?")
    for match in path_re.finditer(text):
        window_start = max(0, match.start() - 120)
        window_end = min(len(text), match.end() + 120)
        window = text[window_start:window_end]
        if re.search(r"\b(?:at\s+line|line|lines)\s+\d+", window, re.IGNORECASE):
            return True
    # Form 3: fenced snippet that names the basename and contains code.
    fence_re = re.compile(r"```[A-Za-z0-9_+.#-]*\s*\n([\s\S]*?)\n```", re.MULTILINE)
    for match in fence_re.finditer(text):
        body = match.group(1)
        if basename in body and ("\n" in body or len(body.splitlines()) >= 1):
            return True
    return False


def structure_describing_anchor_paths(text: str, files: list[str]) -> set[str]:
    """Return the set of required-edit paths whose anchors describe the
    file structure across non-adjacent lines.

    The trap shape we're catching: a brief mentions two distinct line
    numbers in the same file ("Line 247 is X. Line 256 is Y.") and the
    planner combines both insertion sites into a single multi-line
    `old_text`. The combined substring assumes the lines are adjacent
    (which they aren't — there's other content between them in the
    actual file), so the executor's substring match fails.

    Heuristic: for each required-edit path, find every "line N" /
    "<path>:N" / "lines N-M" mention within ~200 chars of a basename
    reference, collect the line numbers, and flag the path if any pair
    of distinct line numbers differs by more than 2 (adjacent lines and
    immediate next-line are fine — only non-adjacent pairs are the
    trap shape).

    Returns an empty set when no path trips the heuristic.
    """
    flagged: set[str] = set()
    if not files:
        return flagged
    line_number_re = re.compile(r"line[s]?\s+(\d+)|:\s*(\d+)\b", re.IGNORECASE)
    for file_path in files:
        basename = file_path.rsplit("/", 1)[-1]
        path_re = re.compile(rf"`?{re.escape(file_path)}`?|`?{re.escape(basename)}`?")
        line_numbers: set[int] = set()
        for path_match in path_re.finditer(text):
            window_start = max(0, path_match.start() - 200)
            window_end = min(len(text), path_match.end() + 200)
            window = text[window_start:window_end]
            for ln_match in line_number_re.finditer(window):
                raw = ln_match.group(1) or ln_match.group(2)
                if not raw:
                    continue
                try:
                    line_numbers.add(int(raw))
                except ValueError:
                    continue
        if len(line_numbers) < 2:
            continue
        sorted_lines = sorted(line_numbers)
        for i in range(len(sorted_lines) - 1):
            for j in range(i + 1, len(sorted_lines)):
                if sorted_lines[j] - sorted_lines[i] > 2:
                    flagged.add(file_path)
                    break
            if file_path in flagged:
                break
    return flagged


if __name__ == "__main__":
    raise SystemExit(main())
