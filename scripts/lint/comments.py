#!/usr/bin/env -S uv run --quiet --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["tree-sitter-language-pack==0.9.1"]
# ///
"""Comment ratchet: inventory, check, tighten, and delete source comments.

Canonical copy: ~/dev/harbor/bin/comments. Projects vendor it byte for byte as
scripts/lint/comments.py; harbor's doctor.sh flags a copy that drifts.
"""
import argparse
import json
import re
import subprocess
import sys
from dataclasses import asdict, dataclass
from pathlib import Path

from tree_sitter_language_pack import get_parser

LANGS = {
    ".dart": "dart", ".ts": "typescript", ".mts": "typescript", ".cts": "typescript",
    ".tsx": "tsx", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript",
    ".jsx": "javascript", ".go": "go", ".py": "python", ".sh": "bash", ".bash": "bash",
    ".sql": "sql", ".swift": "swift", ".kt": "kotlin", ".kts": "kotlin", ".rs": "rust",
    ".css": "css",
}
GENERATED_PATH = re.compile(
    r"(\.g\.dart|\.freezed\.dart|\.gr\.dart|\.mocks\.dart|\.pb\.go|routeTree\.gen\.ts"
    r"|worker-configuration\.d\.ts|(^|/)third_party/|(^|/)node_modules/"
    r"|(^|/)vendor/|\.min\.js$|(^|/)l10n/app_localizations.*\.dart$"
    r"|GeneratedPluginRegistrant|(^|/)(ios|macos)/Flutter/|(^|/)(windows|linux)/flutter/"
    r"|(^|/)migrations/.*\.sql$)"
)
GENERATED_HEAD = re.compile(
    r"DO NOT EDIT|DO NOT MODIFY|@generated"
    r"|^\s*(//+|#|--|/?\*+)\s*(GENERATED|[Aa]uto-?generated|This file (is|was) (automatically )?generated)",
    re.M,
)
DIRECTIVE = re.compile(
    r"^\s*(#!|//\s*ignore(_for_file)?:|//\s*dart format|/[/*]\s*(es|ox)lint-|biome-ignore"
    r"|prettier-ignore|@ts-(ignore|expect-error|nocheck|check)|//\s*@vitest-|/// ?<reference"
    r"|/\*\*?\s*@jsx|//\s*@refresh|//go:|//\s*\+build|//nolint|//export |#\s*noqa"
    r"|#\s*type:\s*ignore|#\s*pyright:|#\s*mypy:|#\s*fmt:|#\s*ruff:|#\s*pylint:"
    r"|#\s*shellcheck |#.*-\*-.*-\*-|//\s*swiftlint:|//\s*swift-format|//\s*coverage:"
    r"|/[/*]\s*(istanbul|c8|v8) ignore|--\s*(migrate|\+goose|plpgsql_check)"
    r"|/\*\s*webpackChunkName|#\s*syntax=|-->\s*statement-breakpoint|/\*\s*[@#]__(PURE|NO_SIDE_EFFECTS)__)",
    re.I,
)
LICENSE = re.compile(r"copyright|license|spdx", re.I)
COUNTED = ("comment", "doc", "docstring")
AAA_WORD = r"(arrange|act|assert|given|when|then)"
AAA = re.compile(rf"^\s*(//+|#|--)\s*{AAA_WORD}(\s*(&|and|/|,|\+|-)\s*{AAA_WORD})*\s*[:.]?\s*$", re.I)
RULE = re.compile(r"^\s*(//+|#+|--|/?\*+/?)\s*(.*?)\s*$")
RULE_CHARS = re.compile(r"[─━═=\-*#~_+]{3,}")
MARK = re.compile(r"^\s*//\s*MARK:|^\s*(//|#)\s*#?(end)?region\b", re.I)


@dataclass
class Block:
    path: str
    start: int
    end: int
    col: int
    own: bool
    trailing: bool
    kind: str
    sole: bool
    text: str

    @property
    def lines(self) -> int:
        return self.end - self.start + 1


def repo_root(cwd: Path) -> Path:
    out = subprocess.run(["git", "-C", str(cwd), "rev-parse", "--show-toplevel"],
                         capture_output=True, text=True, check=True)
    return Path(out.stdout.strip())


