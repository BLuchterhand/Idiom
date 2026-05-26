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
import json
import re
import shlex
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
EDIT_SHAPE_RE = re.compile(
    r"\b(old_text|new_text|start_line|end_line|replace_all|content_ref|new_text_ref)\b",
    re.IGNORECASE,
)
COMMAND_MUTATION_RE = re.compile(
    r"\b(?:run|execute|call)\s+`?(?:(?:python3?|bash|sh|go|node|npm|pnpm|make|scons|idiom)"
    r"(?=\s|`|$)|\./scripts/|scripts/)[^`\n]{0,160}?`?.{0,160}?\b"
    r"(?:regenerate|generate|write|update|refresh|modify|append|create)\b",
    re.IGNORECASE,
)
PROSE_MUTATION_ANCHOR_RE = re.compile(
    r"\b(?:append|insert|add|replace|update|refresh|regenerate)\b.{0,120}?"
    r"\b(?:after|before|around|near|at line|line \d+|entry|section)\b",
    re.IGNORECASE,
)
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
SCOPE_HEADINGS = {"scope"}
SCOPE_FILE_HEADINGS = {"files", "file"}
WORKSPACE_PATH_RE = re.compile(
    r"`?([A-Za-z0-9_.@+%=-][A-Za-z0-9_.@+%=/:-]*\."
    r"(?:c|cc|cpp|cxx|h|hh|hpp|hxx|ipp|inl|tpp|gd|tscn|tres|"
    r"py|pyi|ts|tsx|js|jsx|mjs|cjs|rs|java|kt|kts|sh|bash|zsh|"
    r"go|md|mdx|json|jsonl|yaml|yml|toml|xml|ini|cfg|conf|txt))`?"
)

FENCE_RE = re.compile(r"```[^\n]*\n.*?\n```", re.DOTALL)

# Brief mode taxonomy (matches §2.2 / §2.2.1 of
# docs/refine-end-to-end-loop.md). The lint must be mode-aware so a
# structurally-clean brief that's missing a required section gets a
# BLOCK instead of passing vacuously (Colony Gap 7, 2026-05-11).
BRIEF_MODE_ANCHORED = "anchored-edits"
BRIEF_MODE_MARKDOWN = "markdown-deliverable"
BRIEF_MODE_AMBIGUOUS = "ambiguous"
BRIEF_MODE_UNKNOWN = "unknown"

# Required section headings per mode. Each entry is a tuple of
# acceptable heading variants — `Required Edits` and `Required
# Behavior` are both accepted historically, so either satisfies the
# requirement. The Acceptance section was undocumented as required
# despite being in every canonical template — Bug 7 root cause #2.
REQUIRED_SECTIONS_ANCHORED = [
    ("## Files To Modify", "## Files to Modify", "## Files You May Edit", "### Files"),
    ("## Verified Current State", "## Verified Anchors", "## Current state", "## Current State"),
    ("## Required Edits", "## Required Behavior"),
    ("## Acceptance",),
]
REQUIRED_SECTIONS_MARKDOWN = [
    ("## Deliverable",),
    ("## Inline content", "## Inline Content"),
    ("## Acceptance",),
]


def detect_brief_mode(text: str) -> str:
    """Detect the brief's deliverable shape by section presence.
    Section detection ignores fenced content (example templates inside
    ```text blocks don't count as the brief's own structure).
    Returns one of the BRIEF_MODE_* constants.
    """
    scannable = strip_fenced_blocks(text)
    has_files = has_allowlist_section(scannable)
    has_deliverable = bool(re.search(r"^##\s+Deliverable\b", scannable, re.MULTILINE | re.IGNORECASE))
    has_inline = bool(re.search(r"^##\s+Inline content\b", scannable, re.MULTILINE | re.IGNORECASE))
    if has_deliverable and has_inline:
        if has_files:
            # Mixed shape — likely an authoring error. Caller emits WARN.
            return BRIEF_MODE_AMBIGUOUS
        return BRIEF_MODE_MARKDOWN
    if has_files:
        return BRIEF_MODE_ANCHORED
    return BRIEF_MODE_UNKNOWN


