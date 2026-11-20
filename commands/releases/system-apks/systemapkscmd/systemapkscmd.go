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

type Row struct {
	VariantID string
	Density   string
	ABIs      string
	Locales   string
	Options   string
}

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

func BuildRows(vs []systemapks.Variant) []Row {
	rows := make([]Row, 0, len(vs))
	for _, v := range vs {
		rows = append(rows, BuildRow(v))
	}
	return rows
}

var Columns = output.NewColumnSet(
	output.Column[Row]{Key: "variantId", Header: "VARIANT-ID", Value: func(r Row) string { return r.VariantID }},
	output.Column[Row]{Key: "density", Header: "DENSITY", Value: func(r Row) string { return r.Density }},
	output.Column[Row]{Key: "abis", Header: "ABIS", Value: func(r Row) string { return r.ABIs }},
	output.Column[Row]{Key: "locales", Header: "LOCALES", Value: func(r Row) string { return r.Locales }},
	output.Column[Row]{Key: "options", Header: "OPTIONS", Value: func(r Row) string { return r.Options }},
)

func ResolveColumns(spec string) ([]output.Column[Row], error) { return Columns.Resolve(spec) }
func DefaultColumns() string                                   { return strings.Join(Columns.DefaultKeys(), ",") }

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

func RequireVersionCode(v int64) error {
	if v <= 0 {
		return exit.Usagef("missing or invalid --version-code: the versionCode of the uploaded App Bundle is required")
	}
	return nil
}

type notFoundError struct {
	pkg   string
	cause error
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("package %q, its bundle versionCode, or the variant was not found: verify with `gplay apps list`, `gplay releases artifacts list` and `gplay releases system-apks list`: %v", e.pkg, e.cause)
}
func (e *notFoundError) Unwrap() error { return e.cause }

func Classify(pkg string, err error) error {
	if apihint.Status(err) == http.StatusNotFound {
		return &notFoundError{pkg: pkg, cause: err}
	}
	return apihint.Forbidden(pkg, err)
}
