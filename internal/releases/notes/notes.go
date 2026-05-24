// Package notes loads release notes from CLI input — either a single
// text string (assigned to the app's default language) or a directory
// of per-locale files (`<locale>.txt`) with an optional `default.txt`
// fallback for the default language.
//
// The loader is pure filesystem I/O: no HTTP, no auth. It is consumed
// by internal/releases/orchestrator, which resolves the
// DefaultLanguage from edits.details.get before calling Load.
package notes

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// osReadDir and osReadFile are package-level variables to let later
// blocks (e.g. an injected internal/config.FS seam) swap the
// filesystem without rewriting Load. Today they delegate to os.
var (
	osReadDir  = func(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }
	osReadFile = func(p string) ([]byte, error) { return os.ReadFile(p) }
)

// LocaleNote is one localized release-notes entry ready to ship as part
// of a tracks.update releases[].releaseNotes payload. Both fields are
// always populated on Load's happy path.
type LocaleNote struct {
	Locale string
	Text   string
}

// Opts is the input contract for Load. Exactly one of Text or Dir
// should be set; both empty produces an empty result (uploading without
// notes is valid). Both populated is a CLI misuse and is rejected with
// a CodedError carrying ExitCode()=2 — see cycle 12.
type Opts struct {
	// Text mode: assign this single string to DefaultLanguage.
	Text string

	// Dir mode: walk this directory, picking up `<locale>.txt` files
	// and optionally `default.txt` as the fallback for DefaultLanguage.
	Dir string

	// DefaultLanguage is the locale used by Text mode and by the
	// `default.txt` fallback in Dir mode. Required when either Text
	// or `default.txt` would emit a Locale entry.
	DefaultLanguage string
}

// ValidationError signals a CLI-misuse condition in the release-notes
// inputs. It carries the gplay exit-2 code per docs/DESIGN.md §9.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) ExitCode() int { return 2 }

// Load returns the list of (locale, text) pairs derived from opts.
// In Dir mode it reads every `<locale>.txt` under opts.Dir, plus an
// optional `default.txt` that ships as the DefaultLanguage entry when
// no explicit `<DefaultLanguage>.txt` is present. Passing both Text and
// Dir is CLI misuse and returns a ValidationError (ExitCode()=2).
func Load(opts Opts) ([]LocaleNote, error) {
	if opts.Text != "" && opts.Dir != "" {
		return nil, &ValidationError{
			Message: "release-notes and release-notes-dir are mutually exclusive — pick one",
		}
	}
	if opts.Text != "" {
		return []LocaleNote{{Locale: opts.DefaultLanguage, Text: opts.Text}}, nil
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
		text, err := osReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, LocaleNote{Locale: locale, Text: trimTrailingWS(string(text))})
	}
	// Apply default.txt as the DefaultLanguage entry, but only if no
	// explicit per-locale file already covers DefaultLanguage.
	if hasDefault && defaultLang != "" && !covers(out, defaultLang) {
		text, err := osReadFile(filepath.Join(dir, defaultLocaleFile+".txt"))
		if err != nil {
			return nil, err
		}
		out = append(out, LocaleNote{Locale: defaultLang, Text: trimTrailingWS(string(text))})
	}
	return out, nil
}

// covers reports whether out already has an entry for locale.
func covers(out []LocaleNote, locale string) bool {
	for _, n := range out {
		if n.Locale == locale {
			return true
		}
	}
	return false
}

const defaultLocaleFile = "default"

// trimTrailingWS strips trailing newlines and spaces that editors
// commonly append to text files, while preserving any meaningful
// leading or internal whitespace.
func trimTrailingWS(s string) string {
	return strings.TrimRight(s, " \t\n\r")
}
