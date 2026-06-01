// Package imagetree is the filesystem codec for the Store-image side of the
// Metadata tree: the pure, I/O-only translation between an on-disk
// `<dir>/<locale>/images/...` layout and the in-memory Tree. It is the image
// sibling of internal/metadata/tree — `images pull` writes a Tree here,
// `images apply`/`validate` read one — and it speaks only the filesystem: no
// HTTP, no auth, no knowledge of the Play API beyond the image Type set.
//
// Disk layout (mirrors `fastlane supply`, minus the `android/` segment):
//
//	<dir>/<locale>/images/<type>.<ext>          (singular: icon, featureGraphic, tvBanner, promoGraphic)
//	<dir>/<locale>/images/<type>/1.<ext>…N.<ext> (gallery: phone/seven/ten-inch/tv/wear screenshots)
//
// The codec owns the ADR-0013 "missing == empty" rule on disk: an absent or
// empty slot is simply not represented in the Tree (unmanaged → "leave Play
// untouched"). Read is lenient — a stray empty directory, a README, or an
// unrecognized file is benign, never an error and never a phantom slot —
// because git cannot preserve an "empty directory = delete" signal anyway.
// The text codec (internal/metadata/tree) ignores nested directories, so the
// text `.txt` files and this `images/` subtree cohabit under one locale dir.
package imagetree

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

// Tree is the on-disk Store-image tree in memory: locale → (image type →
// ordered image byte blobs). A singular slot holds exactly one blob; a gallery
// holds its blobs in display order (the on-disk filename sort, ADR-0013). Only
// non-empty slots are present — an absent/empty slot is unmanaged and has no
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
// (ADR-0013) — Play returns no original filename, so the bytes are the only
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
func Write(dir string, tr Tree) error {
	for _, locale := range sortedLocales(tr) {
		slots := tr[locale]
		imgDir := filepath.Join(dir, locale, imagesSubdir)
		for _, ty := range images.Types() {
			seq := slots[ty]
			if len(seq) == 0 {
				continue
			}
			if ty.Gallery() {
				if err := writeGallery(filepath.Join(imgDir, string(ty)), ty, seq); err != nil {
					return err
				}
				continue
			}
			// Singular: write the first (only) blob as <type>.<ext>.
			if err := writeSingular(imgDir, ty, seq[0]); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeSingular(imgDir string, ty images.Type, data []byte) error {
	ext := Ext(data)
	if ext == "" {
		return fmt.Errorf("imagetree: %s: image bytes are neither PNG nor JPEG", ty)
	}
	if err := os.MkdirAll(imgDir, dirPerm); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(imgDir, string(ty)+"."+ext), data, filePerm)
}

func writeGallery(galleryDir string, ty images.Type, seq [][]byte) error {
	if err := os.MkdirAll(galleryDir, dirPerm); err != nil {
		return err
	}
	for i, data := range seq {
		ext := Ext(data)
		if ext == "" {
			return fmt.Errorf("imagetree: %s[%d]: image bytes are neither PNG nor JPEG", ty, i)
		}
		// 1-based, no zero-padding: Play caps galleries at 8 so a single
		// digit always sorts correctly (ADR-0013).
		name := fmt.Sprintf("%d.%s", i+1, ext)
		if err := os.WriteFile(filepath.Join(galleryDir, name), data, filePerm); err != nil {
			return err
		}
	}
	return nil
}

// Read loads the Store-image tree rooted at dir. It scans `<dir>/<locale>/
// images/` for each first-level locale directory: a recognized image file
// whose stem names a singular type is a singular slot; a sub-directory whose
// name is a gallery type is a gallery slot (its image files read in
// filename-sorted display order). Everything else — a README, an unknown
// extension, a stray empty directory, a non-locale dir — is ignored. An absent
// dir or absent images/ subtree yields an empty (non-nil) Tree, not an error.
func Read(dir string) (Tree, error) {
	tr := make(Tree)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		locale := e.Name()
		slots, err := readLocale(filepath.Join(dir, locale, imagesSubdir))
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
func readLocale(imgDir string) (map[images.Type][][]byte, error) {
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
				continue // stray dir, or a singular type named as a dir — ignore
			}
			seq, err := readGallery(filepath.Join(imgDir, e.Name()))
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
			continue // unknown stem, or a gallery type as a flat file — ignore
		}
		data, err := os.ReadFile(filepath.Join(imgDir, e.Name()))
		if err != nil {
			return nil, err
		}
		slots[ty] = [][]byte{data}
	}
	return slots, nil
}

// readGallery reads a gallery directory's recognized image files in
// filename-sorted (display) order, returning their bytes.
func readGallery(galleryDir string) ([][]byte, error) {
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
		data, err := os.ReadFile(filepath.Join(galleryDir, name))
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
