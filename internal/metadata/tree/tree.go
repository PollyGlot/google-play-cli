// Package tree is the filesystem codec for the Metadata tree: the pure,
// I/O-only translation between an on-disk `<dir>/<locale>/<field>.txt`
// layout and the typed listing.Tree model. It is the leaf the higher
// metadata commands stand on: `metadata pull` writes a Tree here,
// `metadata apply`/`validate`/`list` read one, and it speaks only the
// filesystem: no HTTP, no auth, no knowledge of the Play API.
//
// The Metadata tree mirrors the shape `fastlane supply` reads (one
// snake_case `.txt` per Listing field, under a per-locale directory),
// minus fastlane's `android/` segment and its `changelogs/` (release
// notes live with `releases`, not metadata: CONTEXT.md "Metadata
// tree"). This package owns the on-disk encoding of the ADR-0011
// "missing ≠ empty" rule: a field *file* that is absent stays absent
// from the Listing (unmanaged → "leave online untouched"); a field file
// that is present but empty is read back as a managed empty string
// (→ "clear online"). Read and Write are exact inverses at the value
// level, so `pull` then `apply` with no edits is a guaranteed no-op.
package tree

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/locale"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

// LocaleNoFieldsError is returned by Read when a directory NAMED like a
// known Play store locale (locale.IsKnown) holds at least one file but none
// the codec recognizes as a Listing field. That is the signature of a
// filename typo: e.g. `full-description.txt` (hyphen) instead of
// `full_description.txt`, which would otherwise make the locale vanish
// from the Tree silently and, under `metadata apply --prune`, get its live
// Listing deleted. Erroring here makes the typo loud for every consumer of
// the tree (validate and apply alike) instead of silently dropping data.
//
// Note: the check keys on the embedded locale registry, so a locale Google
// added after this gplay release (one you would whitelist with
// --allow-locale) is not recognized as locale-shaped here and a typo inside
// it is still silently skipped; the registry is the best offline signal
// available and is the same authority `metadata validate` uses.
type LocaleNoFieldsError struct {
	Locale string
	Files  []string // the unrecognized file names found in the directory
}

func (e *LocaleNoFieldsError) Error() string {
	return fmt.Sprintf(
		"locale directory %q contains files but no recognized Listing field (%v); expected one of title.txt, short_description.txt, full_description.txt, video.txt: check for a typo in the file name",
		e.Locale, e.Files)
}

// osReadDir, osReadFile, osMkdirAll, and osWriteFile are package-level
// seams so a later block can swap the filesystem (e.g. an injected
// internal/config.FS) without rewriting Read/Write: same pattern as
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
// contents are taken verbatim except that at most one trailing "\n" is
// stripped (the exact inverse of the single "\n" Write appends) so a
// multi-line full_description keeps its internal newlines intact. The codec
// does not normalize CRLF (see stripOneTrailingNewline): a "\r\n"-saved
// file keeps its "\r" in the value, preserving the Read(Write(X)) == X
// round-trip for any value.
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
// error: it yields an empty, non-nil Tree. The one exception to "silently
// ignore the unrecognized" is a directory NAMED like a known Play locale
// that holds an unrecognized *.txt* and no recognized field file: that is
// a Listing-field filename typo, returned as *LocaleNoFieldsError so it is
// not silently dropped (which, under `apply --prune`, would delete the
// live Listing).
func Read(dir string) (listing.Tree, error) {
	entries, err := osReadDir(dir)
	if err != nil {
		return nil, err
	}
	// Containment root, resolved once for the whole walk (PRD #459 / slice
	// #461). Every locale and file name below comes off the disk and can be a
	// symlink out of the tree: without this, an `en-US/title.txt` pointed at
	// `~/.ssh/id_rsa` would be read into the Listing and published to a public
	// store listing by `metadata apply`, under the operator's own credentials.
	root, err := pathguard.Root(dir)
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
		loc := e.Name()
		l, unrecognized, err := readLocale(root, filepath.Join(dir, loc), loc)
		if err != nil {
			return nil, err
		}
		// A directory with no recognized field file (e.g. `changelogs/`,
		// or a locale holding only a README) is not a managed Listing:
		// omit it rather than emit an empty entry. BUT if the directory is
		// named like a known Play locale and holds an unrecognized *.txt*,
		// that is a Listing-field filename typo (e.g. `full-description.txt`):
		// silently dropping it would, under --prune, delete the live Listing.
		// Surface it loudly instead. Non-locale dirs (changelogs/, junk/),
		// README-only locale dirs, and locales Google added since this
		// release (not in the registry) stay benign.
		if l.Empty() {
			if locale.IsKnown(loc) {
				if stray := txtFiles(unrecognized); len(stray) > 0 {
					return nil, &LocaleNoFieldsError{Locale: loc, Files: stray}
				}
			}
			continue
		}
		tr[loc] = l
	}
	return tr, nil
}

