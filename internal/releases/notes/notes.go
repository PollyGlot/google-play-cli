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
	Locale string
	Text   string
}

type Opts struct {
	Text string

	Dir string

	DefaultLanguage string
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

func Precheck(text, dir string) error {
	_, err := Load(Opts{Text: text, Dir: dir, DefaultLanguage: OfflineDefaultLanguage})
	return err
}

func CheckText(text string) error {
	return checkNote(OfflineDefaultLanguage, "--release-notes", trimTrailingWS(text))
}

const OfflineDefaultLanguage = "the default language"

func Load(opts Opts) ([]LocaleNote, error) {
	if opts.Text != "" && opts.Dir != "" {
		return nil, &ValidationError{
			Message: "release-notes and release-notes-dir are mutually exclusive: pick one",
		}
	}
	if opts.Text != "" {
		if opts.DefaultLanguage == "" {
			return nil, &ValidationError{
				Message: "DefaultLanguage is required when using --release-notes (no locale to assign the text to)",
			}
		}
		text := trimTrailingWS(opts.Text)
		if err := checkNote(opts.DefaultLanguage, "--release-notes", text); err != nil {
			return nil, err
		}
		return []LocaleNote{{Locale: opts.DefaultLanguage, Text: text}}, nil
	}
	if opts.Dir == "" {
		return nil, nil
	}
	return loadDir(opts.Dir, opts.DefaultLanguage)
}

func loadDir(dir, defaultLang string) ([]LocaleNote, error) {
	entries, err := osReadDir(dir)
	if err != nil {
		return nil, err
	}
	if err := ValidateDirLocales(dir); err != nil {
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
	if hasDefault && defaultLang == OfflineDefaultLanguage && len(out) > 0 {
		return out, nil
	}
	if hasDefault {
		if defaultLang == "" {
			return nil, &ValidationError{
				Message: "DefaultLanguage is required when default.txt is present in the release-notes directory (no locale to assign the fallback to)",
			}
		}
		if !covers(out, defaultLang) {
			path := filepath.Join(dir, defaultLocaleFile+".txt")
			raw, err := readCapped(root, path)
			if err != nil {
				return nil, err
			}
			text := trimTrailingWS(string(raw))
			if err := checkNote(defaultLang, path, text); err != nil {
				return nil, err
			}
			out = append(out, LocaleNote{Locale: defaultLang, Text: text})
		}
	}
	return out, nil
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

func covers(out []LocaleNote, locale string) bool {
	for _, n := range out {
		if strings.EqualFold(n.Locale, locale) {
			return true
		}
	}
	return false
}

const defaultLocaleFile = "default"

func trimTrailingWS(s string) string {
	return strings.TrimRight(s, " \t\n\r")
}
