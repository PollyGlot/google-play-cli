package devicetierscmd

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/devicetiers"
)

type Row struct {
	ID          string
	Groups      int
	Tiers       int
	CountrySets int
}

func BuildRow(c devicetiers.Config) Row {
	return Row{ID: c.DeviceTierConfigID, Groups: len(c.DeviceGroups), Tiers: c.Tiers(), CountrySets: len(c.UserCountrySets)}
}

func BuildRows(cfgs []devicetiers.Config) []Row {
	rows := make([]Row, 0, len(cfgs))
	for _, c := range cfgs {
		rows = append(rows, BuildRow(c))
	}
	return rows
}

var Columns = output.NewColumnSet(
	output.Column[Row]{Key: "id", Header: "ID", Value: func(r Row) string { return r.ID }},
	output.Column[Row]{Key: "groups", Header: "GROUPS", Value: func(r Row) string { return strconv.Itoa(r.Groups) }},
	output.Column[Row]{Key: "tiers", Header: "TIERS", Value: func(r Row) string { return strconv.Itoa(r.Tiers) }},
	output.Column[Row]{Key: "countrySets", Header: "COUNTRYSETS", Value: func(r Row) string { return strconv.Itoa(r.CountrySets) }},
)

func ResolveColumns(spec string) ([]output.Column[Row], error) {
	return Columns.Resolve(spec)
}

func DefaultColumns() string { return strings.Join(Columns.DefaultKeys(), ",") }

type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found, or the device tier config id does not exist: run `gplay apps list` (packages) or `gplay device-tiers list` (config ids): %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

func Classify(pkg string, err error) error {
	if apihint.Status(err) == http.StatusNotFound {
		return &packageNotFoundError{pkg: pkg, cause: err}
	}
	return apihint.Forbidden(pkg, err)
}