// readLocale reads one locale directory into a Listing. It reads only
// the recognized field *files* at this level; nested directories and
// unrecognized files are skipped. The returned Listing may be Empty (no
// recognized field), which Read uses to decide whether to keep it. The
// second return is the list of unrecognized (non-directory) file names
// found, so Read can distinguish a locale-shaped dir whose field files are
// all mis-named (a typo worth erroring on) from an empty or README-only one.
func readLocale(root, path, locale string) (listing.Listing, []string, error) {
	// ContainUserPath, not Contain: `path` is a directory the operator put in
	// their own tree, so a monorepo that symlinks a locale at a shared one is
	// theirs to opt into (GPLAY_ALLOW_EXTERNAL_SYMLINKS). The write path below
	// keeps the strict form: its names come from the API.
	if _, err := pathguard.ContainUserPath(root, path); err != nil {
		return listing.Listing{}, nil, err
	}
	entries, err := osReadDir(path)
	if err != nil {
		return listing.Listing{}, nil, err
	}
	l := listing.NewListing(locale)
	var unrecognized []string
	for _, e := range entries {
		// A nested directory inside a locale (e.g. a future `images/`,
		// or a misplaced `changelogs/`) is out of scope for the text
		// codec and is not descended into.
		if e.IsDir() {
			continue
		}
		spec, ok := listing.SpecByFile(e.Name())
		if !ok {
			// Unrecognized file (README, *.md, unknown *.txt): ignore here,
			// but remember it so Read can flag a locale-shaped dir that holds
			// only mis-named files.
			unrecognized = append(unrecognized, e.Name())
			continue
		}
		// Contain BEFORE the read: a field file symlinked out of the tree must
		// never be opened, not merely dropped after the fact.
		field, err := pathguard.ContainUserPath(root, filepath.Join(path, e.Name()))
		if err != nil {
			return listing.Listing{}, unrecognized, err
		}
		b, err := osReadFile(field)
		if err != nil {
			return listing.Listing{}, unrecognized, err
		}
		l.Set(spec.Field, stripOneTrailingNewline(string(b)))
	}
	return l, unrecognized, nil
}

// txtFiles filters names down to the *.txt ones: the file type a Listing
// field uses, so a mis-named `full-description.txt` is flagged while a
// README.md / LICENSE / *.png in a locale dir stays ignored.
func txtFiles(names []string) []string {
	var out []string
	for _, n := range names {
		if strings.EqualFold(filepath.Ext(n), ".txt") {
			out = append(out, n)
		}
	}
	return out
}

// stripOneTrailingNewline removes a single trailing "\n", and nothing
// else. It is the EXACT inverse of the one "\n" Write appends, which is
// what makes Read(Write(X)) == X hold for *every* value, and Write(Read(D))
// == D for every disk file D (a clean bijection both ways).
//
// It deliberately does NOT also strip a preceding "\r": doing so would make
// a value whose own last byte is "\r" round-trip to a different value
// (Write("x\r") = "x\r\n", and stripping "\r\n" would read back "x", not
// "x\r"), silently breaking the pull→apply no-op invariant for Play-sourced
// text with a trailing carriage return (CodeRabbit review, PR #110). The
// consequence is that the codec does not normalize CRLF: a file saved with
// "\r\n" line endings keeps its "\r" inside the value: save field files as
// LF for clean values. Stripping at most one byte (not TrimRight) is what
// lets a full_description ending in a blank line survive the round-trip.
func stripOneTrailingNewline(s string) string {
	if n := len(s); n >= 1 && s[n-1] == '\n' {
		return s[:n-1]
	}
	return s
}

// Write serializes tr under dir, creating locale directories as needed.
//
// For each locale, for each *managed* field (present in the Listing's
// Fields map: the ADR-0011 marker), it writes dir/<locale>/<Spec.File>.
// A non-empty value is written as value+"\n" (a trailing newline, so the
// file is a clean POSIX text file and `git diff` stays line-oriented); a
// managed empty value is written as a 0-byte file, preserving on disk the
// "clear this field online" signal: the file's *presence* says managed,
// its emptiness says clear, exactly the distinction Read recovers.
//
// Write is additive: it never deletes a file. Pruning (removing locales
// or fields no longer on disk) is a higher-layer, opt-in concern
// (`metadata apply --prune`), never a side effect of writing the tree.
func Write(dir string, tr listing.Tree) error {
	// The locale keys of tr come from the Play API on the pull path: a string
	// gplay did not author, joined straight into a filesystem path. Establish
	// the containment root up front and check every derived path against it,
	// so neither a hostile locale nor a pre-placed symlink can make a write
	// land outside dir (PRD #459 / slice #461).
	//
	// An empty Tree writes nothing and must therefore create nothing: the
	// early return keeps `pull` on an app with no listings from leaving an
	// empty directory behind.
	if len(tr) == 0 {
		return nil
	}
	if err := osMkdirAll(dir, dirPerm); err != nil {
		return err
	}
	root, err := pathguard.Root(dir)
	if err != nil {
		return err
	}
	// Iterate locales in sorted order for deterministic mkdir/write
	// sequencing (helps reproducible runs and test assertions).
	for _, locale := range tr.Locales() {
		l := tr[locale]
		// A locale is one path component and nothing else: reject "..", a
		// separator, or an absolute path before it is ever joined.
		if err := pathguard.Segment("locale", locale); err != nil {
			return err
		}
		localeDir := filepath.Join(dir, locale)
		if _, err := pathguard.Contain(root, localeDir); err != nil {
			return err
		}
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
				// Append exactly one "\n": the inverse of Read's
				// single-newline strip. An empty value stays 0 bytes so
				// Read recovers a managed "" (clear), not an unmanaged
				// field.
				b = []byte(v + "\n")
			}
			// ContainWrite, not Contain: osWriteFile follows a symlinked leaf,
			// so a `title.txt` pre-placed as a link would have its target
			// overwritten with store text on `metadata pull`.
			field, err := pathguard.ContainWrite(root, filepath.Join(localeDir, spec.File))
			if err != nil {
				return err
			}
			if err := osWriteFile(field, b, filePerm); err != nil {
				return err
			}
		}
	}
	return nil
}
