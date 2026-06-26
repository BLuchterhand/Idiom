#!/usr/bin/env python3
"""Build simple manuscript exports from the Markdown source files.

This script intentionally uses only the Python standard library so the book can
be exported in a minimal local environment. It is not a replacement for final
professional layout, but it gives the project a repeatable production source:

- full manuscript Markdown
- print-review HTML
- basic EPUB package
"""

from __future__ import annotations

import html
import os
import pathlib
import re
import shutil
import tempfile
import zipfile
from datetime import datetime, timezone


ROOT = pathlib.Path(__file__).resolve().parents[2]
BOOK = ROOT / ".book"
EXPORTS = BOOK / "exports"

TITLE = "Trust the Process, Not the Prompt"
SUBTITLE = "How to Turn AI Output Into Trusted Work"
AUTHOR = "Benjamin Luchterhand"
BOOK_ID = "urn:uuid:4b9e0b6f-6e5e-4a41-9b68-1a3bc0f0e7de"
COVER_IMAGE = BOOK / "cover.jpg"
EXPORTED_COVER_IMAGE = EXPORTS / "trust-the-process-not-the-prompt-cover.jpg"
DEFAULT_SOURCE_DATE_EPOCH = 1_782_259_200  # 2026-06-24T00:00:00Z
ZIP_TIMESTAMP = (2026, 6, 24, 0, 0, 0)

FRONT = [
    BOOK / "front-back" / "front-matter.md",
    BOOK / "front-back" / "introduction.md",
]

CHAPTERS = [
    BOOK / "chapters" / "00-opening-workflow-teardown.md",
    BOOK / "chapters" / "01-category-error.md",
    BOOK / "chapters" / "02-how-important-work-earns-trust.md",
    BOOK / "chapters" / "03-when-artifact-fixes-do-not-control-the-decision.md",
    BOOK / "chapters" / "04-shape-of-good-ai-task.md",
    BOOK / "chapters" / "05-evidence-freshness-context.md",
    BOOK / "chapters" / "06-responsibility-incentive-split.md",
    BOOK / "chapters" / "07-effect-boundaries-for-ai-workflows.md",
    BOOK / "chapters" / "08-measuring-accepted-verified-output.md",
    BOOK / "chapters" / "09-conditions-for-useful-human-review.md",
    BOOK / "chapters" / "10-from-assistant-to-bounded-agent.md",
    BOOK / "chapters" / "11-evals-incidents-workflow-governance.md",
    BOOK / "chapters" / "12-designing-one-trustworthy-workflow.md",
    BOOK / "chapters" / "13-working-with-the-machine.md",
]

APPENDICES = [
    BOOK / "appendices" / "decision-acceptance-contract.md",
    BOOK / "appendices" / "claim-to-evidence-matrix.md",
    BOOK / "appendices" / "responsibility-incentive-map.md",
    BOOK / "appendices" / "review-capacity-scorecard.md",
    BOOK / "appendices" / "reviewer-packet-template.md",
    BOOK / "appendices" / "effect-boundary-spec.md",
    BOOK / "appendices" / "control-validation-loop.md",
    BOOK / "appendices" / "workflow-readiness-preflight.md",
    BOOK / "appendices" / "vendor-tool-evaluation-checklist.md",
    BOOK / "appendices" / "ai-workflow-antipatterns.md",
    BOOK / "appendices" / "first-workflow-design-canvas.md",
    BOOK / "appendices" / "authority-pace-checklist.md",
    BOOK / "appendices" / "worked-support-credit-packet.md",
    BOOK / "appendices" / "pilot-operating-guide.md",
    BOOK / "appendices" / "term-glossary.md",
]

BACK = [
    BOOK / "front-back" / "bibliography.md",
    BOOK / "front-back" / "back-matter.md",
]


def slugify(text: str, seen: dict[str, int]) -> str:
    slug = re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")
    slug = slug or "section"
    count = seen.get(slug, 0)
    seen[slug] = count + 1
    if count:
        return f"{slug}-{count + 1}"
    return slug


def read(path: pathlib.Path) -> str:
    return path.read_text(encoding="utf-8").strip()


def build_markdown() -> str:
    parts = [
        f"# {TITLE}\n\n## {SUBTITLE}\n\n{AUTHOR}",
        contents_markdown(),
    ]
    for path in FRONT + CHAPTERS + APPENDICES + BACK:
        parts.append(read(path))
    return "\n\n---\n\n".join(parts) + "\n"


