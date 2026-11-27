package notes_test

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/releases/notes"
)

func TestLoad_textMode_singleEntryForDefaultLanguage(t *testing.T) {
	got, err := notes.Load(notes.Opts{
		Text:            "Bug fixes and performance improvements.",
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []notes.LocaleNote{
		{Locale: "en-US", Text: "Bug fixes and performance improvements."},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries (%v), want %d (%v)", len(got), got, len(want), want)
	}
	if got[0] != want[0] {
		t.Errorf("got %v, want %v", got[0], want[0])
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

func sortNotes(in []notes.LocaleNote) []notes.LocaleNote {
	out := make([]notes.LocaleNote, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool { return out[i].Locale < out[j].Locale })
	return out
}

func TestLoad_dirMode_perLocaleFiles_emitsAllLocales(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "en-US.txt", "English notes")
	writeFile(t, dir, "fr-FR.txt", "Notes en français")

	got, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got = sortNotes(got)
	want := []notes.LocaleNote{
		{Locale: "en-US", Text: "English notes"},
		{Locale: "fr-FR", Text: "Notes en français"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestLoad_dirMode_defaultTxt_emittedAsDefaultLanguage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "default.txt", "Fallback notes")

	got, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []notes.LocaleNote{
		{Locale: "en-US", Text: "Fallback notes"},
	}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLoad_dirMode_explicitLocaleWinsOverDefaultTxt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "default.txt", "Fallback content")
	writeFile(t, dir, "en-US.txt", "Specific English content")

	got, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries (%v), want 1 (en-US.txt should beat default.txt)", len(got), got)
	}
	if got[0].Locale != "en-US" || got[0].Text != "Specific English content" {
		t.Errorf("got %v, want en-US/'Specific English content'", got[0])
	}
}

func TestLoad_neitherTextNorDir_returnsEmpty(t *testing.T) {
	got, err := notes.Load(notes.Opts{DefaultLanguage: notes.KnownLanguage("en-US")})
	if err != nil {
		t.Fatalf("Load(empty opts): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty slice", got)
	}
}

func TestLoad_textMode_trimsTrailingWhitespace_matchesDirMode(t *testing.T) {
	const body = "Bug fixes and performance improvements."
	const noisy = body + "\n  \t\r\n"

	textGot, err := notes.Load(notes.Opts{
		Text:            noisy,
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	if err != nil {
		t.Fatalf("Load(Text): %v", err)
	}
	if len(textGot) != 1 || textGot[0].Locale != "en-US" || textGot[0].Text != body {
		t.Fatalf("text-mode got %v, want one entry {en-US, %q}", textGot, body)
	}

	dir := t.TempDir()
	writeFile(t, dir, "default.txt", noisy)
	dirGot, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	if err != nil {
		t.Fatalf("Load(Dir): %v", err)
	}
	if len(dirGot) != 1 || dirGot[0] != textGot[0] {
		t.Errorf("parity broken: text=%v, dir=%v", textGot, dirGot)
	}
}

func TestLoad_dirMode_fileExceedsCap_returnsExit2(t *testing.T) {
	dir := t.TempDir()
	huge := make([]byte, notes.MaxNoteFileSize+1)
	for i := range huge {
		huge[i] = 'a'
	}
	if err := os.WriteFile(filepath.Join(dir, "en-US.txt"), huge, 0644); err != nil {
		t.Fatalf("WriteFile huge: %v", err)
	}

	_, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	if err == nil {
		t.Fatal("Load(huge file): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	if !strings.Contains(err.Error(), "en-US.txt") {
		t.Errorf("error message %q does not name the offending file", err.Error())
	}
}

func TestLoad_dirMode_defaultTxtExceedsCap_returnsExit2(t *testing.T) {
	dir := t.TempDir()
	huge := make([]byte, notes.MaxNoteFileSize+1)
	for i := range huge {
		huge[i] = 'b'
	}
	if err := os.WriteFile(filepath.Join(dir, "default.txt"), huge, 0644); err != nil {
		t.Fatalf("WriteFile huge default: %v", err)
	}

	_, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	if err == nil {
		t.Fatal("Load(huge default.txt): want error, got nil")
	}
	var coder interface{ ExitCode() int }
	if !errors.As(err, &coder) {
		t.Fatalf("err = %v (%T), want one implementing ExitCode()", err, err)
	}
	if coder.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", coder.ExitCode())
	}
	if !strings.Contains(err.Error(), "default.txt") {
		t.Errorf("error message %q does not name the offending file", err.Error())
	}
}

func TestLoad_dirMode_atCapBoundary_passesTheByteCap(t *testing.T) {
	dir := t.TempDir()
	atLimit := make([]byte, notes.MaxNoteFileSize)
	for i := range atLimit {
		atLimit[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(dir, "en-US.txt"), atLimit, 0644); err != nil {
		t.Fatalf("WriteFile at-limit: %v", err)
	}

	_, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	var noteErr *notes.NoteError
	if !errors.As(err, &noteErr) {
		t.Fatalf("Load(at-cap) err = %v (%T), want *notes.NoteError: the byte cap must let it through", err, err)
	}
	if noteErr.Chars != notes.MaxNoteFileSize {
		t.Errorf("Chars = %d, want %d", noteErr.Chars, notes.MaxNoteFileSize)
	}
}

func TestLoad_noteLength(t *testing.T) {
	accented := strings.Repeat("é", notes.MaxNoteChars)
	cases := []struct {
		name     string
		opts     func(dir string) notes.Opts
		wantErr  bool
		wantText []string
	}{
		{
			name: "text mode at the limit in runes loads",
			opts: func(string) notes.Opts {
				return notes.Opts{Text: accented, DefaultLanguage: notes.KnownLanguage("fr-FR")}
			},
		},
		{
			name: "text mode one rune over",
			opts: func(string) notes.Opts {
				return notes.Opts{Text: accented + "é", DefaultLanguage: notes.KnownLanguage("fr-FR")}
			},
			wantErr:  true,
			wantText: []string{"fr-FR", "--release-notes", "501 characters", "at most 500"},
		},
		{
			name: "text mode whitespace only is empty",
			opts: func(string) notes.Opts {
				return notes.Opts{Text: "  \n\t", DefaultLanguage: notes.KnownLanguage("en-US")}
			},
			wantErr:  true,
			wantText: []string{"en-US", "--release-notes", "empty (0 characters)"},
		},
		{
			name: "dir mode empty file",
			opts: func(dir string) notes.Opts {
				writeFile(t, dir, "en-US.txt", "Fine")
				writeFile(t, dir, "fr-FR.txt", "\n")
				return notes.Opts{Dir: dir}
			},
			wantErr:  true,
			wantText: []string{"fr-FR", "fr-FR.txt", "empty (0 characters)"},
		},
		{
			name: "dir mode over-long file",
			opts: func(dir string) notes.Opts {
				writeFile(t, dir, "de-DE.txt", strings.Repeat("a", 612))
				return notes.Opts{Dir: dir}
			},
			wantErr:  true,
			wantText: []string{"de-DE", "de-DE.txt", "612 characters"},
		},
		{
			name: "dir mode over-long default.txt names the default language",
			opts: func(dir string) notes.Opts {
				writeFile(t, dir, "default.txt", strings.Repeat("a", 501))
				return notes.Opts{Dir: dir}
			},
			wantErr:  true,
			wantText: []string{"the default language", "default.txt", "501 characters"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := notes.Load(tc.opts(t.TempDir()))
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("Load: %v", err)
				}
				return
			}
			var noteErr *notes.NoteError
			if !errors.As(err, &noteErr) {
				t.Fatalf("err = %v (%T), want *notes.NoteError", err, err)
			}
			if got := noteErr.ExitCode(); got != 20 {
				t.Errorf("ExitCode() = %d, want 20 (client-side validation)", got)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestLoad_dirMode_caseInsensitiveCoversDedupes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "en-us.txt", "Lowercase locale stem")
	writeFile(t, dir, "default.txt", "Should not be emitted")

	got, err := notes.Load(notes.Opts{
		Dir:             dir,
		DefaultLanguage: notes.KnownLanguage("en-US"),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries (%v), want 1 (en-us.txt should cover en-US)", len(got), got)
	}
	if !strings.EqualFold(got[0].Locale, "en-US") {
		t.Errorf("entry locale %q does not match en-US case-insensitively", got[0].Locale)
	}
	if got[0].Text != "Lowercase locale stem" {
		t.Errorf("default.txt leaked into output: got text %q", got[0].Text)
	}
}

func TestLoad_unknownLanguage_defaultTxtRefusedOnlyWhenCertainlySent(t *testing.T) {
	covered := t.TempDir()
	writeFile(t, covered, "en-US.txt", "Bug fixes\n")
	writeFile(t, covered, "default.txt", "")
	if _, err := notes.Load(notes.Opts{Dir: covered}); err != nil {
		t.Errorf("Load(en-US.txt + empty default.txt, language unknown) = %v, want nil: the default language may be en-US", err)
	}
	if _, err := notes.Load(notes.Opts{Dir: covered, DefaultLanguage: notes.KnownLanguage("en-US")}); err != nil {
		t.Errorf("Load(default en-US) = %v, want nil: default.txt is unused", err)
	}
	var noteErr *notes.NoteError
	if _, err := notes.Load(notes.Opts{Dir: covered, DefaultLanguage: notes.KnownLanguage("fr-FR")}); !errors.As(err, &noteErr) {
		t.Errorf("Load(default fr-FR) = %v, want *notes.NoteError: default.txt ships as fr-FR", err)
	}

	alone := t.TempDir()
	writeFile(t, alone, "default.txt", "")
	if _, err := notes.Load(notes.Opts{Dir: alone}); !errors.As(err, &noteErr) {
		t.Errorf("Load(only empty default.txt, language unknown) = %v, want *notes.NoteError", err)
	}
}

func TestLoad_defaultLanguageNote_isFallbackOnlyWhileUnknown(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "default.txt", "Bug fixes")
	cases := []struct {
		name string
		opts notes.Opts
		want notes.LocaleNote
	}{
		{"dir unknown", notes.Opts{Dir: dir}, notes.LocaleNote{Text: "Bug fixes", Fallback: true}},
		{"dir known", notes.Opts{Dir: dir, DefaultLanguage: notes.KnownLanguage("fr-FR")}, notes.LocaleNote{Locale: "fr-FR", Text: "Bug fixes"}},
		{"text unknown", notes.Opts{Text: "Bug fixes"}, notes.LocaleNote{Text: "Bug fixes", Fallback: true}},
		{"text known", notes.Opts{Text: "Bug fixes", DefaultLanguage: notes.KnownLanguage("fr-FR")}, notes.LocaleNote{Locale: "fr-FR", Text: "Bug fixes"}},
	}
	for _, tc := range cases {
		got, err := notes.Load(tc.opts)
		if err != nil || len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s: Load = %+v, %v; want [%+v]", tc.name, got, err, tc.want)
		}
	}
}

func TestLoad_explicitEmptyText_isEmptyNoteError(t *testing.T) {
	const want = "release notes for the default language (--release-notes) are empty (0 characters): write them, or drop this locale"
	_, err := notes.Load(notes.Opts{TextSet: true})
	var noteErr *notes.NoteError
	if !errors.As(err, &noteErr) || err.Error() != want || noteErr.ExitCode() != 20 {
		t.Errorf("Load(TextSet, empty) = %v, want %q exit 20", err, want)
	}
	if got, err := notes.Load(notes.Opts{}); err != nil || got != nil {
		t.Errorf("Load(nothing) = %v, %v; want no notes, no error", got, err)
	}
}
