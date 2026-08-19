// Package notes loads release notes from CLI input: either a single
// text string (assigned to the app's default language) or a directory
// of per-locale files (`<locale>.txt`) with an optional `default.txt`
// fallback for the default language.
//
// The loader is pure filesystem I/O: no HTTP, no auth. It is consumed
// by internal/releases/orchestrator, which resolves the
// DefaultLanguage from edits.details.get before calling Load.
package notes

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MaxNoteFileSize caps the size of any single release-notes file read
// from --release-notes-dir. Google Play's per-locale limit is 500
// characters; 1 MiB is orders of magnitude beyond any legitimate notes
// payload and exists purely to keep a hostile or accidentally huge file
// from OOM-ing the CLI.
const MaxNoteFileSize = 1 << 20

// osReadDir, osStat, and osReadFile are package-level variables to let
// later blocks (e.g. an injected internal/config.FS seam) swap the
// filesystem without rewriting Load. Today they delegate to os.
var (
	osReadDir  = func(dir string) ([]fs.DirEntry, error) { return os.ReadDir(dir) }
	osStat     = func(p string) (os.FileInfo, error) { return os.Stat(p) }
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
// a CodedError carrying ExitCode()=2: see cycle 12.
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
// In Text mode the single string is assigned to DefaultLanguage with
// trailing whitespace trimmed, matching how Dir mode normalizes each
// file's contents, so `--release-notes "Bug fixes\n"` and a
// `--release-notes-dir` with the same content produce identical wire
// payloads. In Dir mode every `<locale>.txt` under opts.Dir becomes one
// entry, plus an optional `default.txt` that ships as the
// DefaultLanguage entry when no explicit `<DefaultLanguage>.txt` is
// present (matched case-insensitively). Per-file reads are capped at
// MaxNoteFileSize to prevent OOM on hostile inputs. Passing both Text
// and Dir is CLI misuse and returns a ValidationError (ExitCode()=2).
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
		return []LocaleNote{{Locale: opts.DefaultLanguage, Text: trimTrailingWS(opts.Text)}}, nil
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
		text, err := readCapped(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		out = append(out, LocaleNote{Locale: locale, Text: trimTrailingWS(string(text))})
	}
	// Apply default.txt as the DefaultLanguage entry, but only if no
	// explicit per-locale file already covers DefaultLanguage.
	if hasDefault {
		if defaultLang == "" {
			return nil, &ValidationError{
				Message: "DefaultLanguage is required when default.txt is present in the release-notes directory (no locale to assign the fallback to)",
			}
		}
		if !covers(out, defaultLang) {
			text, err := readCapped(filepath.Join(dir, defaultLocaleFile+".txt"))
			if err != nil {
				return nil, err
			}
			out = append(out, LocaleNote{Locale: defaultLang, Text: trimTrailingWS(string(text))})
		}
	}
	return out, nil
}

// readCapped stat-checks p against MaxNoteFileSize before reading the
// file into memory, then verifies the post-read size as a belt-and-
// suspenders defence against TOCTOU growth. Files larger than the cap
// produce a *ValidationError naming the file and the limit: Google
// Play's per-locale notes are capped at 500 characters anyway, so 1 MiB
// is generous.
func readCapped(p string) ([]byte, error) {
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

// covers reports whether out already has an entry for locale, comparing
// case-insensitively so that e.g. `en-us.txt` in the release-notes
// directory correctly suppresses the default.txt fallback for an
// `en-US` DefaultLanguage: otherwise Google Play would reject the
// upload for emitting two entries that resolve to the same canonical
// locale.
func covers(out []LocaleNote, locale string) bool {
	for _, n := range out {
		if strings.EqualFold(n.Locale, locale) {
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
