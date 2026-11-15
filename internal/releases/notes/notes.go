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
	"unicode/utf8"

	"github.com/PollyGlot/google-play-cli/internal/pathguard"
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

// MaxNoteChars is Google Play's per-locale release-notes limit, counted in
// Unicode characters (runes), not bytes: "You can enter release notes using up
// to 500 Unicode characters per language" (Play Console Help, "Prepare and roll
// out a release", support.google.com/googleplay/android-developer/answer/9859348).
// Discovery documents no limit on LocalizedText.text, so the number lives here.
const MaxNoteChars = 500

// NoteError is a release note Google Play would refuse: empty, or longer than
// MaxNoteChars. It is client-side validation of the input's content, exit 20
// (docs/DESIGN.md §9), not CLI misuse: the flags were well-formed. Source is
// where the text came from (the file, or --release-notes) so the fix is one
// look away; Chars is the counted length after trailing-whitespace trimming,
// i.e. what would have been sent.
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

// checkNote returns a *NoteError when text is empty or over MaxNoteChars. Every
// note Load returns has passed it, so a caller holding a []LocaleNote never
// ships one Play would reject after the bundle went up (#786). It checks
// length only: whether Play serves the locale stays Play's call (no registry).
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

// Precheck loads the notes offline, with a placeholder for the default
// language, and returns the first error Load would raise. A caller runs it
// before opening an Edit: Load itself only runs once details.get has resolved
// the real default language, inside the Edit. It refuses only what that load
// would certainly send (see loadDir for default.txt).
func Precheck(text, dir string) error {
	_, err := Load(Opts{Text: text, Dir: dir, DefaultLanguage: OfflineDefaultLanguage})
	return err
}

// CheckText checks a --release-notes value the caller knows was passed, even
// empty: Opts cannot tell `--release-notes ""` from no flag, the command layer
// can, and an explicit empty value is an empty note (#786).
func CheckText(text string) error {
	return checkNote(OfflineDefaultLanguage, "--release-notes", trimTrailingWS(text))
}

// OfflineDefaultLanguage stands in for the app's default language before
// edits.details.get has named it (Precheck, a dry-run). It reads naturally in a
// NoteError ("release notes for the default language (--release-notes) ...")
// and cannot collide with a `<locale>.txt` stem: it is not a BCP-47 tag, so
// ValidateDirLocales refuses such a file.
const OfflineDefaultLanguage = "the default language"

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
	// Belt to the orchestrator's braces: the callers gate on
	// ValidateDirLocales before opening an Edit, and repeating it here means a
	// library caller (or a new call site that forgets) still cannot ship an
	// `en_US.txt` to tracks.update. It is a re-listing of an already-read
	// directory, which costs nothing next to the HTTP it prevents.
	if err := ValidateDirLocales(dir); err != nil {
		return nil, err
	}
	// Containment root, resolved once for the whole walk (PRD #459 / slice
	// #461). The entries below come from the directory itself, but an entry can
	// be a symlink pointing anywhere: without this, a `fr.txt` symlinked to
	// ~/.ssh/id_rsa would be read and shipped into a public store listing.
	// Resolved after the ReadDir above so a missing directory still reports the
	// familiar not-found error rather than a containment failure.
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
	// Apply default.txt as the DefaultLanguage entry, but only if no
	// explicit per-locale file already covers DefaultLanguage.
	//
	// Offline the language is unknown, so any `<locale>.txt` may be the one
	// that covers it: default.txt is then left to the load inside the Edit,
	// which knows the language, rather than refused here although it may
	// never ship (#786). With no `<locale>.txt` it ships whatever the
	// language, so it is checked now.
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

// readCapped checks p stays inside root, stat-checks it against
// MaxNoteFileSize before reading the file into memory, then verifies the
// post-read size as a belt-and-suspenders defence against TOCTOU growth.
// Files larger than the cap produce a *ValidationError naming the file and
// the limit: Google Play's per-locale notes are capped at 500 characters
// anyway, so 1 MiB is generous.
//
// Containment comes FIRST, before the stat: an escaping path must never be
// touched at all, so a refusal leaks nothing about whether the target exists.
func readCapped(root, p string) ([]byte, error) {
	// ContainUserPath: the notes directory and its files are the operator's
	// own, so a `fr.txt` symlinked at a shared translation is a layout they can
	// opt into (GPLAY_ALLOW_EXTERNAL_SYMLINKS), refused until they do.
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
