// Package devicetierscmd holds the wiring shared by the three `gplay
// device-tiers` leaves (create/view/list): the ADR-0018 shared table
// machinery and 404/403 hint classification. Keeping it in one
// place mirrors commands/team/teamcmd and keeps the leaves thin.
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

// Row is the synthesized one-line-per-config view rendered by table/markdown.
// It is NOT the API shape; the JSON view bypasses it for the raw pass-through.
type Row struct {
	ID          string
	Groups      int
	Tiers       int
	CountrySets int
}

// BuildRow projects one API config into a row.
func BuildRow(c devicetiers.Config) Row {
	return Row{ID: c.DeviceTierConfigID, Groups: len(c.DeviceGroups), Tiers: c.Tiers(), CountrySets: len(c.UserCountrySets)}
}

// BuildRows projects a slice of configs.
func BuildRows(cfgs []devicetiers.Config) []Row {
	rows := make([]Row, 0, len(cfgs))
	for _, c := range cfgs {
		rows = append(rows, BuildRow(c))
	}
	return rows
}

// Columns is the single source of truth for the device-tiers table. Keys mirror
// the API field names (ADR-0003) so an operator who saw the JSON names the same
// columns. Declaration order is the default order (ADR-0018).
var Columns = output.NewColumnSet(
	output.Column[Row]{Key: "id", Header: "ID", Value: func(r Row) string { return r.ID }},
	output.Column[Row]{Key: "groups", Header: "GROUPS", Value: func(r Row) string { return strconv.Itoa(r.Groups) }},
	output.Column[Row]{Key: "tiers", Header: "TIERS", Value: func(r Row) string { return strconv.Itoa(r.Tiers) }},
	output.Column[Row]{Key: "countrySets", Header: "COUNTRYSETS", Value: func(r Row) string { return strconv.Itoa(r.CountrySets) }},
)

// ResolveColumns turns a --columns spec into validated, ordered columns.
func ResolveColumns(spec string) ([]output.Column[Row], error) {
	return Columns.Resolve(spec)
}

// DefaultColumns is the default --columns help string.
func DefaultColumns() string { return strings.Join(Columns.DefaultKeys(), ",") }

// packageNotFoundError attaches the device-tier 404 hint (a 403 gets
// apihint.ForbiddenError), leaving the wrapped *api.Error to drive the exit
// code (404→30, 403→11).
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found, or the device tier config id does not exist: run `gplay apps list` (packages) or `gplay device-tiers list` (config ids): %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

// Classify adds the 404/403 hints to a deviceTierConfigs failure, leaving the
// wrapped *api.Error to drive the exit code. Every other failure propagates.
func Classify(pkg string, err error) error {
	if apihint.Status(err) == http.StatusNotFound {
		return &packageNotFoundError{pkg: pkg, cause: err}
	}
	return apihint.Forbidden(pkg, err)
}
