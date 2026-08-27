package signingcmd

import (
	"fmt"
	"io"
	"strings"

	"encoding/json"

	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
)

// CertRow is the synthesized one-line-per-certificate view: which certificate
// the hashes belong to, then the three digests the API returns.
type CertRow struct {
	Certificate string // "signing" | "upload" | "rotatedKey"
	MD5         string
	SHA1        string
	SHA256      string
}

// NewCertRow projects a CertificateHashes into a row. A nil pointer yields no
// row: gplay never fabricates a hash the API did not send (ADR-0003).
func NewCertRow(certificate string, h *appsigning.CertificateHashes) (CertRow, bool) {
	if h == nil {
		return CertRow{}, false
	}
	return CertRow{
		Certificate: certificate,
		MD5:         h.CertificateHashMd5,
		SHA1:        h.CertificateHashSha1,
		SHA256:      h.CertificateHashSha256,
	}, true
}

// Columns is the single source of truth for the signing table (ADR-0018). The
// hash keys mirror the API field names (ADR-0003).
var Columns = output.NewColumnSet(
	output.Column[CertRow]{Key: "certificate", Header: "CERTIFICATE", Value: func(r CertRow) string { return r.Certificate }},
	output.Column[CertRow]{Key: "certificateHashSha256", Header: "SHA256", Value: func(r CertRow) string { return r.SHA256 }},
	output.Column[CertRow]{Key: "certificateHashSha1", Header: "SHA1", Value: func(r CertRow) string { return r.SHA1 }},
	output.Column[CertRow]{Key: "certificateHashMd5", Header: "MD5", Value: func(r CertRow) string { return r.MD5 }},
)

// defaultCols is Columns in declaration order. Resolve only errors on a
// non-empty spec, and the signing leaves expose no --columns flag (they return
// at most two rows), so the error is structurally impossible here.
func defaultCols() []output.Column[CertRow] {
	cols, _ := Columns.Resolve("")
	return cols
}

// Payload renders a signing write (enroll/rotate). On --dry-run it is the
// gplay-shaped rehearsal carrying the ADR-0017 `requires` array; on a live write
// --output json passes the API response through verbatim (ADR-0003) and the
// human views print the returned certificate hashes.
type Payload struct {
	Verb     string // "enroll" | "rotate"
	Package  string
	Rows     []CertRow
	Requires []string
	DryRun   bool
	Raw      json.RawMessage
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderTable(w) },
		JSON:     func(w io.Writer) error { return p.renderJSON(w) },
		Markdown: func(w io.Writer) error { return p.renderMarkdown(w) },
	}
}

func (p Payload) renderTable(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "would %s app signing for %s (dry-run); requires: %s\n", p.Verb, p.Package, strings.Join(p.Requires, ", "))
		return err
	}
	return output.RenderTable(w, defaultCols(), p.Rows)
}

func (p Payload) renderMarkdown(w io.Writer) error {
	if p.DryRun {
		_, err := fmt.Fprintf(w, "- **dry-run**: would %s app signing for `%s` (requires: %s)\n", p.Verb, p.Package, strings.Join(p.Requires, ", "))
		return err
	}
	if _, err := fmt.Fprintf(w, "## signing %s: %s\n\n", p.Verb, p.Package); err != nil {
		return err
	}
	return output.RenderMarkdown(w, defaultCols(), p.Rows)
}

type dryRunView struct {
	DryRun   bool     `json:"dryRun"`
	Action   string   `json:"action"`
	Package  string   `json:"package"`
	Requires []string `json:"requires"`
}

func (p Payload) renderJSON(w io.Writer) error {
	if p.DryRun {
		return output.WriteJSON(w, dryRunView{
			DryRun:   true,
			Action:   p.Verb,
			Package:  p.Package,
			Requires: p.Requires,
		})
	}
	// Live: verbatim API pass-through (ADR-0003).
	_, err := w.Write(p.Raw)
	return err
}
