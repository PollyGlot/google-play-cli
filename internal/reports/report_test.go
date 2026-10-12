package reports

import (
	"bytes"
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
		Table: Table{
			Columns: []Column{{Key: "date", Header: "Date"}, {Key: "country", Header: "Country"}, {Key: "note", Header: "Note <&>"}},
			Rows: []Row{
				{"note": "a <b> & c", "date": "2026-08-31", "country": "FR"},
				{"country": "DE", "date": "2026-09-01"},
			},
		},
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
