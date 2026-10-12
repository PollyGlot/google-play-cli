package reports

import (
	"bytes"
	"encoding/json"

	"github.com/PollyGlot/google-play-cli/internal/output"
)

// Identity names the exports a Report was read from: report.* in JSON.
type Identity struct {
	Family  string   `json:"family"`
	Package string   `json:"package,omitempty"`
	Product string   `json:"product,omitempty"` // the product id of a PerProduct family
	Months  []string `json:"months"`            // YYYY-MM, chronological
	Split   string   `json:"split,omitempty"`
	Objects []string `json:"objects"` // object keys read, in month order
}

// Report is the `--output json` shape of every family (ADR-0050):
//
//	{"report": {...}, "columns": [{"key", "header"}...], "rows": [{...}]}
//
// It deviates from ADR-0003 for the reason ADR-0037 gave: the upstream is a
// CSV file, not a JSON response. Each row's keys follow the column order and
// a row leaves out a column it has no cell for.
type Report struct {
	Identity Identity
	Table    Table
}

// MarshalJSON writes the envelope. A map would sort the row keys, so each row
// is written by hand in column order; strings go through output.Marshal so
// they stay verbatim (no HTML escaping).
func (r Report) MarshalJSON() ([]byte, error) {
	id := r.Identity
	id.Months = output.NonNil(id.Months)
	id.Objects = output.NonNil(id.Objects)
	head, err := output.Marshal(struct {
		Report  Identity `json:"report"`
		Columns []Column `json:"columns"`
	}{id, output.NonNil(r.Table.Columns)})
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Write(head[:len(head)-1]) // reopen the object to append "rows"
	b.WriteString(`,"rows":[`)
	for i, row := range r.Table.Rows {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('{')
		first := true
		for _, c := range r.Table.Columns {
			v, ok := row[c.Key]
			if !ok {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			if err := writeString(&b, c.Key); err != nil {
				return nil, err
			}
			b.WriteByte(':')
			if err := writeString(&b, v); err != nil {
				return nil, err
			}
		}
		b.WriteByte('}')
	}
	b.WriteString("]}")
	return b.Bytes(), nil
}

func writeString(b *bytes.Buffer, s string) error {
	enc, err := output.Marshal(s)
	if err != nil {
		return err
	}
	b.Write(enc)
	return nil
}

var _ json.Marshaler = Report{}
