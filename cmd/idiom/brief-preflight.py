#!/usr/bin/env python3
"""Pre-dispatch checks for idiom implementation briefs.

Enforces the highest-signal lane-loop gates: single-purpose decomposition, no
prompt-magic contract triggers, safe content fences, test-anchor preload
presence, exact-anchor specificity for every required edit path, prose-anchor
rejection in the file allowlist, branching-language warnings in Required
Edits, discovery-mandate rejection, and (when the workspace can be resolved)
file-existence + labeled-old_text anchor verification against the live tree.

Originally adapted from Colony's qwen_brief_preflight.py; generalized so any
project that dispatches multi-file briefs through `idiom implement` can run
the same lint before handoff.
"""

from __future__ import annotations

import os
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

# Prose anchors force the planner to spend discovery calls reconstructing the
# insertion site at run time. The submit_plan schema expects exact
# old_text/new_text substrings or start_line/end_line ranges; prose anchors
# are not edit-safe and trip the search_content anti-chain gate when the
# planner loops on them.
PROSE_ANCHOR_PATTERNS = [
    r"\bnear (?:the )?top(?: of (?:the )?file)?\b",
    r"\bnear (?:the )?bottom(?: of (?:the )?file)?\b",
    r"\balongside(?: (?:the )?existing)?\b",
    r"\baround line \d+\b",
    r"\bthe relevant section\b",
    r"\bat the end of the file\b",
    r"\bafter the existing [^.\n]{0,80}? includes?\b",
]

# Branching language in Required Edits forces the lane to design at run time.
# Legitimate elsewhere (HARD PROHIBITIONS, test discussions); scope this check
# to ### Edit N section bodies only.
BRANCHING_PATTERNS = [
    r"\bOption \d+\b",
    r"\bAlternatively\b",
    r"\bIf .{0,80}? doesn'?t\b",
    r"\bIf neither\b",
    r"\bEither .{0,80}? or\b",
    r"\botherwise (?:try|use|add)\b",
]

# Discovery-mandate phrasing deflects work the brief-drafter should have
# already done at draft time. The orchestrator owns discovery; the lane is
# not asked to run lookup tools and consume their output.
DISCOVERY_MANDATE_PATTERNS = [
    r"\brun (?:\./scripts/|idiom (?:tool |context ))[a-z_]+ and (?:use|inline|paste|incorporate)\b",
    r"\bverify .{0,120}? by (?:running|calling) (?:\./scripts/|idiom (?:tool |context ))",
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
    r"go|md|mdx|json|jsonl|yaml|yml|toml|xml|ini|cfg|conf|txt))`?"
)

FENCE_RE = re.compile(r"```[^\n]*\n.*?\n```", re.DOTALL)

