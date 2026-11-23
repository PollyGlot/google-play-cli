package imagetree

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/metadata/tree"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
)

type Tree map[string]map[images.Type][][]byte

type Staged map[string]map[images.Type][]string

type blob interface {
	head() ([]byte, error)
	copyTo(w io.Writer) error
}

type memBlob []byte

func (b memBlob) head() ([]byte, error) { return b, nil }
func (b memBlob) copyTo(w io.Writer) error {
	_, err := w.Write(b)
	return err
}

type fileBlob string

func (p fileBlob) head() ([]byte, error) {
	f, err := os.Open(string(p))
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b := make([]byte, 8)
	n, err := io.ReadFull(f, b)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return b[:n], nil
}

func (p fileBlob) copyTo(w io.Writer) error {
	f, err := os.Open(string(p))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func blobExt(b blob) (string, error) {
	h, err := b.head()
	if err != nil {
		return "", err
	}
	return Ext(h), nil
}

const (
	dirPerm  os.FileMode = 0o755
	filePerm os.FileMode = 0o644
)

const imagesSubdir = "images"

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

var recognizedExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true}

func Write(dir string, tr Tree) error {
	bt := make(map[string]map[images.Type][]blob, len(tr))
	for loc, slots := range tr {
		bt[loc] = make(map[images.Type][]blob, len(slots))
		for ty, seq := range slots {
			bs := make([]blob, len(seq))
			for i, data := range seq {
				bs[i] = memBlob(data)
			}
			bt[loc][ty] = bs
		}
	}
	return write(dir, bt)
}

func WriteStaged(dir string, st Staged) error {
	bt := make(map[string]map[images.Type][]blob, len(st))
	for loc, slots := range st {
		bt[loc] = make(map[images.Type][]blob, len(slots))
		for ty, seq := range slots {
			bs := make([]blob, len(seq))
			for i, p := range seq {
				bs[i] = fileBlob(p)
			}
			bt[loc][ty] = bs
		}
	}
	return write(dir, bt)
}

func write(dir string, tr map[string]map[images.Type][]blob) error {
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
			if err := writeSingular(root, imgDir, ty, seq[0]); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeSingular(root, imgDir string, ty images.Type, data blob) error {
	ext, err := blobExt(data)
	if err != nil {
		return err
	}
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

func writeGallery(root, galleryDir string, ty images.Type, seq []blob) error {
	if _, err := pathguard.Contain(root, galleryDir); err != nil {
		return err
	}
	if err := os.MkdirAll(galleryDir, dirPerm); err != nil {
		return err
	}
	written := make(map[string]bool, len(seq))
	for i, data := range seq {
		ext, err := blobExt(data)
		if err != nil {
			return err
		}
		if ext == "" {
			return fmt.Errorf("imagetree: %s[%d]: image bytes are neither PNG nor JPEG", ty, i)
		}
		name := fmt.Sprintf("%d.%s", i+1, ext)
		if err := writeContained(root, filepath.Join(galleryDir, name), data); err != nil {
			return err
		}
		written[name] = true
	}
	return pruneStaleGallery(root, galleryDir, written)
}

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

func Read(dir string) (Tree, error) {
	st, err := Scan(dir)
	if err != nil {
		return nil, err
	}
	tr := make(Tree, len(st))
	for loc, slots := range st {
		tr[loc] = make(map[images.Type][][]byte, len(slots))
		for ty, paths := range slots {
			seq := make([][]byte, len(paths))
			for i, p := range paths {
				if seq[i], err = os.ReadFile(p); err != nil {
					return nil, err
				}
			}
			tr[loc][ty] = seq
		}
	}
	return tr, nil
}

func Scan(dir string) (Staged, error) {
	st := make(Staged)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	root, err := pathguard.Root(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		locale := e.Name()
		localeRoot, ok, err := tree.LocaleRoot(root, filepath.Join(dir, locale), e)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		slots, err := scanLocale(localeRoot, filepath.Join(dir, locale, imagesSubdir))
		if err != nil {
			return nil, err
		}
		if len(slots) > 0 {
			st[locale] = slots
		}
	}
	return st, nil
}

func scanLocale(root, imgDir string) (map[images.Type][]string, error) {
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
	slots := make(map[images.Type][]string)
	for _, e := range entries {
		if e.IsDir() {
			ty, ok := images.ParseType(e.Name())
			if !ok || !ty.Gallery() {
				continue
			}
			seq, err := scanGallery(root, filepath.Join(imgDir, e.Name()))
			if err != nil {
				return nil, err
			}
			if len(seq) > 0 {
				slots[ty] = seq
			}
			continue
		}
		stem, ext := splitExt(e.Name())
		if !recognizedExts[ext] {
			continue
		}
		ty, ok := images.ParseType(stem)
		if !ok || ty.Gallery() {
			continue
		}
		p, err := contained(root, filepath.Join(imgDir, e.Name()))
		if err != nil {
			return nil, err
		}
		slots[ty] = []string{p}
	}
	return slots, nil
}

func Retired(dir string) ([]string, error) {
	locales, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	root, err := pathguard.Root(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range locales {
		if _, ok, err := tree.LocaleRoot(root, filepath.Join(dir, l.Name()), l); err != nil {
			return nil, err
		} else if !ok {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, l.Name(), imagesSubdir))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			stem := e.Name()
			if !e.IsDir() {
				var ext string
				if stem, ext = splitExt(e.Name()); !recognizedExts[ext] {
					continue
				}
			}
			if images.Retired(stem) {
				out = append(out, filepath.Join(dir, l.Name(), imagesSubdir, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func RetiredMessage(path string) string {
	return path + ": not an image type Play accepts any more (Google removed it from the API's AppImageType), so gplay ignores it; delete the file"
}

func writeContained(root, p string, data blob) error {
	target, err := pathguard.ContainWrite(root, p)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = data.copyTo(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, filePerm)
	}
	if err == nil {
		err = os.Rename(tmp, target)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func contained(root, p string) (string, error) {
	if _, err := pathguard.ContainUserPath(root, p); err != nil {
		return "", err
	}
	return p, nil
}

func Open(dir, p string) (*os.File, error) {
	root, err := pathguard.Root(dir)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	if err := sameContainedFile(root, p, f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func sameContainedFile(root, p string, f *os.File) error {
	if _, err := pathguard.ContainUserPath(root, p); err != nil {
		return err
	}
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	named, err := os.Stat(p)
	if err != nil {
		return err
	}
	if !os.SameFile(opened, named) {
		return exit.Usagef("refusing to use %q: it was replaced while gplay was opening it", p)
	}
	return nil
}

func scanGallery(root, galleryDir string) ([]string, error) {
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
	sort.Strings(names)
	seq := make([]string, 0, len(names))
	for _, name := range names {
		p, err := contained(root, filepath.Join(galleryDir, name))
		if err != nil {
			return nil, err
		}
		seq = append(seq, p)
	}
	return seq, nil
}

func splitExt(name string) (stem, ext string) {
	ext = filepath.Ext(name)
	stem = name[:len(name)-len(ext)]
	return stem, toLower(ext)
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func sortedLocales[V any](tr map[string]V) []string {
	out := make([]string, 0, len(tr))
	for loc := range tr {
		out = append(out, loc)
	}
	sort.Strings(out)
	return out
}
