#!/usr/bin/env python3
"""Align a monospace table into &nbsp;-padded HTML rows for the
gplay-social-cards terminal template.

WHY: HTML collapses runs of whitespace to a single space, so column
alignment inside template-terminal.html must be done with &nbsp;. This
script does tabwriter-style padding, then escapes every space to &nbsp;
so the columns line up exactly in the rendered PNG.

INPUT  : TSV (tab-separated) on stdin or a file path. First row = header.
OUTPUT : one <div>…</div> per row on stdout, ready to paste into the
         <div class="body"> of the terminal template. The header row is
         wrapped in <span class="muted">.

Cells keep their internal spaces (also escaped) so multi-word cells like
a SUMMARY column or a "2026-05-29 08:41" timestamp stay on one line.

Usage:
  python3 align_table.py reviews.tsv
  python3 align_table.py --gap=2 reviews.tsv
  python3 align_table.py --no-header data.tsv
  # or, when a service account is available, straight from real output:
  gplay reviews list --package com.example.notes --stars 1-2 --output tsv \
    | python3 align_table.py

The data you feed it is your call (see the skill's demo-data policy:
format-fidelity samples for public posts, real gplay output only when a
service account is present and no real PII is exposed). The alignment is
identical either way.
"""
import html
import sys


def build(rows, gap=2, header=True):
    if not rows:
        return ""
    ncols = max(len(r) for r in rows)
    rows = [r + [""] * (ncols - len(r)) for r in rows]
    widths = [max(len(r[c]) for r in rows) for c in range(ncols)]
    sep = " " * gap
    out = []
    for i, r in enumerate(rows):
        cells = [r[c].ljust(widths[c]) for c in range(ncols)]
        line = sep.join(cells).rstrip()
        # escape HTML entities first, THEN turn every space non-breaking
        line = html.escape(line).replace(" ", "&nbsp;")
        if i == 0 and header:
            out.append(f'<div><span class="muted">{line}</span></div>')
        else:
            out.append(f"<div>{line}</div>")
    return "\n".join(out)


def main():
    gap = 2
    header = True
    paths = []
    for a in sys.argv[1:]:
        if a.startswith("--gap="):
            gap = int(a.split("=", 1)[1])
        elif a == "--no-header":
            header = False
        elif not a.startswith("-"):
            paths.append(a)
    src = open(paths[0]) if paths else sys.stdin
    rows = [line.rstrip("\n").split("\t") for line in src if line.strip()]
    print(build(rows, gap=gap, header=header))


if __name__ == "__main__":
    main()
