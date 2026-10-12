package reports

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/output"
)

func TestReport_JSONEnvelope(t *testing.T) {
	r := Report{
		Identity: Identity{
			Family:  "installs",
			Package: "com.example.app",
			Months:  []string{"2026-08", "2026-09"},
			Split:   "country",
			Objects: []string{"stats/installs/a.csv", "stats/installs/b.csv"},
		},
		Table: tableOf([]Column{{Key: "date", Header: "Date"}, {Key: "country", Header: "Country"}, {Key: "note", Header: "Note <&>"}},
			map[string]string{"note": "a <b> & c", "date": "2026-08-31", "country": "FR"},
			map[string]string{"country": "DE", "date": "2026-09-01"},
		),
	}
	var buf bytes.Buffer
	if err := output.WriteJSON(&buf, r); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	// Row keys follow the column order, an absent cell is left out, and the
	// strings stay verbatim (no HTML escaping).
	want := `{
  "report": {
    "family": "installs",
    "package": "com.example.app",
    "months": [
      "2026-08",
      "2026-09"
    ],
    "split": "country",
    "objects": [
      "stats/installs/a.csv",
      "stats/installs/b.csv"
    ]
  },
  "columns": [
    {
      "key": "date",
      "header": "Date"
    },
    {
      "key": "country",
      "header": "Country"
    },
    {
      "key": "note",
      "header": "Note <&>"
    }
  ],
  "rows": [
    {
      "date": "2026-08-31",
      "country": "FR",
      "note": "a <b> & c"
    },
    {
      "date": "2026-09-01",
      "country": "DE"
    }
  ]
}
`
	if buf.String() != want {
		t.Errorf("JSON =\n%s\nwant\n%s", buf.String(), want)
	}
}

func TestReport_JSONEmptyIsArraysNotNull(t *testing.T) {
	var buf bytes.Buffer
	if err := output.WriteJSON(&buf, Report{Identity: Identity{Family: "installs"}}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	want := "{\n  \"report\": {\n    \"family\": \"installs\",\n    \"months\": [],\n    \"objects\": []\n  },\n  \"columns\": [],\n  \"rows\": []\n}\n"
	if buf.String() != want {
		t.Errorf("JSON =\n%s\nwant\n%s", buf.String(), want)
	}
}

// TestReport_WriteJSONMatchesTheEncoder pins the streamed envelope to the
// document encoding/json prints when the envelope is built compact and then
// indented (the way the rows were written before streaming), on the strings
// the fast path must not write raw (quotes, backslashes, controls, HTML,
// U+2028, invalid UTF-8, non-ASCII), on an empty row and on an empty table.
func TestReport_WriteJSONMatchesTheEncoder(t *testing.T) {
	cols := []Column{{Key: "a", Header: "A <&>"}, {Key: "b", Header: "B"}}
	for name, tbl := range map[string]Table{
		"tricky strings": tableOf(cols,
			map[string]string{"a": `say "hi" \ there`, "b": "tab\there\nnew"},
			map[string]string{"a": "<b>&amp;</b>", "b": "line sep x"},
			map[string]string{"a": "bad \xff utf-8", "b": "Très bien 🎉 \x7f"},
			map[string]string{},
			map[string]string{"b": "only b"},
		),
		"no row":   {Columns: cols},
		"no table": {},
	} {
		t.Run(name, func(t *testing.T) {
			r := Report{Identity: Identity{Family: "sales", Months: []string{"2026-09"}, Objects: []string{"sales/s.zip"}}, Table: tbl}
			want := referenceJSON(t, r)
			var got bytes.Buffer
			if err := r.WriteJSON(&got); err != nil {
				t.Fatalf("WriteJSON: %v", err)
			}
			if got.String() != want {
				t.Errorf("streamed =\n%s\nencoder =\n%s", got.String(), want)
			}
		})
	}
}

// referenceJSON builds the envelope with encoding/json alone: compact, rows
// in column order, then indented by an encoder without HTML escaping.
func referenceJSON(t *testing.T, r Report) string {
	t.Helper()
	str := func(v any) []byte {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			t.Fatal(err)
		}
		return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	}
	id := r.Identity
	cols := r.Table.Columns
	if cols == nil {
		cols = []Column{}
	}
	head := str(struct {
		Report  Identity `json:"report"`
		Columns []Column `json:"columns"`
	}{id, cols})
	var b bytes.Buffer
	b.Write(head[:len(head)-1])
	b.WriteString(`,"rows":[`)
	for i, row := range r.Table.Rows {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('{')
		n := 0
		for _, c := range r.Table.Columns {
			if v, ok := row.Get(c.Key); ok {
				if n > 0 {
					b.WriteByte(',')
				}
				n++
				b.Write(str(c.Key))
				b.WriteByte(':')
				b.Write(str(v))
			}
		}
		b.WriteByte('}')
	}
	b.WriteString("]}")
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(json.RawMessage(b.Bytes())); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