def tracked(root: Path) -> list[str]:
    out = subprocess.run(["git", "-C", str(root), "ls-files", "-co", "--exclude-standard"],
                         capture_output=True, text=True, check=True)
    return out.stdout.splitlines()


def comment_nodes(root):
    stack = [root]
    while stack:
        n = stack.pop()
        if "comment" in n.type and n.type != "comment_content":
            yield n
            continue
        stack.extend(reversed(n.children))


def docstring_nodes(root):
    stack = [root]
    while stack:
        n = stack.pop()
        if n.type in ("module", "block"):
            body = [c for c in n.children if c.type != "comment"]
            first = body[0] if body else None
            if (first is not None and first.type == "expression_statement" and first.children
                    and first.children[0].type == "string"):
                yield first, len(body) == 1
        stack.extend(n.children)


def go_doc(lines: list[str], end: int) -> bool:
    nxt = lines[end] if end < len(lines) else ""
    return bool(re.match(r"^(func|type|var|const|package)\b", nxt))


def scan(root: Path, rel: str, lang: str) -> list[Block]:
    src = (root / rel).read_bytes()
    if GENERATED_HEAD.search(src[:600].decode("utf8", "replace")):
        return []
    tree = get_parser(lang).parse(src)
    lines = src.decode("utf8", "replace").split("\n")
    found = [(n, False, False) for n in comment_nodes(tree.root_node)]
    if lang == "python":
        found += [(n, True, sole) for n, sole in docstring_nodes(tree.root_node)]
    raw: list[Block] = []
    for node, is_doc, sole in found:
        (sr, sc), (er, ec) = node.start_point, node.end_point
        text = src[node.start_byte:node.end_byte].decode("utf8", "replace")
        own = lines[sr][:sc].strip() == ""
        trailing = lines[er][ec:].strip() == "" if er < len(lines) else True
        raw.append(Block(rel, sr + 1, er + 1, sc, own, trailing,
                         "docstring" if is_doc else "comment", sole, text))
    raw.sort(key=lambda b: (b.start, b.col))
    merged: list[Block] = []
    for b in raw:
        prev = merged[-1] if merged else None
        if (prev and b.own and prev.own and prev.kind == b.kind == "comment"
                and b.start == prev.end + 1 and b.col == prev.col
                and not DIRECTIVE.search(b.text) and not DIRECTIVE.search(prev.text.split("\n")[-1])):
            prev.end, prev.text, prev.trailing = b.end, prev.text + "\n" + b.text, b.trailing
        else:
            merged.append(b)
    for b in merged:
        if b.kind == "docstring":
            continue
        first = b.text.lstrip()
        if DIRECTIVE.search(first):
            b.kind = "directive"
        elif b.start <= 3 and LICENSE.search(b.text):
            b.kind = "license"
        elif first.startswith(("///", "/**")) or (lang == "go" and b.own and go_doc(lines, b.end)):
            b.kind = "doc"
    return merged


def scan_repo(root: Path, exclude: list[str]) -> list[Block]:
    extra = [re.compile(x) for x in exclude]
    blocks: list[Block] = []
    for rel in tracked(root):
        lang = LANGS.get(Path(rel).suffix)
        if not lang or GENERATED_PATH.search(rel) or any(x.search(rel) for x in extra):
            continue
        if not (root / rel).is_file():
            continue
        blocks.extend(scan(root, rel, lang))
    return blocks


def is_mechanical(b: Block, pattern: str) -> bool:
    if b.kind not in COUNTED or not b.own:
        return False
    if pattern == "aaa":
        return bool(AAA.match(b.text))
    if pattern == "banner":
        rows = b.text.split("\n")
        if MARK.match(rows[0]) and len(rows) == 1:
            return True
        bodies = [(RULE.match(r).group(2) if RULE.match(r) else r) for r in rows]
        rules = [x for x in bodies if RULE_CHARS.search(x)]
        words = [x for x in bodies if x and not RULE_CHARS.fullmatch(x)]
        if not rules:
            return False
        titles = [RULE_CHARS.sub("", x).strip(" .:") for x in words]
        return len(titles) <= 1 and all(len(t) <= 60 and not t.endswith((".", "!", "?")) for t in titles) \
            and all(len(RULE_CHARS.sub("", x).strip()) <= 60 for x in bodies)
    raise SystemExit(f"unknown pattern: {pattern}")


