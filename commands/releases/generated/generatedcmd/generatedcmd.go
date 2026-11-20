package generatedcmd

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/generatedapks"
)

const (
	typeSplit      = "split"
	typeStandalone = "standalone"
	typeUniversal  = "universal"
	typeAssetSlice = "asset-slice"
	typeRecovery   = "recovery"
)

const shortHashLen = 12

type Row struct {
	Type       string
	Module     string
	ID         string
	DownloadID string
	Cert       string
}

func BuildRows(lr generatedapks.ListResponse) []Row {
	var rows []Row
	for _, g := range lr.GeneratedApks {
		cert := shortHash(g.CertificateSha256Hash)
		appendSplits := func(splits []generatedapks.SplitApk) {
			for _, s := range splits {
				rows = append(rows, Row{Type: typeSplit, Module: s.ModuleName, ID: s.SplitID, DownloadID: s.DownloadID, Cert: cert})
			}
		}
		appendStandalones := func(stones []generatedapks.StandaloneApk) {
			for _, s := range stones {
				rows = append(rows, Row{Type: typeStandalone, ID: variantID(s.VariantID), DownloadID: s.DownloadID, Cert: cert})
			}
		}
		appendSplits(g.GeneratedSplitApks)
		appendSplits(g.UnprotectedGeneratedSplitApks)
		appendStandalones(g.GeneratedStandaloneApks)
		appendStandalones(g.UnprotectedGeneratedStandaloneApks)
		if g.GeneratedUniversalApk != nil {
			rows = append(rows, Row{Type: typeUniversal, DownloadID: g.GeneratedUniversalApk.DownloadID, Cert: cert})
		}
		for _, a := range g.GeneratedAssetPackSlices {
			rows = append(rows, Row{Type: typeAssetSlice, Module: a.ModuleName, ID: a.SliceID, DownloadID: a.DownloadID, Cert: cert})
		}
		for _, r := range g.GeneratedRecoveryModules {
			rows = append(rows, Row{Type: typeRecovery, Module: r.ModuleName, ID: r.RecoveryID, DownloadID: r.DownloadID, Cert: cert})
		}
	}
	return rows
}

func variantID(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func shortHash(h string) string {
	if len(h) <= shortHashLen {
		return h
	}
	return h[:shortHashLen] + "…"
}

var Columns = output.NewColumnSet(
	output.Column[Row]{Key: "type", Header: "TYPE", Value: func(r Row) string { return r.Type }},
	output.Column[Row]{Key: "module", Header: "MODULE", Value: func(r Row) string { return r.Module }},
	output.Column[Row]{Key: "id", Header: "ID", Value: func(r Row) string { return r.ID }},
	output.Column[Row]{Key: "downloadId", Header: "DOWNLOAD-ID", Value: func(r Row) string { return r.DownloadID }},
	output.Column[Row]{Key: "cert", Header: "CERT", Value: func(r Row) string { return r.Cert }},
)

func ResolveColumns(spec string) ([]output.Column[Row], error) { return Columns.Resolve(spec) }
func DefaultColumns() string                                   { return strings.Join(Columns.DefaultKeys(), ",") }

type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found, or no APKs were generated for that versionCode: verify with `gplay apps list` and check the version code of an uploaded bundle: %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

func Classify(pkg string, err error) error {
	if apihint.Status(err) == http.StatusNotFound {
		return &packageNotFoundError{pkg: pkg, cause: err}
	}
	return apihint.Forbidden(pkg, err)
}
