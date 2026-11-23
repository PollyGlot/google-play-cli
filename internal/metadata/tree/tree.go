package tree

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/locale"
	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

type LocaleNoFieldsError struct {
	Locale string
	Files  []string
}

func (e *LocaleNoFieldsError) Error() string {
	return fmt.Sprintf(
		"locale directory %q contains files but no recognized Listing field (%v); expected one of title.txt, short_description.txt, full_description.txt, video.txt: check for a typo in the file name",
		e.Locale, e.Files)
}

var (
	osReadDir   = func(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }
	osReadFile  = func(p string) ([]byte, error) { return os.ReadFile(p) }
	osMkdirAll  = func(p string, perm fs.FileMode) error { return os.MkdirAll(p, perm) }
	osWriteFile = func(p string, b []byte, perm fs.FileMode) error { return os.WriteFile(p, b, perm) }
)

const (
	dirPerm  fs.FileMode = 0o755
	filePerm fs.FileMode = 0o644
)

func Read(dir string) (listing.Tree, error) {
	entries, err := osReadDir(dir)
	if err != nil {
		return nil, err
	}
	root, err := pathguard.Root(dir)
	if err != nil {
		return nil, err
	}
	tr := make(listing.Tree)
	for _, e := range entries {
		loc := e.Name()
		path := filepath.Join(dir, loc)
		localeRoot, ok, err := LocaleRoot(root, path, e)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		l, unrecognized, err := readLocale(localeRoot, path, loc)
		if err != nil {
			return nil, err
		}
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

func LocaleRoot(root, path string, e fs.DirEntry) (string, bool, error) {
	if e.IsDir() {
		return root, true, nil
	}
	if e.Type()&fs.ModeSymlink == 0 {
		return "", false, nil
	}
	if _, err := pathguard.ContainUserPath(root, path); err != nil {
		return "", false, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", false, exit.Usagef("locale entry %q is a symlink whose target cannot be read (%v): point it at a locale directory or remove it", path, err)
	}
	if !fi.IsDir() {
		return "", false, exit.Usagef("locale entry %q is a symlink to a file, not a directory: point it at a locale directory or remove it", path)
	}
	resolved, err := pathguard.Root(path)
	if err != nil {
		return "", false, err
	}
	if rel, err := filepath.Rel(root, resolved); err == nil && filepath.IsLocal(rel) {
		return root, true, nil
	}
	return resolved, true, nil
}

func readLocale(root, path, locale string) (listing.Listing, []string, error) {
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
		if e.IsDir() {
			continue
		}
		spec, ok := listing.SpecByFile(e.Name())
		if !ok {
			unrecognized = append(unrecognized, e.Name())
			continue
		}
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

func txtFiles(names []string) []string {
	var out []string
	for _, n := range names {
		if strings.EqualFold(filepath.Ext(n), ".txt") {
			out = append(out, n)
		}
	}
	return out
}

func stripOneTrailingNewline(s string) string {
	if n := len(s); n >= 1 && s[n-1] == '\n' {
		return s[:n-1]
	}
	return s
}

func Write(dir string, tr listing.Tree) error {
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
	for _, locale := range tr.Locales() {
		l := tr[locale]
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
		for _, f := range listing.Fields() {
			v, managed := l.Get(f)
			if !managed {
				continue
			}
			spec, ok := listing.SpecOf(f)
			if !ok {
				continue
			}
			var b []byte
			if v != "" {
				b = []byte(v + "\n")
			}
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
