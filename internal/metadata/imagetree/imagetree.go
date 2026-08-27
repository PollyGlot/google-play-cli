// Package imagetree is the filesystem codec for the Store-image side of the
// Metadata tree: the pure, I/O-only translation between an on-disk
// `<dir>/<locale>/images/...` layout and the in-memory Tree. It is the image
// sibling of internal/metadata/tree: `images pull` writes a Tree here,
// `images apply`/`validate` read one, and it speaks only the filesystem: no
// HTTP, no auth, no knowledge of the Play API beyond the image Type set.
//
// Disk layout (mirrors `fastlane supply`, minus the `android/` segment):
//
//	<dir>/<locale>/images/<type>.<ext>          (singular: icon, featureGraphic, tvBanner, promoGraphic)
//	<dir>/<locale>/images/<type>/1.<ext>…N.<ext> (gallery: phone/seven/ten-inch/tv/wear screenshots)
//
// The codec owns the ADR-0013 "missing == empty" rule on disk: an absent or
// empty slot is simply not represented in the Tree (unmanaged → "leave Play
// untouched"). Read is lenient: a stray empty directory, a README, or an
// unrecognized file is benign, never an error and never a phantom slot,
// because git cannot preserve an "empty directory = delete" signal anyway.
// The text codec (internal/metadata/tree) ignores nested directories, so the
// text `.txt` files and this `images/` subtree cohabit under one locale dir.
package imagetree

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/pathguard"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// Tree is the on-disk Store-image tree in memory: locale → (image type →
// ordered image byte blobs). A singular slot holds exactly one blob; a gallery
// holds its blobs in display order (the on-disk filename sort, ADR-0013). Only
// non-empty slots are present: an absent/empty slot is unmanaged and has no
// entry (missing == empty).
type Tree map[string]map[images.Type][][]byte

const (
	dirPerm  os.FileMode = 0o755
	filePerm os.FileMode = 0o644
)

// imagesSubdir is the per-locale directory the image codec owns, kept distinct
// from the locale's text `.txt` files.
const imagesSubdir = "images"

// Ext returns the file extension ("png" or "jpg") sniffed from an image's
// leading magic bytes, or "" if the bytes are neither PNG nor JPEG. The
// extension is derived from CONTENT, never from a response Content-Type header
// (ADR-0013): Play returns no original filename, so the bytes are the only
// authority.
func Ext(data []byte) string {
	switch {
	case len(data) >= 8 && string(data[:8]) == "\x89PNG\r\n\x1a\n":
		return "png"
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		return "jpg"
	default:
		return ""
	}
}

// recognizedExts are the on-disk extensions Read accepts as an image file.
// Write only ever emits png/jpg (Ext sniffs those two), but a hand-authored
// tree may use the .jpeg spelling, so Read recognizes it too.
var recognizedExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true}

