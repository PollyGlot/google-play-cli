// Package tree is the filesystem codec for the Metadata tree: the pure,
// I/O-only translation between an on-disk `<dir>/<locale>/<field>.txt`
// layout and the typed listing.Tree model. It is the leaf the higher
// metadata commands stand on — `metadata pull` writes a Tree here,
// `metadata apply`/`validate`/`list` read one — and it speaks only the
// filesystem: no HTTP, no auth, no knowledge of the Play API.
//
// The Metadata tree mirrors the shape `fastlane supply` reads (one
// snake_case `.txt` per Listing field, under a per-locale directory),
// minus fastlane's `android/` segment and its `changelogs/` (release
// notes live with `releases`, not metadata — CONTEXT.md "Metadata
// tree"). This package owns the on-disk encoding of the ADR-0011
// "missing ≠ empty" rule: a field *file* that is absent stays absent
// from the Listing (unmanaged → "leave online untouched"); a field file
// that is present but empty is read back as a managed empty string
// (→ "clear online"). Read and Write are exact inverses at the value
// level, so `pull` then `apply` with no edits is a guaranteed no-op.
package tree

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
)

// osReadDir, osReadFile, osMkdirAll, and osWriteFile are package-level
// seams so a later block can swap the filesystem (e.g. an injected
// internal/config.FS) without rewriting Read/Write — same pattern as
// internal/releases/notes. Today they delegate straight to os.
var (
	osReadDir   = func(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }
	osReadFile  = func(p string) ([]byte, error) { return os.ReadFile(p) }
	osMkdirAll  = func(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }
	osWriteFile = func(p string, b []byte, perm fs.FileMode) error { return os.WriteFile(p, b, perm) }
)

// dirPerm and filePerm are the modes Write creates with: 0o755 for the
// locale directories (world-readable, owner-writable, as is conventional
// for a checked-in metadata tree) and 0o644 for the field files.
const (
	dirPerm  fs.FileMode = 0o755
	filePerm fs.FileMode = 0o644
)

// Read loads the Metadata tree rooted at dir into a listing.Tree.
//
// Layout: it scans only the first level of dir. Each sub-directory is a
// candidate locale; inside it, every file whose name listing.SpecByFile
// recognizes (title.txt, short_description.txt, full_description.txt,
// video.txt) becomes a managed field on that locale's Listing. The file
// contents are taken verbatim except that at most one trailing newline
// (\r\n or \n) is stripped — the inverse of the single "\n" Write
// appends — so a multi-line full_description keeps its internal newlines
// intact.
//
// Everything else is deliberately ignored, drawing the metadata/releases
// boundary on disk: unrecognized files (a README, a *.md, an unknown
// *.txt), any first-level *file* (a top-level README), any non-locale
// first-level directory (notably fastlane's `changelogs/`), and any
// nested directory inside a locale (its files are not scanned). A locale
// directory with no recognized field file yields no Tree entry at all
// (an empty Listing carries no information and would only pollute
// downstream diffs).
//
// A dir that does not exist or cannot be read is an error (the caller
// maps it to a CLI exit code). An existing but empty dir is not an
// error: it yields an empty, non-nil Tree.
func Read(dir string) (listing.Tree, error) {
	entries, err := osReadDir(dir)
	if err != nil {
		return nil, err
	}
	tr := make(listing.Tree)
	for _, e := range entries {
		// Only first-level sub-directories are locales. A stray file at
		// the root (e.g. a README) carries no locale and is skipped.
		if !e.IsDir() {
			continue
		}
		locale := e.Name()
		l, err := readLocale(filepath.Join(dir, locale), locale)
		if err != nil {
			return nil, err
		}
		// A directory with no recognized field file (e.g. `changelogs/`,
		// or a locale holding only a README) is not a managed Listing —
		// omit it rather than emit an empty entry.
		if l.Empty() {
			continue
		}
		tr[locale] = l
	}
	return tr, nil
}

// readLocale reads one locale directory into a Listing. It reads only
// the recognized field *files* at this level; nested directories and
// unrecognized files are skipped. The returned Listing may be Empty (no
// recognized field), which Read uses to decide whether to keep it.
func readLocale(path, locale string) (listing.Listing, error) {
	entries, err := osReadDir(path)
	if err != nil {
		return listing.Listing{}, err
	}
	l := listing.NewListing(locale)
	for _, e := range entries {
		// A nested directory inside a locale (e.g. a future `images/`,
		// or a misplaced `changelogs/`) is out of scope for the text
		// codec and is not descended into.
		if e.IsDir() {
			continue
		}
		spec, ok := listing.SpecByFile(e.Name())
		if !ok {
			// Unrecognized file (README, *.md, unknown *.txt) — ignore.
			continue
		}
		b, err := osReadFile(filepath.Join(path, e.Name()))
		if err != nil {
			return listing.Listing{}, err
		}
		l.Set(spec.Field, stripOneTrailingNewline(string(b)))
	}
	return l, nil
}

// stripOneTrailingNewline removes a single trailing line ending — "\r\n"
// or a lone "\n" — and nothing else. It is the exact inverse of the one
// "\n" Write appends to a non-empty value, which is what makes
// Read(Write(X)) == X hold. It must remove *at most one* newline (not
// TrimRight) so that a value whose own last character is a newline, or a
// full_description ending in a blank line, survives the round-trip.
func stripOneTrailingNewline(s string) string {
	if n := len(s); n >= 2 && s[n-2] == '\r' && s[n-1] == '\n' {
		return s[:n-2]
	} else if n >= 1 && s[n-1] == '\n' {
		return s[:n-1]
	}
	return s
}

// Write serializes tr under dir, creating locale directories as needed.
//
// For each locale, for each *managed* field (present in the Listing's
// Fields map — the ADR-0011 marker), it writes dir/<locale>/<Spec.File>.
// A non-empty value is written as value+"\n" (a trailing newline, so the
// file is a clean POSIX text file and `git diff` stays line-oriented); a
// managed empty value is written as a 0-byte file, preserving on disk the
// "clear this field online" signal — the file's *presence* says managed,
// its emptiness says clear, exactly the distinction Read recovers.
//
// Write is additive: it never deletes a file. Pruning (removing locales
// or fields no longer on disk) is a higher-layer, opt-in concern
// (`metadata apply --prune`), never a side effect of writing the tree.
func Write(dir string, tr listing.Tree) error {
	// Iterate locales in sorted order for deterministic mkdir/write
	// sequencing (helps reproducible runs and test assertions).
	for _, locale := range tr.Locales() {
		l := tr[locale]
		localeDir := filepath.Join(dir, locale)
		if err := osMkdirAll(localeDir, dirPerm); err != nil {
			return err
		}
		// Walk the canonical field order so writes are deterministic and
		// only touch fields this Listing actually manages.
		for _, f := range listing.Fields() {
			v, managed := l.Get(f)
			if !managed {
				continue
			}
			spec, ok := listing.SpecOf(f)
			if !ok {
				// Unreachable for a canonical Field, but guard rather
				// than write to an empty path.
				continue
			}
			var b []byte
			if v != "" {
				// Append exactly one "\n" — the inverse of Read's
				// single-newline strip. An empty value stays 0 bytes so
				// Read recovers a managed "" (clear), not an unmanaged
				// field.
				b = []byte(v + "\n")
			}
			if err := osWriteFile(filepath.Join(localeDir, spec.File), b, filePerm); err != nil {
				return err
			}
		}
	}
	return nil
}