def contents_markdown() -> str:
    entries = []
    seen: dict[str, int] = {}
    slugify(TITLE, seen)
    slugify("Contents", seen)
    for entry in source_heading_titles():
        entries.append(f"- [{entry}](#{slugify(entry, seen)})")
    return "# Contents\n\n" + "\n".join(entries)


def source_heading_titles() -> list[str]:
    entries = []
    for path in FRONT + CHAPTERS + APPENDICES + BACK:
        for line in read(path).splitlines():
            if line.startswith("# "):
                entries.append(line.removeprefix("# ").strip())
    return entries


def inline_markup(text: str) -> str:
    escaped = html.escape(text)
    escaped = re.sub(r"\[([^\]]+)\]\((#[^)]+)\)", internal_link, escaped)
    escaped = re.sub(r"`([^`]+)`", r"<code>\1</code>", escaped)
    escaped = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", escaped)
    escaped = re.sub(r"\*([^*]+)\*", r"<em>\1</em>", escaped)
    escaped = re.sub(r"https?://[^\s<\"]+", link_url, escaped)
    return escaped


def internal_link(match: re.Match[str]) -> str:
    label = match.group(1)
    target = match.group(2)
    return f'<a href="{html.escape(target)}">{label}</a>'


def link_url(match: re.Match[str]) -> str:
    url = match.group(0)
    trailing = ""
    while url and url[-1] in ".,);:":
        trailing = url[-1] + trailing
        url = url[:-1]
    return f'<a href="{url}">{url}</a>{trailing}'


def table_to_html(lines: list[str]) -> str:
    rows = []
    for line in lines:
        cells = [cell.strip() for cell in line.strip().strip("|").split("|")]
        if all(re.fullmatch(r":?-{3,}:?", cell or "") for cell in cells):
            continue
        tag = "th" if not rows else "td"
        attrs = ' scope="col"' if tag == "th" else ""
        rows.append(
            "<tr>"
            + "".join(
                f"<{tag}{attrs}>{inline_markup(cell)}</{tag}>" for cell in cells
            )
            + "</tr>"
        )
    return "<table>\n" + "\n".join(rows) + "\n</table>"


def markdown_to_html(markdown: str) -> str:
    out: list[str] = []
    paragraph: list[str] = []
    table: list[str] = []
    in_code = False
    code_lines: list[str] = []
    list_type: str | None = None
    list_item: list[str] = []
    quote_paragraph: list[str] = []
    quote_blocks: list[str] = []
    heading_ids: dict[str, int] = {}

    def close_paragraph() -> None:
        nonlocal paragraph
        if paragraph:
            out.append("<p>" + inline_markup(" ".join(paragraph)) + "</p>")
            paragraph = []

    def close_quote_paragraph() -> None:
        nonlocal quote_paragraph
        if quote_paragraph:
            quote_blocks.append(
                "<p>" + inline_markup(" ".join(quote_paragraph)) + "</p>"
            )
            quote_paragraph = []

    def close_blockquote() -> None:
        nonlocal quote_blocks
        close_quote_paragraph()
        if quote_blocks:
            out.append("<blockquote>\n" + "\n".join(quote_blocks) + "\n</blockquote>")
            quote_blocks = []

    def close_table() -> None:
        nonlocal table
        if table:
            out.append(table_to_html(table))
            table = []

    def close_list_item() -> None:
        nonlocal list_item
        if list_item:
            out.append("<li>" + inline_markup(" ".join(list_item)) + "</li>")
            list_item = []

    def close_list() -> None:
        nonlocal list_type
        close_list_item()
        if list_type:
            out.append(f"</{list_type}>")
            list_type = None

    def open_list(kind: str) -> None:
        nonlocal list_type
        if list_type == kind:
            return
        close_list()
        out.append(f"<{kind}>")
        list_type = kind

    for raw in markdown.splitlines():
        line = raw.rstrip()
        if line.startswith("```"):
            close_blockquote()
            close_paragraph()
            close_table()
            close_list()
            if in_code:
                out.append("<pre><code>" + html.escape("\n".join(code_lines)) + "</code></pre>")
                code_lines = []
                in_code = False
            else:
                in_code = True
            continue
        if in_code:
            code_lines.append(line)
            continue
        if line.startswith(">"):
            close_paragraph()
            close_table()
            close_list()
            quote_text = line[1:].lstrip()
            if quote_text:
                quote_paragraph.append(quote_text)
            else:
                close_quote_paragraph()
            continue
        close_blockquote()
        if not line:
            close_paragraph()
            close_table()
            close_list()
            continue
        if line == "---":
            close_paragraph()
            close_table()
            close_list()
            out.append('<hr class="section-break" />')
            continue
        if line.startswith("|") and line.endswith("|"):
            close_paragraph()
            close_list()
            table.append(line)
            continue
        close_table()
        heading = re.match(r"^(#{1,6})\s+(.+)$", line)
        if heading:
            close_paragraph()
            close_list()
            level = len(heading.group(1))
            heading_text = heading.group(2)
            heading_id = slugify(heading_text, heading_ids)
            out.append(
                f'<h{level} id="{heading_id}">{inline_markup(heading_text)}</h{level}>'
            )
            continue
        if line.startswith("- "):
            close_paragraph()
            open_list("ul")
            close_list_item()
            list_item.append(line[2:])
            continue
        ordered = re.match(r"^\d+\.\s+(.+)$", line)
        if ordered:
            close_paragraph()
            open_list("ol")
            close_list_item()
            list_item.append(ordered.group(1))
            continue
        if list_type and re.match(r"^\s+\S", raw):
            list_item.append(line.strip())
            continue
        paragraph.append(line)

    close_blockquote()
    close_paragraph()
    close_table()
    close_list()
    return "\n".join(out)