def find_section_body(text: str, heading: str) -> str | None:
    """Return the body text between `heading` and the next `## ` heading.
    Ignores fenced content (example templates inside fences don't count
    as a real section). Returns None when the heading isn't present.
    Body may be empty string when the heading is followed immediately
    by another section heading.
    """
    scannable = strip_fenced_blocks(text)
    # Allow trailing modifiers on the heading line (e.g. parenthetical
    # clarifications like "## Files to Modify (EXHAUSTIVE allowlist)").
    # The brief lint historically accepted those, and the structural-
    # completeness check must too.
    pattern = re.compile(
        rf"^{re.escape(heading)}[^\n]*\n(.*?)(?=^##\s|\Z)",
        re.MULTILINE | re.DOTALL | re.IGNORECASE,
    )
    match = pattern.search(scannable)
    if not match:
        return None
    return match.group(1).strip()


def required_sections_for_mode(mode: str) -> list[tuple[str, ...]]:
    if mode == BRIEF_MODE_ANCHORED:
        return REQUIRED_SECTIONS_ANCHORED
    if mode == BRIEF_MODE_MARKDOWN:
        return REQUIRED_SECTIONS_MARKDOWN
    return []


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

DEFAULT_LANGUAGE_RULES = [
    {
        "language": "gdscript",
        "extensions": [".gd"],
        "forbidden_acceptance_markers": ["TEST_CASE", "CHECK(", "REQUIRE("],
        "allowed_acceptance_markers": ["func test_", "extends GutTest", "GdUnit"],
    },
    {
        "language": "cpp",
        "extensions": [
            ".c",
            ".cc",
            ".cpp",
            ".cxx",
            ".h",
            ".hh",
            ".hpp",
            ".hxx",
            ".ipp",
            ".inl",
            ".tpp",
        ],
        "forbidden_acceptance_markers": ["extends GutTest", "GdUnit", "func test_"],
        "allowed_acceptance_markers": ["TEST_CASE", "CHECK(", "REQUIRE("],
    },
]

