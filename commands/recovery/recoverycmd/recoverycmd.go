package recoverycmd

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
)

type Row struct {
	ID      string
	Status  string
	Created string
}

func BuildRow(a recovery.Action) Row {
	return Row{ID: a.AppRecoveryID, Status: a.Status, Created: a.CreateTime}
}

func BuildRows(actions []recovery.Action) []Row {
	rows := make([]Row, 0, len(actions))
	for _, a := range actions {
		rows = append(rows, BuildRow(a))
	}
	return rows
}

var Columns = output.NewColumnSet(
	output.Column[Row]{Key: "id", Header: "ID", Value: func(r Row) string { return r.ID }},
	output.Column[Row]{Key: "status", Header: "STATUS", Value: func(r Row) string { return r.Status }},
	output.Column[Row]{Key: "created", Header: "CREATED", Value: func(r Row) string { return r.Created }},
)

func ResolveColumns(spec string) ([]output.Column[Row], error) { return Columns.Resolve(spec) }
func DefaultColumns() string                                   { return strings.Join(Columns.DefaultKeys(), ",") }

type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found, or the recovery action / versionCode does not exist: verify with `gplay apps list` and `gplay recovery list --version-code <N>`: %v", e.pkg, e.cause)
}
func (e *packageNotFoundError) Unwrap() error { return e.cause }

func Classify(pkg string, err error) error {
	if apihint.Status(err) == http.StatusNotFound {
		return &packageNotFoundError{pkg: pkg, cause: err}
	}
	return apihint.Forbidden(pkg, err)
}