def toc_items(body: str) -> list[tuple[str, str]]:
    items: list[tuple[str, str]] = []
    for match in re.finditer(r'<h1 id="([^"]+)">(.+?)</h1>', body):
        heading_id = match.group(1)
        title = re.sub(r"<[^>]+>", "", match.group(2))
        items.append((heading_id, html.unescape(title)))
    return items


def source_toc_items(body: str) -> list[tuple[str, str]]:
    source_items = toc_items(body)[2:]
    nav_titles = [title for _, title in source_items]
    expected_titles = source_heading_titles()
    if nav_titles != expected_titles:
        raise RuntimeError("EPUB nav headings do not match generated Contents entries")
    return source_items


def nav_document(items: list[tuple[str, str]]) -> str:
    links = "\n".join(
        f'    <li><a href="chapter.xhtml#{heading_id}">{html.escape(title)}</a></li>'
        for heading_id, title in items
    )
    return f"""<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="en" xml:lang="en">
<head><title>{html.escape(TITLE)} Contents</title></head>
<body>
  <nav epub:type="toc" id="toc" aria-label="Table of contents">
  <h1>Contents</h1>
  <ol>
{links}
  </ol>
  </nav>
</body>
</html>
"""


def html_document(body: str) -> str:
    return f"""<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <title>{html.escape(TITLE)}</title>
  <style>
    body {{
      font-family: Georgia, 'Times New Roman', serif;
      line-height: 1.45;
      max-width: 760px;
      margin: 48px auto;
      padding: 0 24px;
      color: #111;
    }}
    h1, h2, h3, h4 {{ font-family: Arial, Helvetica, sans-serif; line-height: 1.15; }}
    h1 {{ page-break-before: always; margin-top: 3rem; }}
    h1:first-child {{ page-break-before: auto; }}
    table {{ border-collapse: collapse; width: 100%; margin: 1rem 0; font-size: 0.92rem; }}
    th, td {{ border: 1px solid #999; padding: 0.35rem; vertical-align: top; }}
    blockquote {{ border-left: 3px solid #999; padding-left: 1rem; color: #333; }}
    pre {{ white-space: pre-wrap; background: #f5f5f5; padding: 1rem; }}
    code {{ font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }}
    .section-break {{ border: 0; border-top: 1px solid #ccc; margin: 2.5rem 0; }}
  </style>
</head>
<body>
{body}
</body>
</html>
"""