// Write materializes tr under dir. Singular slots become `<type>.<ext>`;
// gallery slots become a `<type>/` directory of `1.<ext>`…`N.<ext>` in slice
// order. The extension is sniffed per blob (Ext); a blob that is neither PNG
// nor JPEG is a hard error (the codec cannot synthesize a filename for it) so
// a corrupt download surfaces loudly rather than landing as `.bin`.
//
// Write is idempotent PER MANAGED SLOT: writing a slot that shrank (a gallery
// from 3 images to 2) or whose singular extension changed (jpg → png) removes
// the stale recognized image files so the slot resolves to exactly what tr
// holds, which keeps a re-`pull` a faithful mirror and `pull → apply` a no-op.
// It is NOT a full RemoveAll: an unrecognized file (a hand-added note) and a
// slot/locale entirely absent from tr are left untouched, matching the
// additive stance of the text codec (removing an online-gone slot is apply's
// job, not pull's).
func Write(dir string, tr Tree) error {
	// The locale keys of tr come from the Play API on the pull path: a string
	// gplay did not author, joined straight into a filesystem path. Establish
	// the containment root up front (creating dir, since pull writes into a
	// tree that may not exist yet) and check every derived path against it,
	// so a hostile or merely malformed locale cannot write outside dir
	// (PRD #459 / slice #461).
	//
	// An empty Tree writes nothing and must therefore create nothing: returning
	// early keeps `pull` on an app with no images from leaving an empty
	// directory behind, which is what it did before containment was added.
	if len(tr) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	root, err := pathguard.Root(dir)
	if err != nil {
		return err
	}
	for _, locale := range sortedLocales(tr) {
		// A locale is one path component and nothing else: reject "..", a
		// separator, or an absolute path before it is ever joined.
		if err := pathguard.Segment("locale", locale); err != nil {
			return err
		}
		slots := tr[locale]
		imgDir := filepath.Join(dir, locale, imagesSubdir)
		for _, ty := range images.Types() {
			seq := slots[ty]
			if len(seq) == 0 {
				continue
			}
			if ty.Gallery() {
				if err := writeGallery(root, filepath.Join(imgDir, string(ty)), ty, seq); err != nil {
					return err
				}
				continue
			}
			// Singular: write the first (only) blob as <type>.<ext>.
			if err := writeSingular(root, imgDir, ty, seq[0]); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeSingular(root, imgDir string, ty images.Type, data []byte) error {
	ext := Ext(data)
	if ext == "" {
		return fmt.Errorf("imagetree: %s: image bytes are neither PNG nor JPEG", ty)
	}
	if _, err := pathguard.Contain(root, imgDir); err != nil {
		return err
	}
	if err := os.MkdirAll(imgDir, dirPerm); err != nil {
		return err
	}
	name := string(ty) + "." + ext
	if err := writeContained(root, filepath.Join(imgDir, name), data); err != nil {
		return err
	}
	// Idempotency: drop a sibling <type>.<otherext> left by a previous write
	// (e.g. a re-pull where the live image's format changed jpg → png), so the
	// singular slot resolves to exactly one file and Read stays deterministic.
	for ext := range recognizedExts {
		stale := string(ty) + ext
		if stale == name {
			continue
		}
		if err := os.Remove(filepath.Join(imgDir, stale)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func writeGallery(root, galleryDir string, ty images.Type, seq [][]byte) error {
	if _, err := pathguard.Contain(root, galleryDir); err != nil {
		return err
	}
	if err := os.MkdirAll(galleryDir, dirPerm); err != nil {
		return err
	}
	written := make(map[string]bool, len(seq))
	for i, data := range seq {
		ext := Ext(data)
		if ext == "" {
			return fmt.Errorf("imagetree: %s[%d]: image bytes are neither PNG nor JPEG", ty, i)
		}
		// 1-based, no zero-padding: Play caps galleries at 8 so a single
		// digit always sorts correctly (ADR-0013).
		name := fmt.Sprintf("%d.%s", i+1, ext)
		if err := writeContained(root, filepath.Join(galleryDir, name), data); err != nil {
			return err
		}
		written[name] = true
	}
	// Idempotency: prune recognized image files left from a previous, LARGER
	// write (e.g. a re-pull where the live gallery shrank from 3 to 2), so a
	// stale N+1.<ext> is not read back as a phantom screenshot and re-uploaded
	// by apply. Only recognized image files are removed: a hand-added note
	// stays put (this is not a destructive RemoveAll of the slot directory).
	return pruneStaleGallery(root, galleryDir, written)
}

// pruneStaleGallery removes the recognized image files in galleryDir that are
// not in keep (the set just written), leaving any unrecognized file untouched.
func pruneStaleGallery(root, galleryDir string, keep map[string]bool) error {
	entries, err := os.ReadDir(galleryDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || keep[e.Name()] {
			continue
		}
		if _, ext := splitExt(e.Name()); recognizedExts[ext] {
			// This branch DELETES, so containment matters most here: a symlink
			// named 2.png must not turn a prune into a remove outside the tree.
			victim, err := pathguard.Contain(root, filepath.Join(galleryDir, e.Name()))
			if err != nil {
				return err
			}
			if err := os.Remove(victim); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// Read loads the Store-image tree rooted at dir. It scans `<dir>/<locale>/
// images/` for each first-level locale directory: a recognized image file
// whose stem names a singular type is a singular slot; a sub-directory whose
// name is a gallery type is a gallery slot (its image files read in
// filename-sorted display order). Everything else (a README, an unknown
// extension, a stray empty directory, a non-locale dir) is ignored. An absent
// dir or absent images/ subtree yields an empty (non-nil) Tree, not an error.
func Read(dir string) (Tree, error) {
	tr := make(Tree)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	// Containment root, resolved once for the whole walk (PRD #459 / slice
	// #461). Every locale, gallery and file name below comes off the disk, and
	// any of them can be a symlink out of the tree: without this, an
	// `en-US/images/icon.png` symlinked at a private file would be uploaded to
	// a public store listing under the operator's own credentials.
	root, err := pathguard.Root(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		locale := e.Name()
		slots, err := readLocale(root, filepath.Join(dir, locale, imagesSubdir))
		if err != nil {
			return nil, err
		}
		if len(slots) > 0 {
			tr[locale] = slots
		}
	}
	return tr, nil
}

// readLocale reads one locale's images/ directory into a slot map. A missing
// images/ dir yields an empty map (the locale has only text, or nothing). Only
// non-empty slots are returned.
func readLocale(root, imgDir string) (map[images.Type][][]byte, error) {
	// The read side uses ContainUserPath throughout: every name below was found
	// by walking the operator's own tree, so `en-US/images` symlinked at a
	// shared asset directory is a layout they can opt into
	// (GPLAY_ALLOW_EXTERNAL_SYMLINKS). The write side above stays strict: its
	// locales come from the API.
	if _, err := pathguard.ContainUserPath(root, imgDir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(imgDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	slots := make(map[images.Type][][]byte)
	for _, e := range entries {
		if e.IsDir() {
			ty, ok := images.ParseType(e.Name())
			if !ok || !ty.Gallery() {
				continue // stray dir, or a singular type named as a dir: ignore
			}
			seq, err := readGallery(root, filepath.Join(imgDir, e.Name()))
			if err != nil {
				return nil, err
			}
			if len(seq) > 0 {
				slots[ty] = seq
			}
			continue
		}
		// A file: a singular slot iff its stem is a singular type and its
		// extension is a recognized image extension.
		stem, ext := splitExt(e.Name())
		if !recognizedExts[ext] {
			continue
		}
		ty, ok := images.ParseType(stem)
		if !ok || ty.Gallery() {
			continue // unknown stem, or a gallery type as a flat file: ignore
		}
		data, err := readContained(root, filepath.Join(imgDir, e.Name()))
		if err != nil {
			return nil, err
		}
		slots[ty] = [][]byte{data}
	}
	return slots, nil
}

// writeContained is os.WriteFile with the containment check in front of it,
// the write-side mirror of readContained: containing the directory is not
// enough, because os.WriteFile follows a symlinked LEAF and an image file
// pre-placed as a link would have its target overwritten by `images pull`.
func writeContained(root, p string, data []byte) error {
	target, err := pathguard.ContainWrite(root, p)
	if err != nil {
		return err
	}
	return os.WriteFile(target, data, filePerm)
}

// readContained is os.ReadFile with the containment check in front of it, so
// no image file is opened before it has been proved to live inside root.
func readContained(root, p string) ([]byte, error) {
	if _, err := pathguard.ContainUserPath(root, p); err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// readGallery reads a gallery directory's recognized image files in
// filename-sorted (display) order, returning their bytes.
func readGallery(root, galleryDir string) ([][]byte, error) {
	if _, err := pathguard.ContainUserPath(root, galleryDir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(galleryDir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ext := splitExt(e.Name()); recognizedExts[ext] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // filename sort == display order (ADR-0013)
	seq := make([][]byte, 0, len(names))
	for _, name := range names {
		data, err := readContained(root, filepath.Join(galleryDir, name))
		if err != nil {
			return nil, err
		}
		seq = append(seq, data)
	}
	return seq, nil
}

// splitExt splits a filename into its stem and lower-cased extension
// (including the dot). "icon.PNG" → ("icon", ".png").
func splitExt(name string) (stem, ext string) {
	ext = filepath.Ext(name)
	stem = name[:len(name)-len(ext)]
	return stem, toLower(ext)
}

// toLower lowercases an ASCII extension without pulling in strings just for
// this (the codec stays dependency-light).
func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// sortedLocales returns tr's locale keys in lexical order for deterministic
// Write output.
func sortedLocales(tr Tree) []string {
	out := make([]string, 0, len(tr))
	for loc := range tr {
		out = append(out, loc)
	}
	sort.Strings(out)
	return out
}
