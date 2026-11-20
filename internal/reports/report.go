package reports

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"

	"github.com/PollyGlot/google-play-cli/internal/output"
)

type Identity struct {
	Family  string   `json:"family"`
	Package string   `json:"package,omitempty"`
	Product string   `json:"product,omitempty"`
	Months  []string `json:"months"`
	Split   string   `json:"split,omitempty"`
	Objects []string `json:"objects"`
}

type Report struct {
	Identity Identity
	Table    Table
}

func (r Report) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	if err := r.WriteJSON(&b); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

func (r Report) WriteJSON(w io.Writer) error {
	id := r.Identity
	id.Months = output.NonNil(id.Months)
	id.Objects = output.NonNil(id.Objects)
	head, err := output.Marshal(struct {
		Report  Identity `json:"report"`
		Columns []Column `json:"columns"`
	}{id, output.NonNil(r.Table.Columns)})
	if err != nil {
		return err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, head, "", "  "); err != nil {
		return err
	}
	bw := bufio.NewWriterSize(w, 64<<10)
	_, _ = bw.Write(indented.Bytes()[:indented.Len()-2])
	_, _ = bw.WriteString(",\n  \"rows\": [")
	for i, row := range r.Table.Rows {
		if i > 0 {
			_ = bw.WriteByte(',')
		}
		_, _ = bw.WriteString("\n    {")
		first := true
		for _, c := range r.Table.Columns {
			v, ok := row.Get(c.Key)
			if !ok {
				continue
			}
			if !first {
				_ = bw.WriteByte(',')
			}
			first = false
			_, _ = bw.WriteString("\n      ")
			if err := writeString(bw, c.Key); err != nil {
				return err
			}
			_, _ = bw.WriteString(": ")
			if err := writeString(bw, v); err != nil {
				return err
			}
		}
		if !first {
			_, _ = bw.WriteString("\n    ")
		}
		_ = bw.WriteByte('}')
	}
	if len(r.Table.Rows) > 0 {
		_, _ = bw.WriteString("\n  ")
	}
	_, _ = bw.WriteString("]\n}\n")
	return bw.Flush()
}

func writeString(w *bufio.Writer, s string) error {
	plain := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7E || c == '"' || c == '\\' {
			plain = false
			break
		}
	}
	if plain {
		_ = w.WriteByte('"')
		_, _ = w.WriteString(s)
		_ = w.WriteByte('"')
		return nil
	}
	enc, err := output.Marshal(s)
	if err != nil {
		return err
	}
	_, _ = w.Write(enc)
	return nil
}

var _ json.Marshaler = Report{}
