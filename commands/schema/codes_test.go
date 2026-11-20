package schema

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

func TestCodesPayload_jsonExposesTheWholeCatalog(t *testing.T) {
	var buf bytes.Buffer
	r := CodesPayload{}.Renderers()
	if err := r.JSON(&buf); err != nil {
		t.Fatalf("render JSON: %v", err)
	}
	var view struct {
		Codes []exit.CodeDoc `json:"codes"`
	}
	if err := json.Unmarshal(buf.Bytes(), &view); err != nil {
		t.Fatalf("codes JSON is malformed: %v\n%s", err, buf.String())
	}
	want := exit.CodeCatalog()
	if len(view.Codes) != len(want) {
		t.Fatalf("JSON carries %d codes, want the full catalog of %d", len(view.Codes), len(want))
	}
	for i, d := range want {
		if view.Codes[i] != d {
			t.Errorf("codes[%d] = %+v, want %+v", i, view.Codes[i], d)
		}
	}
}

func TestCodesPayload_humanViewsNameEveryCode(t *testing.T) {
	r := CodesPayload{}.Renderers()
	for name, render := range map[string]func(io.Writer) error{
		"table":    r.Table,
		"markdown": r.Markdown,
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := render(&buf); err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, d := range exit.CodeCatalog() {
				if !strings.Contains(buf.String(), string(d.Code)) {
					t.Errorf("%s view omits %q", name, d.Code)
				}
			}
		})
	}
}

func TestCodesPayload_tableIsTheSharedRenderer(t *testing.T) {
	var buf bytes.Buffer
	r := CodesPayload{}.Renderers()
	if err := r.Table(&buf); err != nil {
		t.Fatalf("render table: %v", err)
	}
	if buf.String() != exit.CodeTableString() {
		t.Errorf("table view diverged from the shared renderer:\ngot:\n%s\nwant:\n%s", buf.String(), exit.CodeTableString())
	}
}

func TestRun_codesShortCircuits(t *testing.T) {
	payload, err := Run(nil, Input{Codes: true, Query: "edits.tracks.update", List: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, ok := payload.(CodesPayload); !ok {
		t.Fatalf("payload = %T, want CodesPayload", payload)
	}
}
