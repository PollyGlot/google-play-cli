package output_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

func TestResolve_AutoNonTTY_ReturnsJSON(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, false)

	got, err := output.Resolve(output.FormatAuto, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != output.FormatJSON {
		t.Errorf("got %q, want %q", got, output.FormatJSON)
	}
}

func TestResolve_AutoTTY_ReturnsTable(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, true)

	got, err := output.Resolve(output.FormatAuto, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != output.FormatTable {
		t.Errorf("got %q, want %q", got, output.FormatTable)
	}
}

func TestResolve_AutoCIEnv_OverridesTTYToJSON(t *testing.T) {
	t.Setenv("CI", "true")
	outputtest.ForceTerminal(t, true)

	got, err := output.Resolve(output.FormatAuto, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != output.FormatJSON {
		t.Errorf("got %q, want %q (CI=true must force JSON even on a TTY)", got, output.FormatJSON)
	}
}

func TestResolve_ExplicitTable_WinsOverNonTTY(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, false)

	got, err := output.Resolve(output.FormatTable, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != output.FormatTable {
		t.Errorf("got %q, want explicit override to win", got)
	}
}

func TestRender_PicksMatchingRenderer(t *testing.T) {
	t.Setenv("CI", "")
	outputtest.ForceTerminal(t, true)

	var stdout bytes.Buffer
	rs := output.Renderers{
		Table:    func(w io.Writer) error { _, err := w.Write([]byte("TABLE")); return err },
		JSON:     func(w io.Writer) error { _, err := w.Write([]byte("JSON")); return err },
		Markdown: func(w io.Writer) error { _, err := w.Write([]byte("MD")); return err },
	}

	if err := output.Render(&stdout, output.FormatAuto, rs); err != nil {
		t.Fatalf("Render auto+TTY: %v", err)
	}
	if got := stdout.String(); got != "TABLE" {
		t.Errorf("auto+TTY rendered %q, want TABLE", got)
	}

	stdout.Reset()
	if err := output.Render(&stdout, output.FormatMarkdown, rs); err != nil {
		t.Fatalf("Render markdown: %v", err)
	}
	if got := stdout.String(); got != "MD" {
		t.Errorf("markdown rendered %q, want MD", got)
	}
}

func TestRender_NilRenderer_ReturnsUniformError(t *testing.T) {
	var stdout bytes.Buffer
	rs := output.Renderers{
		Table: func(w io.Writer) error { return nil },
		JSON:  func(w io.Writer) error { return nil },
		// Markdown intentionally nil
	}
	err := output.Render(&stdout, output.FormatMarkdown, rs)
	if err == nil {
		t.Fatal("expected error when Markdown renderer is nil")
	}
	for _, want := range []string{"unsupported", "markdown"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestMarkdownTable_HeaderSeparatorAndRows(t *testing.T) {
	var buf bytes.Buffer
	err := output.MarkdownTable(&buf, []string{"Account", "Active"}, [][]string{
		{"alpha", "*"},
		{"beta", ""},
	})
	if err != nil {
		t.Fatalf("MarkdownTable: %v", err)
	}
	want := "| Account | Active |\n| --- | --- |\n| alpha | * |\n| beta |  |\n"
	if got := buf.String(); got != want {
		t.Errorf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestMarkdownTable_NoRows_EmitsHeaderAndSeparatorOnly(t *testing.T) {
	var buf bytes.Buffer
	if err := output.MarkdownTable(&buf, []string{"A", "B"}, nil); err != nil {
		t.Fatalf("MarkdownTable: %v", err)
	}
	want := "| A | B |\n| --- | --- |\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMarkdownTable_EscapesPipesInHeadersAndCells(t *testing.T) {
	var buf bytes.Buffer
	err := output.MarkdownTable(&buf, []string{"Col|A", "B"}, [][]string{
		{"pipe|inside", "ok"},
	})
	if err != nil {
		t.Fatalf("MarkdownTable: %v", err)
	}
	want := "| Col\\|A | B |\n| --- | --- |\n| pipe\\|inside | ok |\n"
	if got := buf.String(); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMarkdownTable_RowWidthMismatch_ReturnsError(t *testing.T) {
	var buf bytes.Buffer
	err := output.MarkdownTable(&buf, []string{"A", "B"}, [][]string{
		{"a1", "b1"},
		{"only-one"},
	})
	if err == nil {
		t.Fatal("expected error for short row")
	}
	for _, want := range []string{"row 1", "1 cells", "want 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestResolve_UnknownValue_ReturnsErrorMentioningValidSet(t *testing.T) {
	_, err := output.Resolve(output.Format("xml"), &bytes.Buffer{})
	if err == nil {
		t.Fatal("Resolve(xml): expected error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"unsupported", "table", "json", "markdown", "xml"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}
