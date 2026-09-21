#!/usr/bin/env python3
"""Turn a pile of raw English .txt files into the corpus JSON this project imports.

The output is *not* the final form: it is the input to `articlesync -materialize`,
which writes it into the content tree as one JSON file per article. Those files,
not this document and not the database, are the source of truth from then on.

Body text is stored as *paragraphs*; sentences are split on the fly by the
backend (`internal/sentence`), and annotations are anchored to
`article_id + paragraph_hash + sentence_index + word_index`, where the hash is
of the paragraph text itself. That has three consequences which drive every
cleaning rule below:

  1. A hard-wrapped line inside a paragraph becomes a sentence boundary, because
     the splitter flushes on "\\n". Wrapped lines MUST be joined first, or the
     reader shows fragments like "and the Fox had to jump for it." as if they
     were sentences — and those bogus anchors are what users annotate.
  2. A paragraph must be one logical paragraph, never a whole chapter: it is
     stored as a single JSON string and read into memory whole. The scanner
     rejects anything over 60000 bytes (MySQL TEXT is 65535 *bytes*).
  3. Anything that survives into the text is what the user clicks on, so page
     numbers, running heads and Gutenberg boilerplate have to go before import.

`paragraph_count` and `sentence_counts` are deliberately NOT emitted: the scanner
recomputes them with the Go splitter and rejects a file whose declaration
disagrees, so producing them here would mean maintaining the sentence-splitting
algorithm twice, in two languages. `articlesync -materialize` fills them in.

Output shape (the corpus schema `content.LoadCorpus` accepts):

    [ { slug, title, description, emoji, color,
        articles: [ { title, subtitle, level, author, origin, paragraphs: [...] } ] } ]

Usage
-----
    # one .txt = one article (essays, short stories, news)
    python3 tools/txt2articles.py corpus/ -o corpus.json \
        --dataset-slug essays --dataset-title 随笔选 --emoji ✍️

    # one .txt = one book, split into an article per chapter
    python3 tools/txt2articles.py books/ -o corpus.json --mode book \
        --dataset-slug novels --dataset-title 小说

    # inspect what would change without writing anything
    python3 tools/txt2articles.py corpus/ -o /tmp/x.json --dry-run

    # then: cd backend && go run ./cmd/articlesync -materialize ../corpus.json
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

# --------------------------------------------------------------------------
# Field limits from internal/store/models.go — exceeding them fails on MySQL
# (STRICT_TRANS_TABLES rejects over-long values instead of truncating).
# --------------------------------------------------------------------------
LIMITS = {
    "dataset.slug": 96,
    "dataset.title": 191,
    "dataset.description": 512,
    "dataset.emoji": 16,
    "dataset.color": 32,
    "article.title": 512,
    "article.subtitle": 512,
    "article.level": 64,
}
# MySQL TEXT is 65535 *bytes*; leave room for the utf8mb4 worst case.
MAX_PARAGRAPH_BYTES = 60000

GUTENBERG_START = re.compile(
    r"^\*\*\*\s*START OF (?:THE|THIS) PROJECT GUTENBERG.*?\*\*\*\s*$",
    re.IGNORECASE | re.MULTILINE,
)
GUTENBERG_END = re.compile(
    r"^\*\*\*\s*END OF (?:THE|THIS) PROJECT GUTENBERG.*?\*\*\*\s*$",
    re.IGNORECASE | re.MULTILINE,
)
# Trailing licence blocks / "Produced by" credits that sit outside the markers.
BOILERPLATE_LINE = re.compile(
    r"^\s*(?:produced by|transcribed from|updated editions will|"
    r"most recently updated|character set encoding|end of (?:the )?project gutenberg|"
    r"this ebook is for the use of anyone anywhere)",
    re.IGNORECASE,
)
# A line that is nothing but a page number, or a roman-numeral leaf number.
PAGE_NUMBER_LINE = re.compile(r"^\s*(?:\d{1,4}|[ivxlcdm]{1,7})\s*$", re.IGNORECASE)
# Leading "Title: ...", "Author: ..." metadata blocks that many .txt dumps carry
# above the actual text.
METADATA_LINE = re.compile(
    r"^\s*(?P<key>title|author|translator|editor|illustrator|release date|posting date|"
    r"language|subject|source|credits?|encoding)\s*:\s*(?P<value>.*)$",
    re.IGNORECASE,
)
# "The Project Gutenberg eBook of <title>" is itself the title line of many dumps.
GUTENBERG_TITLE_LINE = re.compile(
    r"^\s*the project gutenberg ebook of\s+(?P<title>.+?)\s*$", re.IGNORECASE
)
# Metadata keys mapped onto the fields an article file understands.
META_TITLE = "title"
META_AUTHOR = "author"
META_DATE = "release date"
# Chapter / part headings used by --mode book.
CHAPTER_HEADING = re.compile(
    r"^\s*(?:chapter|part|book|letter|section)\b[\s.:IVXLC0-9-]*.*$",
    re.IGNORECASE,
)
ALLCAPS_HEADING = re.compile(r"^[^a-z]{4,60}$")

# Characters that must never reach the database.
ZERO_WIDTH = dict.fromkeys(map(ord, "\u200b\u200c\u200d\ufeff\u00ad"), None)

TYPOGRAPHY = {
    "\u2018": "'", "\u2019": "'", "\u201a": "'", "\u201b": "'",
    "\u201c": '"', "\u201d": '"', "\u201e": '"', "\u201f": '"',
    "\u2013": "-", "\u2014": "--", "\u2212": "-",
    "\u2026": "...", "\u00a0": " ", "\u2009": " ", "\u202f": " ",
    "\u2002": " ", "\u2003": " ", "\u2007": " ", "\u2008": " ",
}


class Report:
    """Collects per-file cleaning decisions so nothing is silently mangled."""

    def __init__(self) -> None:
        self.lines: list[str] = []
        self.warnings: list[str] = []

    def info(self, msg: str) -> None:
        self.lines.append(msg)

    def warn(self, msg: str) -> None:
        self.warnings.append(msg)
        self.lines.append("WARN  " + msg)

    def dump(self) -> None:
        for line in self.lines:
            print(line, file=sys.stderr)


def decode(raw: bytes, path: Path, rep: Report) -> str:
    """Decode UTF-8, falling back to Windows-1252 for legacy Gutenberg dumps."""
    for enc in ("utf-8-sig", "utf-8"):
        try:
            return raw.decode(enc)
        except UnicodeDecodeError:
            pass
    try:
        text = raw.decode("cp1252")
    except UnicodeDecodeError:
        text = raw.decode("latin-1")
    rep.warn(f"{path.name}: not valid UTF-8, decoded as cp1252/latin-1 — check for mojibake")
    return text


def extract_metadata(text: str, path: Path, rep: Report) -> tuple[dict, str]:
    """Pull the leading "Title:/Author:/Release Date:" block out of the text.

    These lines are boilerplate for the reader, but their values are exactly the
    metadata an article file wants — so capture them instead of dropping them.
    Returns the metadata found and the text with those lines removed.
    """
    lines = text.split("\n")
    meta: dict[str, str] = {}
    cut = 0
    for i, line in enumerate(lines[:40]):
        if not line.strip():
            # Blank lines are skipped rather than treated as the end of the
            # block: plain-text dumps routinely separate "The Project Gutenberg
            # eBook of ..." from the "Title:/Author:" lines with one.
            continue
        gutenberg = GUTENBERG_TITLE_LINE.match(line)
        if gutenberg:
            meta.setdefault(META_TITLE, gutenberg.group("title").strip())
            cut = i + 1
            continue
        found = METADATA_LINE.match(line)
        if found:
            meta.setdefault(found.group("key").lower(), found.group("value").strip())
            cut = i + 1
            continue
        break
    if not cut:
        return meta, text
    if meta:
        rep.info(f"{path.name}: captured metadata ({', '.join(sorted(meta))})")
    return meta, "\n".join(lines[cut:])


def strip_gutenberg(text: str, path: Path, rep: Report) -> str:
    """Remove Project Gutenberg headers/footers if present."""
    start = GUTENBERG_START.search(text)
    if start:
        text = text[start.end():]
        rep.info(f"{path.name}: stripped Gutenberg header")
    end = GUTENBERG_END.search(text)
    if end:
        text = text[: end.start()]
        rep.info(f"{path.name}: stripped Gutenberg footer")
    if start is None and end is None:
        # No markers: at least drop a leading "Produced by ..." credit block.
        lines = text.split("\n")
        cut = 0
        for i, line in enumerate(lines[:40]):
            if not line.strip():
                if cut:
                    cut = i + 1
                    break
                continue
            if BOILERPLATE_LINE.match(line) or METADATA_LINE.match(line):
                cut = i + 1
                continue
            break
        if cut:
            text = "\n".join(lines[cut:])
            rep.info(f"{path.name}: dropped {cut} leading boilerplate line(s)")
    return text


def clean_lines(text: str, opts: argparse.Namespace, path: Path, rep: Report) -> list[str]:
    """Normalise characters and drop non-prose lines. Returns surviving lines."""
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    # A form feed marks a page boundary, not a paragraph boundary: turning it
    #    into a blank line splits any paragraph that happens to span two pages.
    #    A single newline keeps the reflow able to rejoin it.
    text = text.replace("\f", "\n")
    text = text.translate(ZERO_WIDTH)
    if not opts.keep_typography:
        for src, dst in TYPOGRAPHY.items():
            text = text.replace(src, dst)
    # Tabs and stray vertical whitespace are not meaningful in prose.
    text = text.replace("\t", " ").replace("\v", "\n")

    kept: list[str] = []
    dropped_numbers = 0
    for line in text.split("\n"):
        stripped = line.strip()
        if not stripped:
            kept.append("")
            continue
        if opts.strip_page_numbers and PAGE_NUMBER_LINE.match(stripped):
            dropped_numbers += 1
            continue
        if BOILERPLATE_LINE.match(stripped):
            continue
        kept.append(stripped)
    if dropped_numbers:
        rep.info(f"{path.name}: dropped {dropped_numbers} page-number line(s)")
    return kept


def join_wrapped_lines(lines: list[str], opts: argparse.Namespace) -> list[str]:
    """Reflow hard-wrapped lines into logical paragraphs.

    Default (blank-line) mode: a blank line ends a paragraph, and consecutive
    non-blank lines are one wrapped paragraph. That is what plain-text editions
    and ``pdftotext`` produce.

    ``--line-per-paragraph`` mode: every non-blank line is its own paragraph.
    Use it for extractors that emit one newline per paragraph and never hard-wrap
    — ``textutil -convert txt`` on .docx/.rtf, or LibreOffice --convert-to txt.
    Without it those documents collapse into a single giant paragraph, because
    they contain no blank lines at all.

    A line starting with 4+ spaces, or a short CHAPTER/ALL-CAPS heading, also
    starts a new paragraph in either mode.
    """
    paragraphs: list[str] = []
    buf: list[str] = []

    def flush() -> None:
        if buf:
            paragraphs.append(" ".join(buf))
            buf.clear()

    for line in lines:
        if not line:
            flush()
            continue
        indented = line.startswith("    ") or line.startswith("  ") and opts.indent_starts_paragraph
        headingish = bool(CHAPTER_HEADING.match(line)) or bool(ALLCAPS_HEADING.match(line))
        if opts.line_per_paragraph:
            flush()
            paragraphs.append(line.strip())
            continue
        if (indented or headingish) and buf:
            flush()
        buf.append(line.strip())
    flush()
    return paragraphs


def fix_hyphenation(text: str) -> tuple[str, int]:
    """Join words broken across lines by a hyphen: "exam-\\nple" -> "example".

    Runs while the text is still newline-separated, i.e. BEFORE reflowing, and
    only joins when the continuation is lowercase — genuine compounds such as
    "well-\\nKnown" are left alone.
    """
    count = 0

    def repl(match: re.Match[str]) -> str:
        nonlocal count
        count += 1
        return match.group(1) + match.group(2)

    # "exam-\nple" -> "example"; requires lowercase continuation.
    fixed = re.sub(r"([A-Za-z])-\n([a-z])", repl, text)
    return fixed, count


def normalize_paragraph(p: str) -> str:
    """Collapse internal whitespace runs; guarantee no newlines/tabs remain."""
    p = re.sub(r"\s+", " ", p)
    return p.strip()


def split_chapters(paragraphs: list[str], opts: argparse.Namespace) -> list[tuple[str, list[str]]]:
    """Group paragraphs into (title, body) pairs for --mode book."""
    chapters: list[tuple[str, list[str]]] = []
    title: str | None = None
    body: list[str] = []

    def flush() -> None:
        if body:
            chapters.append((title or "", body[:]))
            body.clear()

    for para in paragraphs:
        looks_like_heading = bool(CHAPTER_HEADING.match(para)) and len(para) <= 80
        if looks_like_heading:
            flush()
            title = para.rstrip(".:").strip()
            continue
        body.append(para)
    flush()
    return chapters


def validate_paragraph(p: str, path: Path, rep: Report) -> bool:
    """Return True when the paragraph is safe to store."""
    if "\n" in p or "\t" in p:
        rep.warn(f"{path.name}: paragraph still contains a newline/tab, dropped")
        return False
    size = len(p.encode("utf-8"))
    if size > MAX_PARAGRAPH_BYTES:
        rep.warn(
            f"{path.name}: paragraph is {size} bytes (> MySQL TEXT limit "
            f"{MAX_PARAGRAPH_BYTES}); split it at a natural break"
        )
        return False
    return True


def cap(value: str, field: str, path: Path, rep: Report) -> str:
    limit = LIMITS[field]
    if len(value) <= limit:
        return value
    rep.warn(f"{path.name}: {field} truncated from {len(value)} to {limit} chars")
    return value[: limit - 1].rstrip() + "…"


def build_article(
    paragraphs: list[str], title: str, opts: argparse.Namespace, path: Path, rep: Report,
    meta: dict | None = None,
) -> dict | None:
    body = [p for p in paragraphs if validate_paragraph(p, path, rep)]
    body = [p for p in body if len(p) >= opts.min_paragraph_chars]
    if not body:
        rep.warn(f"{path.name}: no usable paragraphs after cleaning, skipped")
        return None
    meta = meta or {}
    article = {
        "title": cap(title, "article.title", path, rep),
        "subtitle": cap(opts.subtitle or "", "article.subtitle", path, rep),
        "level": cap(opts.level, "article.level", path, rep),
        "paragraphs": body,
    }
    # Carry the document's own metadata through when it declared any.
    if meta.get(META_AUTHOR):
        article["author"] = meta[META_AUTHOR]
    if opts.origin:
        article["origin"] = opts.origin
    return article


def clean_file(path: Path, opts: argparse.Namespace, rep: Report) -> list[dict]:
    raw = path.read_bytes()
    text = decode(raw, path, rep)
    meta, text = extract_metadata(text, path, rep)
    text = strip_gutenberg(text, path, rep)

    text, hyphen_fixes = fix_hyphenation(text)
    if hyphen_fixes:
        rep.info(f"{path.name}: rejoined {hyphen_fixes} end-of-line hyphen(s)")

    lines = clean_lines(text, opts, path, rep)
    paragraphs = [normalize_paragraph(p) for p in join_wrapped_lines(lines, opts)]
    paragraphs = [p for p in paragraphs if p]

    if opts.mode == "book":
        chapters = split_chapters(paragraphs, opts)
        if len(chapters) <= 1:
            rep.warn(
                f"{path.name}: --mode book found no chapter headings; "
                "imported as a single article (set --split-regex if your edition differs)"
            )
        articles = []
        for i, (chapter_title, body) in enumerate(chapters, start=1):
            title = chapter_title or meta.get(META_TITLE) or f"{path.stem} ({i})"
            art = build_article(body, title, opts, path, rep, meta)
            if art:
                articles.append(art)
        return articles

    # Title preference: explicit flag, then the document's own Title:, then the
    # file name.
    title = opts.title_from or meta.get(META_TITLE) or path.stem
    art = build_article(paragraphs, title, opts, path, rep, meta)
    return [art] if art else []


def main() -> int:
    ap = argparse.ArgumentParser(
        description="Clean raw .txt into the corpus JSON this project imports.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
    )
    ap.add_argument("input", type=Path, help="file or directory of .txt files")
    ap.add_argument("-o", "--out", type=Path, required=True, help="output corpus JSON")
    ap.add_argument("--dataset-slug", default="imported", help="dataset slug (unique, <=96 chars)")
    ap.add_argument("--dataset-title", default="导入语料", help="dataset display title")
    ap.add_argument("--dataset-description", default="", help="dataset description")
    ap.add_argument("--emoji", default="📚", help="dataset emoji (needs utf8mb4)")
    ap.add_argument("--color", default="#6366f1", help="dataset accent colour")
    ap.add_argument(
        "--mode", choices=("article", "book"), default="article",
        help="article: one .txt = one article; book: split each .txt by chapter headings",
    )
    ap.add_argument("--level", default="入门", help="level label shown in the UI")
    ap.add_argument("--subtitle", default="", help="subtitle applied to every article")
    ap.add_argument("--title-from", default="", help="article title override (single-file input)")
    ap.add_argument("--origin", default="", help="source URL or edition note applied to every article")
    ap.add_argument("--min-paragraph-chars", type=int, default=2,
                    help="drop paragraphs shorter than this (kills stray fragments)")
    ap.add_argument("--keep-typography", action="store_true",
                    help="keep curly quotes/dashes instead of normalising to ASCII")
    ap.add_argument("--no-page-number-strip", dest="strip_page_numbers",
                    action="store_false", help="keep lines that are only a number")
    ap.add_argument("--line-per-paragraph", action="store_true",
                    help="treat every non-blank line as its own paragraph: for extractors that "
                         "emit one newline per paragraph and never hard-wrap "
                         "(textutil on .docx/.rtf, LibreOffice --convert-to txt)")
    ap.add_argument("--indent-starts-paragraph", action="store_true", default=True,
                    help="treat indented lines as new paragraphs (default)")
    ap.add_argument("--split-regex", default="",
                    help="custom chapter-heading regex for --mode book")
    ap.add_argument("--dry-run", action="store_true", help="analyse only, write nothing")
    opts = ap.parse_args()

    if opts.split_regex:
        global CHAPTER_HEADING
        CHAPTER_HEADING = re.compile(opts.split_regex, re.IGNORECASE)

    files = sorted(opts.input.rglob("*.txt")) if opts.input.is_dir() else [opts.input]
    if not files:
        print(f"no .txt files under {opts.input}", file=sys.stderr)
        return 2

    rep = Report()
    articles: list[dict] = []
    for path in files:
        articles.extend(clean_file(path, opts, rep))

    if not articles:
        rep.dump()
        print("nothing usable was produced", file=sys.stderr)
        return 1

    dataset = {
        "slug": cap(opts.dataset_slug, "dataset.slug", Path(opts.dataset_slug), Report()),
        "title": cap(opts.dataset_title, "dataset.title", Path(opts.dataset_slug), Report()),
        "description": cap(opts.dataset_description, "dataset.description", Path("dataset"), Report()),
        "emoji": opts.emoji,
        "color": opts.color,
        "articles": articles,
    }
    if len(opts.emoji) > LIMITS["dataset.emoji"]:
        rep.warn(f"emoji longer than {LIMITS['dataset.emoji']} chars, MySQL will reject it")

    total_paras = sum(len(a["paragraphs"]) for a in articles)
    total_bytes = sum(len(p.encode("utf-8")) for a in articles for p in a["paragraphs"])

    if opts.dry_run:
        rep.info("dry run: no file written")
    else:
        opts.out.parent.mkdir(parents=True, exist_ok=True)
        opts.out.write_text(
            json.dumps([dataset], ensure_ascii=False, indent=1) + "\n", encoding="utf-8"
        )

    rep.dump()
    print(
        f"\n{len(files)} file(s) -> {len(articles)} article(s), "
        f"{total_paras} paragraph(s), {total_bytes / 1024:.1f} KiB of text",
        file=sys.stderr,
    )
    if rep.warnings:
        print(f"{len(rep.warnings)} warning(s) above — read them before importing.", file=sys.stderr)
    if not opts.dry_run:
        print(f"wrote {opts.out}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