def write_epub(
    body: str, target: pathlib.Path, cover_image: pathlib.Path | None = None
) -> None:
    if cover_image is None or not cover_image.exists():
        raise FileNotFoundError(f"required cover image not found: {cover_image}")
    source_date_epoch = int(os.environ.get("SOURCE_DATE_EPOCH", DEFAULT_SOURCE_DATE_EPOCH))
    modified = (
        datetime.fromtimestamp(source_date_epoch, timezone.utc)
        .replace(microsecond=0)
        .isoformat()
        .replace("+00:00", "Z")
    )
    nav_items = source_toc_items(body)
    has_cover_image = True
    with tempfile.TemporaryDirectory() as tmp:
        root = pathlib.Path(tmp)
        (root / "META-INF").mkdir()
        (root / "OEBPS").mkdir()
        (root / "OEBPS" / "images").mkdir()
        (root / "mimetype").write_text("application/epub+zip", encoding="utf-8")
        (root / "META-INF" / "container.xml").write_text(
            """<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml" />
  </rootfiles>
</container>
""",
            encoding="utf-8",
        )
        (root / "OEBPS" / "chapter.xhtml").write_text(
            """<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" lang="en" xml:lang="en">
<head>
  <title>"""
            + html.escape(TITLE)
            + """</title>
  <link rel="stylesheet" type="text/css" href="style.css" />
</head>
<body>
"""
            + body
            + """
</body>
</html>
""",
            encoding="utf-8",
        )
        (root / "OEBPS" / "style.css").write_text(
            "body { line-height: 1.45; } table { border-collapse: collapse; width: 100%; } "
            "th, td { border: 1px solid #888; padding: 0.3em; }",
            encoding="utf-8",
        )
        if has_cover_image:
            shutil.copyfile(cover_image, root / "OEBPS" / "images" / "cover.jpg")
        cover_metadata = (
            '    <meta name="cover" content="cover-image" />\n'
            if has_cover_image
            else ""
        )
        cover_manifest = (
            '    <item id="cover-image" href="images/cover.jpg" media-type="image/jpeg" properties="cover-image" />\n'
            if has_cover_image
            else ""
        )
        (root / "OEBPS" / "content.opf").write_text(
            f"""<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" unique-identifier="bookid" version="3.0">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="bookid">{BOOK_ID}</dc:identifier>
    <dc:title id="title">{html.escape(TITLE)}</dc:title>
    <meta refines="#title" property="title-type">main</meta>
    <dc:title id="subtitle">{html.escape(SUBTITLE)}</dc:title>
    <meta refines="#subtitle" property="title-type">subtitle</meta>
    <dc:creator>{html.escape(AUTHOR)}</dc:creator>
    <dc:language>en</dc:language>
{cover_metadata}    <meta property="dcterms:modified">{modified}</meta>
  </metadata>
  <manifest>
{cover_manifest}    <item id="chapter" href="chapter.xhtml" media-type="application/xhtml+xml" />
    <item id="style" href="style.css" media-type="text/css" />
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav" />
  </manifest>
  <spine>
    <itemref idref="chapter" />
  </spine>
</package>
""",
            encoding="utf-8",
        )
        (root / "OEBPS" / "nav.xhtml").write_text(
            nav_document(nav_items),
            encoding="utf-8",
        )
        with zipfile.ZipFile(target, "w") as zf:
            write_zip_file(zf, root / "mimetype", pathlib.Path("mimetype"), zipfile.ZIP_STORED)
            for path in sorted(root.rglob("*")):
                if path.is_file() and path.name != "mimetype":
                    write_zip_file(zf, path, path.relative_to(root), zipfile.ZIP_DEFLATED)


def write_zip_file(
    zf: zipfile.ZipFile,
    source: pathlib.Path,
    archive_name: pathlib.Path,
    compression: int,
) -> None:
    info = zipfile.ZipInfo(str(archive_name).replace("\\", "/"), ZIP_TIMESTAMP)
    info.compress_type = compression
    info.external_attr = 0o644 << 16
    zf.writestr(info, source.read_bytes())


def main() -> None:
    EXPORTS.mkdir(parents=True, exist_ok=True)
    markdown = build_markdown()
    body = markdown_to_html(markdown)
    (EXPORTS / "trust-the-process-not-the-prompt.md").write_text(markdown, encoding="utf-8")
    (EXPORTS / "trust-the-process-not-the-prompt.html").write_text(html_document(body), encoding="utf-8")
    shutil.copyfile(COVER_IMAGE, EXPORTED_COVER_IMAGE)
    write_epub(body, EXPORTS / "trust-the-process-not-the-prompt.epub", COVER_IMAGE)
    cover = BOOK / "cover.svg"
    if cover.exists():
        shutil.copyfile(cover, EXPORTS / "cover.svg")
    print(f"Wrote exports to {EXPORTS}")


if __name__ == "__main__":
    main()
