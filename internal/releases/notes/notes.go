package notes

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/PollyGlot/google-play-cli/internal/pathguard"
)

const MaxNoteFileSize = 1 << 20

var (
	osReadDir  = func(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }
	osStat     = func(p string) (os.FileInfo, error) { return os.Stat(p) }
	osReadFile = func(p string) ([]byte, error) { return os.ReadFile(p) }
)

type LocaleNote struct {
	Locale   string
	Text     string
	Fallback bool
}

type DefaultLanguage struct {
	tag string
}

func KnownLanguage(tag string) DefaultLanguage { return DefaultLanguage{tag: tag} }

func (l DefaultLanguage) Known() bool { return l.tag != "" }

func (l DefaultLanguage) String() string {
	if !l.Known() {
		return "the default language"
	}
	return l.tag
}

func (l DefaultLanguage) note(text string) LocaleNote {
	return LocaleNote{Locale: l.tag, Text: text, Fallback: !l.Known()}
}

type Opts struct {
	Text    string
	TextSet bool

	Dir string

	DefaultLanguage DefaultLanguage
}

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) ExitCode() int { return 2 }

const MaxNoteChars = 500

type NoteError struct {
	Locale string
	Source string
	Chars  int
}

func (e *NoteError) Error() string {
	if e.Chars == 0 {
		return fmt.Sprintf("release notes for %s (%s) are empty (0 characters): write them, or drop this locale", e.Locale, e.Source)
	}
	return fmt.Sprintf("release notes for %s (%s) are %d characters long: Google Play allows at most %d per locale", e.Locale, e.Source, e.Chars, MaxNoteChars)
}
func (e *NoteError) ExitCode() int { return 20 }

func checkNote(locale, source, text string) error {
	n := utf8.RuneCountInString(text)
	if strings.TrimSpace(text) == "" {
		n = 0
	}
	if n == 0 || n > MaxNoteChars {
		return &NoteError{Locale: locale, Source: source, Chars: n}
	}
	return nil
}

func Load(opts Opts) ([]LocaleNote, error) {
	if opts.TextSet || opts.Text != "" {
		text := trimTrailingWS(opts.Text)
		if err := checkNote(opts.DefaultLanguage.String(), "--release-notes", text); err != nil {
			return nil, err
		}
		return []LocaleNote{opts.DefaultLanguage.note(text)}, nil
	}
	if opts.Dir == "" {
		return nil, nil
	}
	return loadDir(opts.Dir, opts.DefaultLanguage)
}

func loadDir(dir string, defaultLang DefaultLanguage) ([]LocaleNote, error) {
	entries, err := osReadDir(dir)
	if err != nil {
		return nil, err
	}
	root, err := pathguard.Root(dir)
	if err != nil {
		return nil, err
	}
	var out []LocaleNote
	hasDefault := false
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".txt") {
			continue
		}
		locale := strings.TrimSuffix(name, ".txt")
		if locale == defaultLocaleFile {
			hasDefault = true
			continue
		}
		path := filepath.Join(dir, name)
		raw, err := readCapped(root, path)
		if err != nil {
			return nil, err
		}
		text := trimTrailingWS(string(raw))
		if err := checkNote(locale, path, text); err != nil {
			return nil, err
		}
		out = append(out, LocaleNote{Locale: locale, Text: text})
	}
	if !hasDefault || defaultMayBeCovered(out, defaultLang) {
		return out, nil
	}
	path := filepath.Join(dir, defaultLocaleFile+".txt")
	raw, err := readCapped(root, path)
	if err != nil {
		return nil, err
	}
	text := trimTrailingWS(string(raw))
	if err := checkNote(defaultLang.String(), path, text); err != nil {
		return nil, err
	}
	return append(out, defaultLang.note(text)), nil
}

func defaultMayBeCovered(out []LocaleNote, defaultLang DefaultLanguage) bool {
	if !defaultLang.Known() {
		return len(out) > 0
	}
	for _, n := range out {
		if strings.EqualFold(n.Locale, defaultLang.tag) {
			return true
		}
	}
	return false
}

func readCapped(root, p string) ([]byte, error) {
	if _, err := pathguard.ContainUserPath(root, p); err != nil {
		return nil, err
	}
	info, err := osStat(p)
	if err != nil {
		return nil, err
	}
	if info.Size() > MaxNoteFileSize {
		return nil, &ValidationError{
			Message: fmt.Sprintf("release-notes file %s exceeds the %d-byte cap (got %d bytes): Google Play limits per-locale notes to 500 characters", p, int64(MaxNoteFileSize), info.Size()),
		}
	}
	text, err := osReadFile(p)
	if err != nil {
		return nil, err
	}
	if int64(len(text)) > MaxNoteFileSize {
		return nil, &ValidationError{
			Message: fmt.Sprintf("release-notes file %s exceeds the %d-byte cap (got %d bytes): Google Play limits per-locale notes to 500 characters", p, int64(MaxNoteFileSize), len(text)),
		}
	}
	return text, nil
}

const defaultLocaleFile = "default"

func trimTrailingWS(s string) string {
	return strings.TrimRight(s, " \t\n\r")
}
