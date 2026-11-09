// Package systemapkscmd holds the wiring shared by the `gplay releases
// system-apks` leaves (create/list/view/download): the ADR-0018 table of
// variants, the variantId positional parser, and 404/403 hint classification.
// Mirrors commands/releases/generated/generatedcmd.
package systemapkscmd

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/systemapks"
)

// Row is the one-line view of a variant. The verbatim Variant is always
// available via --output json (ADR-0003).
type Row struct {
	VariantID string
	Density   string
	ABIs      string
	Locales   string
	Options   string
}

// BuildRow projects one variant into a row. Options lists the enabled build
// options by their gplay flag name, so a row reads back as the create call
// that produced it.
func BuildRow(v systemapks.Variant) Row {
	r := Row{VariantID: strconv.FormatUint(uint64(v.VariantID), 10)}
	if s := v.DeviceSpec; s != nil {
		if s.ScreenDensity != 0 {
			r.Density = strconv.FormatUint(uint64(s.ScreenDensity), 10)
		}
		r.ABIs = strings.Join(s.SupportedAbis, ",")
		r.Locales = strings.Join(s.SupportedLocales, ",")
	}
	if o := v.Options; o != nil {
		var opts []string
		if o.Rotated {
			opts = append(opts, "rotated-key")
		}
		if o.UncompressedDexFiles {
			opts = append(opts, "uncompressed-dex")
		}
		if o.UncompressedNativeLibraries {
			opts = append(opts, "uncompressed-native-libs")
		}
		r.Options = strings.Join(opts, ",")
	}
	return r
}

// BuildRows projects every variant, preserving the API's order.
func BuildRows(vs []systemapks.Variant) []Row {
	rows := make([]Row, 0, len(vs))
	for _, v := range vs {
		rows = append(rows, BuildRow(v))
	}
	return rows
}

// Columns is the single source of truth for the variant table (ADR-0018).
var Columns = output.NewColumnSet(
	output.Column[Row]{Key: "variantId", Header: "VARIANT-ID", Value: func(r Row) string { return r.VariantID }},
	output.Column[Row]{Key: "density", Header: "DENSITY", Value: func(r Row) string { return r.Density }},
	output.Column[Row]{Key: "abis", Header: "ABIS", Value: func(r Row) string { return r.ABIs }},
	output.Column[Row]{Key: "locales", Header: "LOCALES", Value: func(r Row) string { return r.Locales }},
	output.Column[Row]{Key: "options", Header: "OPTIONS", Value: func(r Row) string { return r.Options }},
)

func ResolveColumns(spec string) ([]output.Column[Row], error) { return Columns.Resolve(spec) }
func DefaultColumns() string                                   { return strings.Join(Columns.DefaultKeys(), ",") }

// ParseVariantID parses the <variantId> positional. The API types it uint32,
// so anything else is CLI misuse (exit 2) caught before any network.
func ParseVariantID(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, exit.Usagef("missing <variantId>: the system APK variant, from `gplay releases system-apks list`")
	}
	id, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, exit.Usagef("invalid <variantId> %q: want the unsigned integer `gplay releases system-apks list` shows", s)
	}
	return uint32(id), nil
}

// RequireVersionCode refuses a missing or non-positive --version-code (exit 2).
func RequireVersionCode(v int64) error {
	if v <= 0 {
		return exit.Usagef("missing or invalid --version-code: the versionCode of the uploaded App Bundle is required")
	}
	return nil
}

// notFoundError attaches the system APK 404 hint (a 403 gets
// apihint.ForbiddenError), leaving the wrapped *api.Error to drive the exit
// code (404→30, 403→11).
type notFoundError struct {
	pkg   string
	cause error
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("package %q, its bundle versionCode, or the variant was not found: verify with `gplay apps list`, `gplay releases artifacts list` and `gplay releases system-apks list`: %v", e.pkg, e.cause)
}
func (e *notFoundError) Unwrap() error { return e.cause }

// Classify adds 404/403 hints, leaving the *api.Error to drive the exit code.
func Classify(pkg string, err error) error {
	if apihint.Status(err) == http.StatusNotFound {
		return &notFoundError{pkg: pkg, cause: err}
	}
	return apihint.Forbidden(pkg, err)
}