def per_file(blocks: list[Block]) -> dict[str, int]:
    counts: dict[str, int] = {}
    for b in blocks:
        if b.kind in COUNTED:
            counts[b.path] = counts.get(b.path, 0) + b.lines
    return counts


def load_baseline(path: Path) -> dict:
    if not path.exists():
        return {"exclude": [], "files": {}}
    return json.loads(path.read_text())


def save_baseline(path: Path, data: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    data["files"] = dict(sorted((k, v) for k, v in data["files"].items() if v > 0))
    path.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n")


def delete_blocks(root: Path, targets: list[Block]) -> list[str]:
    refused: list[str] = []
    by_file: dict[str, list[Block]] = {}
    for b in targets:
        by_file.setdefault(b.path, []).append(b)
    for rel, blocks in by_file.items():
        path = root / rel
        lines = path.read_text().split("\n")
        for b in sorted(blocks, key=lambda x: (x.start, x.col), reverse=True):
            if b.kind == "docstring" and b.sole:
                refused.append(f"{rel}:{b.start} docstring is the only statement of its body")
                continue
            if b.own and b.trailing:
                del lines[b.start - 1:b.end]
                i = b.start - 1
                if 0 < i < len(lines) and lines[i - 1].strip() == "" and lines[i].strip() == "":
                    del lines[i]
                elif i == 0 and lines and lines[0].strip() == "":
                    del lines[0]
            elif not b.own and b.trailing and b.start == b.end:
                lines[b.start - 1] = lines[b.start - 1][:b.col].rstrip()
            else:
                refused.append(f"{rel}:{b.start} comment shares its line with code after it")
        path.write_text("\n".join(lines))
    return refused


def code_without_comments(src: bytes, lang: str) -> bytes:
    tree = get_parser(lang).parse(src)
    spans = [(n.start_byte, n.end_byte) for n in comment_nodes(tree.root_node)]
    if lang == "python":
        spans += [(n.start_byte, n.end_byte) for n, _ in docstring_nodes(tree.root_node)]
    out, pos = bytearray(), 0
    for a, b in sorted(spans):
        if a >= pos:
            out += src[pos:a]
            pos = b
    out += src[pos:]
    return re.sub(rb"\s+", b"", bytes(out))


def only_comments_changed(root: Path, base: str) -> list[str]:
    names = subprocess.run(["git", "-C", str(root), "diff", "--name-only", "--diff-filter=M", base],
                           capture_output=True, text=True, check=True).stdout.split()
    changed = []
    for rel in names:
        lang = LANGS.get(Path(rel).suffix)
        if not lang:
            continue
        old = subprocess.run(["git", "-C", str(root), "show", f"{base}:{rel}"], capture_output=True,
                             check=True).stdout
        if code_without_comments(old, lang) != code_without_comments((root / rel).read_bytes(), lang):
            changed.append(rel)
    return changed


def main() -> int:
    ap = argparse.ArgumentParser(prog="comments", description=__doc__)
    ap.add_argument("--root", type=Path, default=None)
    ap.add_argument("--baseline", type=Path, default=None)
    sub = ap.add_subparsers(dest="cmd", required=True)
    inv = sub.add_parser("inventory", help="summary, or every block as JSON")
    inv.add_argument("--json", action="store_true")
    inv.add_argument("paths", nargs="*")
    sub.add_parser("check", help="fail when a file's comment lines differ from the baseline")
    up = sub.add_parser("update", help="lower the baseline to the current counts")
    up.add_argument("--raise", dest="raise_", nargs="*", default=[], metavar="PATH",
                    help="also allow these files to go up (shows in the baseline diff)")
    st = sub.add_parser("strip", help="delete every block matching a mechanical pattern")
    st.add_argument("--pattern", action="append", choices=["aaa", "banner"], required=True)
    st.add_argument("--dry-run", action="store_true")
    de = sub.add_parser("delete", help="delete the blocks starting at FILE:LINE")
    de.add_argument("targets", nargs="+")
    oc = sub.add_parser("only-comments", help="fail when a file differs from BASE in more than comments")
    oc.add_argument("base")
    args = ap.parse_args()

    root = (args.root or repo_root(Path.cwd())).resolve()
    baseline_path = args.baseline or root / "scripts/lint/comments-baseline.json"
    baseline = load_baseline(baseline_path)
    blocks = scan_repo(root, baseline.get("exclude", []))

    if args.cmd == "inventory":
        if args.paths:
            blocks = [b for b in blocks if any(b.path.startswith(p) for p in args.paths)]
        if args.json:
            json.dump([asdict(b) | {"lines": b.lines} for b in blocks], sys.stdout, ensure_ascii=False)
            return 0
        tally: dict[str, list[int]] = {}
        for b in blocks:
            t = tally.setdefault(b.kind, [0, 0])
            t[0] += 1
            t[1] += b.lines if b.own else 0
        for kind, (n, own_lines) in sorted(tally.items()):
            print(f"{kind:10} {n:7} blocks {own_lines:8} own lines")
        return 0

    if args.cmd == "only-comments":
        changed = only_comments_changed(root, args.base)
        for rel in changed:
            print(f"{rel}: code changed, not only comments")
        return 1 if changed else 0

    current = per_file(blocks)
    if args.cmd == "check":
        allowed = baseline["files"]
        worse = sorted(p for p, n in current.items() if n > allowed.get(p, 0))
        stale = sorted(p for p, n in allowed.items() if current.get(p, 0) < n)
        for p in worse:
            print(f"{p}: {current[p]} comment lines, baseline allows {allowed.get(p, 0)}")
            for b in blocks:
                if b.path == p and b.kind in COUNTED:
                    print(f"  {p}:{b.start}  {b.text.splitlines()[0][:100]}")
        if worse:
            print("\nNew comments are banned. Make the code say it (name, type, test), file an issue for a"
                  " workaround, or record a decision in an ADR.")
        for p in stale:
            print(f"{p}: {current.get(p, 0)} comment lines, baseline still allows {allowed[p]}")
        if stale and not worse:
            print(f"\nComments were removed: tighten the ratchet with `{sys.argv[0]} update` and commit"
                  f" {baseline_path.relative_to(root)}.")
        return 1 if worse or stale else 0

    if args.cmd == "update":
        files = dict(baseline["files"])
        raised = set(args.raise_)
        if not baseline_path.exists():
            files = current
        else:
            for p in set(files) | set(current):
                now = current.get(p, 0)
                files[p] = now if (p in raised or now < files.get(p, 0)) else files.get(p, 0)
                if p not in current:
                    files[p] = 0
        blocked = sorted(p for p, n in current.items() if n > files.get(p, 0))
        baseline["files"] = files
        save_baseline(baseline_path, baseline)
        for p in blocked:
            print(f"{p}: {current[p]} > {files.get(p, 0)}, not raised (use --raise {p})")
        return 1 if blocked else 0

    if args.cmd == "strip":
        hits = [b for b in blocks if any(is_mechanical(b, pat) for pat in args.pattern)]
        if args.dry_run:
            for b in hits:
                print(f"{b.path}:{b.start}  {b.text.splitlines()[0][:100]}")
        else:
            for r in delete_blocks(root, hits):
                print(f"refused {r}")
        print(f"{len(hits)} blocks, {sum(b.lines for b in hits)} lines", file=sys.stderr)
        return 0

    if args.cmd == "delete":
        index = {(b.path, b.start): b for b in blocks}
        chosen, missing = [], []
        for t in args.targets:
            path, _, line = t.rpartition(":")
            b = index.get((path, int(line)))
            (chosen if b else missing).append(b or t)
        for m in missing:
            print(f"no comment block starts at {m}")
        refused = delete_blocks(root, chosen)
        for r in refused:
            print(f"refused {r}")
        return 1 if missing or refused else 0
    return 2


if __name__ == "__main__":
    sys.exit(main())