# Match labeled old_text blocks. Accepted shapes:
#   old_text:
#   ```text
#   ...
#   ```
#
#   **old_text:**
#   ```
#   ...
#   ```
OLD_TEXT_BLOCK_RE = re.compile(
    r"(?:\*\*)?old_text:?(?:\*\*)?\s*\n+```[A-Za-z0-9_+.#-]*\s*\n(.*?)\n```",
    re.DOTALL | re.IGNORECASE,
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
    # (>=8 distinct edits AND >=6 files). A header+impl+test triplet for one
    # feature (3 rows / 3 files) is allowed — the planner-owned-verification
    # and read-budget contracts handle these cleanly.
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
        if action and not is_newfile_action(action) and looks_like_combined_action(action):
            failures.append(
                "file-allowlist row combines multiple actions: "
                f"{file_path} -> {action!r}. Split includes, declarations, bodies, callsites, "
                "tests, and tracker stamping into separate rows/sub-atoms."
            )

    # Prose-anchor BLOCK in allowlist row actions. Prose anchors here force
    # the planner to spend discovery calls reconstructing the insertion site
    # even when the brief later supplies an exact edit in a Required Edits
    # section. This is the gap that caused the falldetect brief to wedge
    # despite passing `has_exact_anchor_for_path` (which accepts a "line N"
    # mention as an exact anchor).
    for file_path, action in rows:
        if not action:
            continue
        action_text = strip_fenced_blocks(action)
        for pattern in PROSE_ANCHOR_PATTERNS:
            match = re.search(pattern, action_text, re.IGNORECASE)
            if match:
                failures.append(
                    f"prose anchor in allowlist row for {file_path}: "
                    f"{match.group(0)!r}. Replace with `<path>:<line>`, an exact "
                    "line number, or a unique snippet. Prose anchors force the "
                    "planner to spend discovery calls finding the insertion site."
                )
                break

    # Branching-language WARN inside Required Edits. Required Edits should
    # be unconditional — branching turns the planner into a designer at run
    # time, which costs reads and breaks the "planner thinks once" contract.
    for heading, body in required_edit_blocks(text):
        scannable = strip_fenced_blocks(body)
        for pattern in BRANCHING_PATTERNS:
            match = re.search(pattern, scannable, re.IGNORECASE)
            if match:
                warnings.append(
                    f"branching language in Required Edit ({heading}): "
                    f"{match.group(0)!r}. Required Edits should be unconditional; "
                    "branching forces the planner to design at run time."
                )
                break

    # Discovery-mandate BLOCK (global). The brief-drafter owns discovery;
    # asking the lane to run tools and consume their output deflects work
    # that should have been done at draft time and burns the lane's budget.
    for pattern in DISCOVERY_MANDATE_PATTERNS:
        match = re.search(pattern, text, re.IGNORECASE)
        if match:
            failures.append(
                f"discovery-mandate phrasing: {match.group(0)!r}. The brief-drafter "
                "owns discovery — run the tool yourself, inline the verified output, "
                "and the lane's job becomes a single Write."
            )
            break

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
    # tool-call parsers. Modern lanes handle them cleanly, so this is a WARN.
    for tag in language_tagged_content_fences(text):
        failures.append(
            f"unknown fence tag ```{tag}: only COMMAND_FENCE_TAGS ({', '.join(sorted(COMMAND_FENCE_TAGS))}) are allowed. "
            "Use ```text for content blocks or a command tag like ```bash or ```sh for shell snippets."
        )

    if looks_like_outer_fenced(text):
        failures.append(
            "outer code-fence wrapper detected: the brief is entirely "
            "wrapped in a ``` fence. Unwrap the brief content before dispatch."
        )

    if re.search(r"(?m)^>\s*(//|\*)", text):
        failures.append(
            "unsafe blockquoted code/content found. Idiom brief guidance forbids blockquote "
            "wrappers around content the lane may copy or edit."
        )

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

    paths_without_anchors = [
        file_path for file_path, action in rows
        if not is_newfile_action(action) and not has_exact_anchor_for_path(text, file_path)
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

    # Atom-size estimation. The planner phase emits submit_plan with all
    # inlined ```text content escaped as JSON `new_text` strings; once the
    # estimated escaped size approaches the backend's max_tokens cap, the
    # plan truncates mid-edit and the lane wedges at completion gate. We
    # estimate from the raw text-fence character count, add a small escape
    # overhead, and divide by the ~4 chars-per-token English/code heuristic.
    # Thresholds assume the default planner max_tokens of 8192 from
    # .idiom/backends.json; projects with a different cap can override via
    # IDIOM_PLANNER_MAX_TOKENS env var.
    estimated_tokens, heavy_blocks = estimate_output_token_load(text)
    max_tokens_cap = planner_max_tokens_cap()
    warn_threshold = max_tokens_cap // 2
    fail_threshold = (max_tokens_cap * 4) // 5
    if estimated_tokens >= fail_threshold:
        failures.append(
            f"atom-size estimate {estimated_tokens} tokens of inlined content "
            f"exceeds {fail_threshold} (80% of planner max_tokens={max_tokens_cap}). "
            f"submit_plan will likely truncate mid-edit. "
            f"{decomposition_hint(heavy_blocks)}"
        )
    elif estimated_tokens >= warn_threshold:
        warnings.append(
            f"atom-size estimate {estimated_tokens} tokens of inlined content "
            f"approaches {warn_threshold} (50% of planner max_tokens={max_tokens_cap}). "
            f"Consider decomposing. {decomposition_hint(heavy_blocks)}"
        )

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

    # Canonical anchor verification — file existence + labeled old_text.
    # Resolve workspace from cwd → walk up to a .git root; fall back to cwd.
    # When no named file resolves under the chosen root, WARN explaining the
    # skip rather than flooding existence failures (lint may be running from
    # outside the target repo).
    if rows:
        workspace_root = resolve_workspace_root(Path.cwd())
        verify_failures, verify_warnings = verify_anchors(text, rows, workspace_root)
        failures.extend(verify_failures)
        warnings.extend(verify_warnings)

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
    # Strip parenthetical suffixes ("(EXHAUSTIVE allowlist)", "(scope: X)")
    # so the canonical brief skeleton template
    # ("## Files to Modify (EXHAUSTIVE allowlist)") matches the base form
    # in ALLOWLIST_HEADINGS. Without this strip, the linter silently
    # accepts any brief that follows the template by reporting zero
    # allowlist rows and skipping every downstream check.
    value = re.sub(r"\s*\([^)]*\)\s*", " ", value)
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
    """A clean (header, impl, test) triplet for one feature is allowed even at 3 rows."""
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


def is_newfile_action(action: str) -> bool:
    if not action:
        return False
    return action.strip().lower().startswith(("new file:", "create file:"))


def looks_like_outer_fenced(text: str) -> bool:
    lines = [line for line in text.splitlines() if line.strip()]
    if len(lines) < 2:
        return False
    first = lines[0].rstrip()
    last = lines[-1].rstrip()
    if not re.match(r"^```[A-Za-z0-9_+.#-]*\s*$", first):
        return False
    if not re.match(r"^```\s*$", last):
        return False
    return True


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
    if not file_path:
        return False
    basename = file_path.rsplit("/", 1)[-1]
    if re.search(rf"`?{re.escape(file_path)}`?\s*:\s*\d+", text):
        return True
    if re.search(rf"`?{re.escape(basename)}`?\s*:\s*\d+", text):
        return True
    path_re = re.compile(rf"`?{re.escape(file_path)}`?|`?{re.escape(basename)}`?")
    for match in path_re.finditer(text):
        window_start = max(0, match.start() - 120)
        window_end = min(len(text), match.end() + 120)
        window = text[window_start:window_end]
        if re.search(r"\b(?:at\s+line|line|lines)\s+\d+", window, re.IGNORECASE):
            return True
    fence_re = re.compile(r"```[A-Za-z0-9_+.#-]*\s*\n([\s\S]*?)\n```", re.MULTILINE)
    for match in fence_re.finditer(text):
        body = match.group(1)
        if basename in body and ("\n" in body or len(body.splitlines()) >= 1):
            return True
    return False


def structure_describing_anchor_paths(text: str, files: list[str]) -> set[str]:
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


def required_edit_blocks(text: str) -> list[tuple[str, str]]:
    """Return [(heading, body)] for each ### Edit N section.

    A Required Edit block runs from a `### Edit ...` heading until the next
    `### ` or `## ` heading or end-of-file. Used to scope branching-language
    checks to where they matter (not HARD PROHIBITIONS or test discussion).
    """
    blocks: list[tuple[str, str]] = []
    lines = text.splitlines()
    current_heading: str | None = None
    current_lines: list[str] = []
    edit_re = re.compile(r"^###\s+Edit\s+", re.IGNORECASE)
    for line in lines:
        stripped = line.strip()
        is_h2_or_h3 = stripped.startswith("## ") or stripped.startswith("### ")
        if is_h2_or_h3:
            if current_heading is not None:
                blocks.append((current_heading, "\n".join(current_lines)))
                current_heading = None
                current_lines = []
            if edit_re.match(stripped):
                current_heading = stripped.lstrip("#").strip()
            continue
        if current_heading is not None:
            current_lines.append(line)
    if current_heading is not None:
        blocks.append((current_heading, "\n".join(current_lines)))
    return blocks


def strip_fenced_blocks(text: str) -> str:
    return FENCE_RE.sub("", text)


# Match ```text fences (the canonical content-fence the brief skeleton
# requires for inlined code or file content). Captures the inner body so
# we can sum its size. Other-language fences (```bash, ```sh, etc.) are
# command snippets and excluded from the atom-size estimate.
TEXT_FENCE_RE = re.compile(r"```text\s*\n(.*?)\n```", re.DOTALL)


def planner_max_tokens_cap() -> int:
    """Returns the planner's max_tokens cap used for size thresholds.

    Defaults to 8192 (matches the shipped .idiom/backends.json for both
    qwen3.6-35b and qwen3-coder-30b). Override via IDIOM_PLANNER_MAX_TOKENS
    when a project uses a different backend cap.
    """
    raw = os.environ.get("IDIOM_PLANNER_MAX_TOKENS", "").strip()
    if raw:
        try:
            value = int(raw)
            if value > 0:
                return value
        except ValueError:
            pass
    return 8192


def estimate_output_token_load(text: str) -> tuple[int, list[tuple[int, str]]]:
    """Estimate the tokens of inlined ``text`` content the planner must emit.

    Returns (estimated_tokens, heavy_blocks) where heavy_blocks is a list of
    (chars, preview) tuples sorted by size descending, useful for the
    decomposition hint. The estimate sums the raw character count of every
    ```text fence, applies a 1.2x JSON-escape overhead factor, and divides
    by 4 (chars per token; standard English/code rule of thumb). This is
    intentionally conservative — JSON-escaping `"` and `\\` and newlines
    pushes overhead up; the 1.2x covers typical code.
    """
    blocks: list[tuple[int, str]] = []
    total_chars = 0
    for match in TEXT_FENCE_RE.finditer(text):
        body = match.group(1)
        body_len = len(body)
        if body_len == 0:
            continue
        total_chars += body_len
        preview = body.splitlines()[0][:60] if body else ""
        blocks.append((body_len, preview))
    blocks.sort(key=lambda b: b[0], reverse=True)
    estimated_tokens = int((total_chars * 1.2) // 4)
    return estimated_tokens, blocks


def decomposition_hint(heavy_blocks: list[tuple[int, str]]) -> str:
    """Render a top-3 heaviest-fences hint for the user."""
    if not heavy_blocks:
        return ""
    top = heavy_blocks[:3]
    parts = [
        f"{chars} chars '{preview[:40]}...'" if preview else f"{chars} chars"
        for chars, preview in top
    ]
    return "Heaviest inlined blocks: " + "; ".join(parts) + ". Split the brief along these boundaries into ADD-only / MODIFY-only / TEST-only sub-atoms."


def resolve_workspace_root(start: Path) -> Path | None:
    """Walk up from `start` to find a directory containing `.git`.

    Returns the resolved workspace root, or None if no ancestor contains
    a `.git` directory or file (git worktrees use a `.git` file).
    """
    try:
        current = start.resolve()
    except OSError:
        return None
    for candidate in [current, *current.parents]:
        if (candidate / ".git").exists():
            return candidate
    return None


def verify_anchors(
    text: str,
    rows: list[tuple[str, str]],
    workspace_root: Path | None,
) -> tuple[list[str], list[str]]:
    """File-existence + labeled-old_text verification.

    For each non-NEW-FILE row, the named file must exist under
    workspace_root (BLOCK on miss). For each `old_text:`-labeled block in a
    Required Edit section that names an allowlisted file, the literal text
    is grep'd against the file (BLOCK on miss, WARN on non-unique).

    Returns (failures, warnings). If the workspace can't be resolved, or if
    no named file exists under the chosen root, both lists carry a single
    WARN explaining the skip — lint may be running from outside the target
    repo, and flooding existence failures would just be noise.
    """
    failures: list[str] = []
    warnings: list[str] = []
    if workspace_root is None:
        warnings.append(
            "anchor verification skipped: could not resolve workspace root "
            "from cwd. Run `idiom brief lint` from inside the target repo."
        )
        return failures, warnings

    nonfile_rows = [(fp, act) for fp, act in rows if not is_newfile_action(act)]
    if not nonfile_rows:
        return failures, warnings

    resolvable_any = any(
        (workspace_root / fp).exists() for fp, _ in nonfile_rows
    )
    if not resolvable_any:
        sampled = ", ".join(fp for fp, _ in nonfile_rows[:3])
        warnings.append(
            "anchor verification skipped: no named files exist under "
            f"workspace_root={workspace_root}. Sampled paths: {sampled}. "
            "Run lint from inside the target repo, or the brief may "
            "reference a different project."
        )
        return failures, warnings

    missing_paths: set[str] = set()
    for file_path, _action in nonfile_rows:
        full_path = workspace_root / file_path
        if not full_path.exists():
            failures.append(
                f"anchor verification: file not found in workspace: {file_path} "
                f"(resolved to {full_path}). The brief references a path that "
                "doesn't exist — either the path is stale or the lint is "
                "running from the wrong workspace."
            )
            missing_paths.add(file_path)

    # Labeled old_text verification.
    rows_by_basename = {Path(fp).name: fp for fp, _ in nonfile_rows}
    for heading, body in required_edit_blocks(text):
        section_file = _file_for_edit_section(heading, body, rows_by_basename)
        if not section_file or section_file in missing_paths:
            continue
        full_path = workspace_root / section_file
        if not full_path.exists():
            continue  # already reported above
        try:
            file_content = full_path.read_text(encoding="utf-8", errors="replace")
        except OSError as exc:
            warnings.append(
                f"anchor verification: could not read {section_file}: {exc}"
            )
            continue
        for match in OLD_TEXT_BLOCK_RE.finditer(body):
            old_text = match.group(1)
            if not old_text.strip():
                continue
            count = file_content.count(old_text)
            if count == 0:
                failures.append(
                    f"anchor verification: old_text block in '{heading}' "
                    f"does not match {section_file}. The literal text in the "
                    "brief's old_text block was not found in the file — "
                    "either the anchor is stale or the file has been edited."
                )
            elif count > 1:
                warnings.append(
                    f"anchor not unique in {section_file} ({count} matches) "
                    f"for '{heading}'. Extend the old_text block until it "
                    "matches exactly once; otherwise the planner has to "
                    "disambiguate at run time."
                )
    return failures, warnings


def _file_for_edit_section(
    heading: str,
    body: str,
    rows_by_basename: dict[str, str],
) -> str | None:
    """Pick which allowlist file an Edit section refers to.

    Look for an allowlist basename in the heading first (canonical form:
    "### Edit N -- <basename> ..."); fall back to a basename mentioned in
    the first 200 chars of the body.
    """
    for basename, file_path in rows_by_basename.items():
        if basename in heading:
            return file_path
    head = body[:200]
    for basename, file_path in rows_by_basename.items():
        if basename in head:
            return file_path
    return None


if __name__ == "__main__":
    raise SystemExit(main())