COMMAND_LIKE_PREFIXES = {
    "bash",
    "go",
    "godot",
    "make",
    "node",
    "npm",
    "pnpm",
    "python",
    "python3",
    "pytest",
    "scons",
    "sh",
    "yarn",
}


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

    # Mode-aware required-section check (Bug 7, 2026-05-11). The lint
    # was previously anchored-edits-only and passed vacuously on
    # markdown-deliverable briefs because they have no `## Files To
    # Modify` rows for the row-based checks to trigger on. Each mode
    # now declares its required sections; missing → BLOCK; present
    # but empty body → BLOCK.
    mode = detect_brief_mode(text)
    if mode == BRIEF_MODE_AMBIGUOUS:
        warnings.append(
            "brief shape is ambiguous: contains both `## Files To Modify` "
            "(anchored-edits) AND `## Deliverable`+`## Inline content` "
            "(markdown-deliverable). Refine override templates should pick one "
            "mode; mixing collapses the convergence guarantee. Force a mode "
            "with `idiom lane refine --mode anchored|markdown` if intentional."
        )
    elif mode == BRIEF_MODE_UNKNOWN:
        failures.append(
            "brief shape is unknown: no `## Files To Modify` (anchored-edits) "
            "and no `## Deliverable`+`## Inline content` (markdown-deliverable). "
            "Add the canonical section for your mode before dispatch."
        )
    for heading_variants in required_sections_for_mode(mode):
        present_heading: str | None = None
        body: str | None = None
        for variant in heading_variants:
            body = find_section_body(text, variant)
            if body is not None:
                present_heading = variant
                break
        if present_heading is None:
            failures.append(
                f"required section missing for {mode} mode: "
                f"expected `{' or '.join(heading_variants)}`. "
                "Every canonical refine template requires this section; a brief "
                "without it is structurally incomplete even if other shape "
                "checks pass."
            )
            continue
        if not body.strip():
            failures.append(
                f"required section `{present_heading}` is present but empty. "
                "A heading with no body content fails to satisfy the section's "
                "contract (the downstream lane has no edit target / no acceptance "
                "command / etc. to act on)."
            )

    rows = files_to_modify_rows(text)
    files = sorted({row[0] for row in rows})
    workspace_root = resolve_workspace_root(Path.cwd())
    lint_profile = load_lint_profile(workspace_root)

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

    action_failures, action_warnings = validate_actionable_edit_contract(
        text, rows
    )
    failures.extend(action_failures)
    warnings.extend(action_warnings)

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
            + ". Inlining fixture inventory + nearest existing test case for this language/profile reduces lane discovery cost; the new idiom build can recover without it but the brief is then larger."
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

    new_file_body_gaps = [
        file_path
        for file_path, action in rows
        if (is_newfile_action(action) or is_missing_create_file_row(file_path, action, workspace_root))
        and looks_like_plan_execute_brief(text)
        and is_code_or_test_path(file_path)
        and not has_substantial_new_file_body(text, file_path)
    ]
    if new_file_body_gaps:
        failures.append(
            "plan-execute new-file body missing for required path(s): "
            + ", ".join(new_file_body_gaps)
            + ". Plan-execute is an exact-plan lane; for missing source/test "
            "files, inline the full file body in a ```text fence or a "
            "`replace_all:true`/`new_text` edit shape before dispatch. "
            "Behavior-only implementation shape belongs in an implementation "
            "lane, not a plan-execute atom."
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

    structure_anchor_paths = sorted(structure_describing_anchor_paths(edit_contract_anchor_text(text), files))
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

    failures.extend(language_mismatch_failures(text, rows, lint_profile))
    command_failures, command_warnings = validate_acceptance_command_references(
        text, rows, workspace_root
    )
    failures.extend(command_failures)
    warnings.extend(command_warnings)

    # Canonical anchor verification — file existence + labeled old_text.
    # Resolve workspace from cwd → walk up to a .git root; fall back to cwd.
    # When no named file resolves under the chosen root, WARN explaining the
    # skip rather than flooding existence failures (lint may be running from
    # outside the target repo).
    if rows:
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


def has_allowlist_section(text: str) -> bool:
    lines = text.splitlines()
    in_scope = False
    for line in lines:
        stripped = line.strip()
        if not stripped.startswith("#"):
            continue
        level = heading_level(stripped)
        normalized = normalize_heading(stripped)
        if is_allowlist_heading(stripped):
            return True
        if level == 2:
            in_scope = normalized in SCOPE_HEADINGS
            continue
        if in_scope and level >= 3 and normalized in SCOPE_FILE_HEADINGS:
            return True
    return False


def files_to_modify_rows(text: str) -> list[tuple[str, str]]:
    lines = text.splitlines()
    in_section = False
    in_scope = False
    section_level = 0
    rows: list[tuple[str, str]] = []
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("#"):
            level = heading_level(stripped)
            normalized = normalize_heading(stripped)
            if in_section and level <= section_level:
                break
            if is_allowlist_heading(stripped):
                in_section = True
                section_level = level
                continue
            if level == 2:
                in_scope = normalized in SCOPE_HEADINGS
                continue
            if in_scope and level >= 3 and normalized in SCOPE_FILE_HEADINGS:
                in_section = True
                section_level = level
                continue
        if is_allowlist_heading(stripped):
            in_section = True
            section_level = heading_level(stripped)
            continue
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


def heading_level(line: str) -> int:
    return len(line) - len(line.lstrip("#"))


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
    lower = action.strip().lower()
    if lower.startswith(
        (
            "new file:",
            "new file target:",
            "new file target.",
            "new-file target:",
            "new-file target.",
            "create file:",
            "create new file:",
            "add new file:",
        )
    ):
        return True
    if re.match(r"^new[- ]file\s+target\b", lower):
        return True
    return bool(
        re.match(
            r"^(create|add|write|produce|generate)\s+"
            r"(this\s+|a\s+|an\s+)?new\b.*\b"
            r"(file|script|helper|check|json|document|artifact)\b",
            lower,
        )
    )


def is_missing_create_file_row(
    file_path: str,
    action: str,
    workspace_root: Path | None,
) -> bool:
    if not action or workspace_root is None:
        return False
    lower = action.strip().lower()
    if not re.match(r"^(create|add|write|produce|generate)\b", lower):
        return False
    try:
        return not (workspace_root / file_path).exists()
    except OSError:
        return False


def looks_like_plan_execute_brief(text: str) -> bool:
    scannable = strip_fenced_blocks(text).lower()
    if re.search(r"(?im)^\s*routing:\s*idiom\s+plan-execute\b", scannable):
        return True
    if re.search(r"(?im)^\s*mode:\s*plan-execute\b", scannable):
        return True
    return (
        "plan-then-execute lane brief" in scannable
        or "this run was launched with `idiom plan-execute`" in scannable
    )


def is_code_or_test_path(file_path: str) -> bool:
    path = Path(file_path)
    suffix = path.suffix.lower()
    return suffix in {
        ".c",
        ".cc",
        ".cpp",
        ".cxx",
        ".h",
        ".hh",
        ".hpp",
        ".hxx",
        ".ipp",
        ".inl",
        ".tpp",
        ".gd",
        ".py",
        ".pyi",
        ".ts",
        ".tsx",
        ".js",
        ".jsx",
        ".rs",
        ".java",
        ".kt",
        ".kts",
        ".sh",
        ".bash",
        ".zsh",
        ".go",
    } or file_path.startswith("native/tests/") or "/tests/" in file_path


def has_substantial_new_file_body(text: str, file_path: str) -> bool:
    if has_explicit_replace_all_new_file_shape(text, file_path):
        return True
    name = Path(file_path).name
    suffix = Path(file_path).suffix.lower()
    for match in FENCE_RE.finditer(text):
        fence = match.group(0)
        fence_context = (
            text[max(0, match.start() - 500):match.start()]
            + fence.splitlines()[0]
        )
        if file_path not in fence_context and (not name or name not in fence_context):
            continue
        body = "\n".join(fence.splitlines()[1:-1])
        nonblank = [line for line in body.splitlines() if line.strip()]
        if len(nonblank) < 8:
            continue
        body_low = body.lower()
        if suffix in {".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".ipp", ".inl", ".tpp"}:
            if "#include" in body_low or "test_case" in body_low or "namespace" in body_low:
                return True
        elif suffix == ".gd":
            if "extends " in body_low or "func " in body_low:
                return True
        elif suffix in {".py", ".pyi"}:
            if "def " in body_low or "class " in body_low or "import " in body_low:
                return True
        elif suffix in {".ts", ".tsx", ".js", ".jsx"}:
            if "function " in body_low or "export " in body_low or "const " in body_low:
                return True
        elif suffix in {".sh", ".bash", ".zsh"}:
            if body.startswith("#!") or "\nset -" in body:
                return True
        elif len(nonblank) >= 16:
            return True
    return False


def has_explicit_replace_all_new_file_shape(text: str, file_path: str) -> bool:
    escaped_path = re.escape(file_path)
    escaped_name = re.escape(Path(file_path).name)
    path_pattern = rf"(?:{escaped_path}|{escaped_name})"
    replace_all_pattern = r"replace_all[\"'`\s]*[:=][\"'`\s]*(?:true|True)"
    new_text_pattern = r"new_text[\"'`\s]*[:=]"
    window_pattern = re.compile(
        rf"{path_pattern}.{{0,1600}}{replace_all_pattern}.{{0,1600}}{new_text_pattern}|"
        rf"{replace_all_pattern}.{{0,1600}}{new_text_pattern}.{{0,1600}}{path_pattern}",
        re.DOTALL | re.IGNORECASE,
    )
    return bool(window_pattern.search(text))


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


def edit_contract_anchor_text(text: str) -> str:
    """Return only the action-bearing parts of a brief for anchor-shape lint.

    `Files to Read First` and `Verified Current State` often cite several
    non-adjacent lines in the same file as factual evidence. That is useful
    context, not an instruction to build one multi-line old_text. The
    structure-describing-anchor warning should therefore inspect only the edit
    contract: allowlist rows and required edit/behavior sections.
    """
    parts: list[str] = []
    for heading in (
        "## Files To Modify",
        "## Files to Modify",
        "## Files You May Edit",
        "## Required Edits",
        "## Required Behavior",
    ):
        body = find_section_body(text, heading)
        if body:
            parts.append(body)
    parts.extend(body for _heading, body in required_edit_blocks(text))
    return "\n\n".join(parts)


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


def required_action_sections(text: str) -> list[str]:
    bodies: list[str] = []
    seen: set[str] = set()
    for heading in ("## Required Edits", "## Required Behavior"):
        body = find_section_body(text, heading)
        if body is None:
            continue
        key = body.strip()
        if key and key not in seen:
            bodies.append(body)
            seen.add(key)
    bodies.extend(body for _heading, body in required_edit_blocks(text))
    return bodies


def validate_actionable_edit_contract(
    text: str,
    rows: list[tuple[str, str]],
) -> tuple[list[str], list[str]]:
    """Reject required-edit prose that cannot become a deterministic plan.

    Plan-execute can only dispatch structured edits. If the brief asks the
    planner to run a mutating command or to append/insert from prose, the
    model has to invent anchors at run time; that is exactly the stale-anchor
    loop this lint exists to prevent.
    """
    failures: list[str] = []
    warnings: list[str] = []
    if not rows:
        return failures, warnings
    existing_rows = [(fp, action) for fp, action in rows if not is_newfile_action(action)]
    if not existing_rows:
        return failures, warnings
    sections = required_action_sections(text)
    if not sections:
        return failures, warnings
    action_text = "\n\n".join(sections)
    scannable = strip_fenced_blocks(action_text)
    has_shape = bool(EDIT_SHAPE_RE.search(scannable))
    if COMMAND_MUTATION_RE.search(scannable) and not has_shape:
        failures.append(
            "required edit contract asks the lane to run a mutating command "
            "without an explicit edit shape. Plan-execute cannot execute shell "
            "commands in the planner phase; run the command before dispatch and "
            "inline exact old_text/new_text, start_line/end_line+content, or a "
            "whole-file replacement."
        )
    if PROSE_MUTATION_ANCHOR_RE.search(scannable) and not has_shape:
        failures.append(
            "required edit contract describes a prose-only mutation site "
            "(append/insert/update near another entry) without old_text/new_text "
            "or start_line/end_line+content. Provide an executable edit shape "
            "before dispatch so the planner does not invent stale anchors."
        )
    truncated = truncated_anchor_paths_without_edit_shape(text, [fp for fp, _ in existing_rows], has_shape)
    if truncated:
        failures.append(
            "truncated line evidence cannot serve as an edit anchor for required "
            "path(s): "
            + ", ".join(truncated)
            + ". Replace `...` summaries with exact source text or provide "
            "explicit old_text/new_text or line-range content."
        )
    return failures, warnings


def truncated_anchor_paths_without_edit_shape(
    text: str,
    files: list[str],
    has_shape: bool,
) -> list[str]:
    if has_shape:
        return []
    flagged: list[str] = []
    for file_path in files:
        basename = file_path.rsplit("/", 1)[-1]
        path_pattern = rf"(?:`?{re.escape(file_path)}`?|`?{re.escape(basename)}`?)\s*:\s*\d+"
        if re.search(path_pattern + r"[^`\n]*\.\.\.", text):
            flagged.append(file_path)
    return flagged


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


def load_lint_profile(workspace_root: Path | None) -> dict:
    """Load optional project brief-lint profile.

    The profile is intentionally data-only so projects can declare language and
    acceptance-command conventions without idiom core learning project-specific
    vocabulary. Malformed profiles fall back to defaults; callers still get the
    generic safety net.
    """
    if workspace_root is None:
        return {}
    path = workspace_root / ".idiom" / "brief-lint" / "profile.json"
    if not path.exists():
        return {}
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return {}
    return data if isinstance(data, dict) else {}


def lint_language_rules(profile: dict) -> list[dict]:
    rules = list(DEFAULT_LANGUAGE_RULES)
    profile_rules = profile.get("language_rules", [])
    if isinstance(profile_rules, list):
        rules.extend(rule for rule in profile_rules if isinstance(rule, dict))
    return rules


def language_mismatch_failures(
    text: str,
    rows: list[tuple[str, str]],
    profile: dict,
) -> list[str]:
    if not rows:
        return []
    scan_text = acceptance_and_required_edit_text(text)
    if not scan_text.strip():
        return []
    failures: list[str] = []
    for rule in lint_language_rules(profile):
        language = str(rule.get("language", "")).strip() or "unknown"
        extensions = [str(ext).lower() for ext in rule.get("extensions", [])]
        if not extensions:
            continue
        touched = [
            file_path
            for file_path, _action in rows
            if Path(file_path).suffix.lower() in extensions
        ]
        if not touched:
            continue
        for marker in rule.get("forbidden_acceptance_markers", []):
            marker = str(marker)
            if marker and marker in scan_text:
                failures.append(
                    "language/profile mismatch: "
                    f"{language} file(s) {', '.join(sorted(touched))} are in scope, "
                    f"but acceptance or required edits mention foreign test marker {marker!r}. "
                    "Move this rule into the project brief-lint profile if intentional, "
                    "otherwise use the test idiom for the touched language."
                )
                break
    return failures


def acceptance_and_required_edit_text(text: str) -> str:
    parts: list[str] = []
    for heading in ("## Acceptance", "## Tests", "## Validation"):
        body = find_section_body(text, heading)
        if body:
            parts.append(body)
    parts.extend(body for _heading, body in required_edit_blocks(text))
    return "\n\n".join(parts)


def validate_acceptance_command_references(
    text: str,
    rows: list[tuple[str, str]],
    workspace_root: Path | None,
) -> tuple[list[str], list[str]]:
    body = acceptance_and_required_edit_text(text)
    commands = extract_inline_commands(body)
    if not commands:
        return [], []
    if workspace_root is None:
        return [], [
            "acceptance command reference validation skipped: could not resolve "
            "workspace root from cwd."
        ]
    declared_newfile_targets = declared_newfile_target_paths(
        text, rows, workspace_root
    )
    failures: list[str] = []
    warnings: list[str] = []
    for command in commands:
        for rel in command_reference_paths(command):
            full = workspace_root / rel
            if full.exists():
                continue
            if rel in declared_newfile_targets:
                continue
            failures.append(
                "acceptance command references missing path: "
                f"{rel!r} in `{command}`. Correct the command, add the path as "
                "a new-file target, or mark the validation as blocked with the "
                "exact missing command/path."
            )
    return failures, warnings


def declared_newfile_target_paths(
    text: str,
    rows: list[tuple[str, str]],
    workspace_root: Path,
) -> set[str]:
    targets: set[str] = set()
    for file_path, action in rows:
        if not file_path:
            continue
        try:
            exists = (workspace_root / file_path).exists()
        except OSError:
            exists = False
        if exists:
            continue
        if (
            is_newfile_action(action)
            or has_explicit_replace_all_new_file_shape(text, file_path)
            or has_substantial_new_file_body(text, file_path)
        ):
            targets.add(file_path)
            parent = Path(file_path).parent
            if str(parent) not in {"", "."}:
                targets.add(parent.as_posix())
    return targets


def extract_inline_commands(text: str) -> list[str]:
    commands: list[str] = []
    for raw in re.findall(r"`([^`\n]+)`", text):
        command = raw.strip()
        if not command:
            continue
        first = command.split(maxsplit=1)[0]
        if (
            first in COMMAND_LIKE_PREFIXES
            or command.startswith("./")
            or command.startswith("scripts/")
            or " scripts/" in command
            or " ./scripts/" in command
        ):
            commands.append(command)
    return commands


def command_reference_paths(command: str) -> list[str]:
    try:
        tokens = shlex.split(command)
    except ValueError:
        return []
    paths: list[str] = []
    for token in tokens:
        token = token.strip()
        if not token or token.startswith("-") or "..." in token:
            continue
        if "=" in token and not token.startswith(("./", "scripts/")):
            continue
        rel = token[2:] if token.startswith("./") else token
        rel = rel.strip("'\"")
        if (
            rel in {"", ".", ".."}
            or Path(rel).is_absolute()
            or rel.startswith("../")
            or "://" in rel
        ):
            continue
        if command_token_is_workspace_reference(rel):
            paths.append(rel)
    return sorted(set(paths))


def command_token_is_workspace_reference(token: str) -> bool:
    if token.startswith("scripts/"):
        return True
    if token.startswith(("cmd/", "internal/", "pkg/", "native/", "test/", "tests/")):
        return True
    suffix = Path(token).suffix.lower()
    return "/" in token and suffix in {
        ".bash",
        ".gd",
        ".go",
        ".js",
        ".json",
        ".md",
        ".py",
        ".rs",
        ".sh",
        ".ts",
        ".yaml",
        ".yml",
    }


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

    nonfile_rows = [
        (fp, act)
        for fp, act in rows
        if not is_newfile_action(act)
        and not has_explicit_replace_all_new_file_shape(text, fp)
    ]
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
                failures.append(
                    f"anchor not unique in {section_file} ({count} matches) "
                    f"for '{heading}'. Extend the old_text block until it "
                    "matches exactly once; submit_plan rejects ambiguous "
                    "substring edits before executor writes."
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
