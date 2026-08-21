// Package generatedcmd holds the wiring shared by the `gplay releases generated`
// leaves (list/download): package resolution, the ADR-0018 list-table machinery
// that flattens the grouped-by-signing-key GeneratedApksListResponse to one row
// per artifact, and 404/403 hint classification. Mirrors commands/recovery/recoverycmd.
package generatedcmd

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/generatedapks"
)

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Usagef builds a usage error (exit 2).
func Usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// ResolvePackage resolves the target package: --package wins, else the project pin.
func ResolvePackage(rc *kernel.RunContext, flag string) (string, error) {
	pkg := strings.TrimSpace(flag)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", &usageError{msg: "no package: pass --package <pkg> or run gplay init in your repo"}
	}
	return pkg, nil
}

// Artifact type labels (the `type` column). Stable, lower-case, hyphenated.
const (
	typeSplit      = "split"
	typeStandalone = "standalone"
	typeUniversal  = "universal"
	typeAssetSlice = "asset-slice"
	typeRecovery   = "recovery"
)

// shortHashLen is how many leading hex chars of certificateSha256Hash the table
// shows: enough to tell two signing keys apart without a 64-char column.
const shortHashLen = 12

// Row is the synthesized one-line-per-artifact view of the grouped response.
// ID carries the artifact's natural secondary identifier (split→splitId,
// standalone→variantId, asset-slice→sliceId, recovery→recoveryId; universal has
// none). The verbatim grouping is always available via --output json (ADR-0003).
type Row struct {
	Type       string
	Module     string
	ID         string
	DownloadID string
	Cert       string
}

// BuildRows flattens every signing-key group into one row per downloadable
// artifact, preserving the API's order. Unprotected split/standalone APKs are
// included (they carry downloadIds too) under their base type.
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

// variantID renders a standalone/split variant index, "" for the zero value so
// the column stays blank rather than a misleading "0".
func variantID(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

// shortHash truncates a cert hash to its leading chars (with an ellipsis when
// truncated), leaving "" untouched.
func shortHash(h string) string {
	if len(h) <= shortHashLen {
		return h
	}
	return h[:shortHashLen] + "…"
}

// Columns is the single source of truth for the generated-APK table (ADR-0018).
// Keys mirror the artifact axes; the downloadId is what `download` consumes.
var Columns = output.NewColumnSet(
	output.Column[Row]{Key: "type", Header: "TYPE", Value: func(r Row) string { return r.Type }},
	output.Column[Row]{Key: "module", Header: "MODULE", Value: func(r Row) string { return r.Module }},
	output.Column[Row]{Key: "id", Header: "ID", Value: func(r Row) string { return r.ID }},
	output.Column[Row]{Key: "downloadId", Header: "DOWNLOAD-ID", Value: func(r Row) string { return r.DownloadID }},
	output.Column[Row]{Key: "cert", Header: "CERT", Value: func(r Row) string { return r.Cert }},
)

func ResolveColumns(spec string) ([]output.Column[Row], error) { return Columns.Resolve(spec) }
func DefaultColumns() string                                   { return strings.Join(Columns.DefaultKeys(), ",") }

// packageNotFoundError / forbiddenError attach actionable hints, leaving the
// wrapped *api.Error to drive the exit code (404→30, 403→11).
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found, or no APKs were generated for that versionCode: verify with `gplay apps list` and check the version code of an uploaded bundle: %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

type forbiddenError struct {
	pkg   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to %q: in the Play Console, open Setup → API access and grant it permission on the app: %v", e.pkg, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// Classify adds 404/403 hints, leaving the *api.Error to drive the exit code.
func Classify(pkg string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return &packageNotFoundError{pkg: pkg, cause: err}
		case http.StatusForbidden:
			return &forbiddenError{pkg: pkg, cause: err}
		}
	}
	return err
}
