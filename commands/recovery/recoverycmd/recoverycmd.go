// Package recoverycmd holds the wiring shared by the `gplay recovery` leaves:
// package resolution, the ADR-0018 list-table machinery, and 404/403 hint
// classification. Mirrors commands/team/teamcmd.
package recoverycmd

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/recovery"
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

// Row is the synthesized one-line-per-recovery view.
type Row struct {
	ID      string
	Status  string
	Created string
}

// BuildRow / BuildRows project API actions into rows.
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

// Columns is the single source of truth for the recovery table (ADR-0018). Keys
// mirror API field names (ADR-0003).
var Columns = output.NewColumnSet(
	output.Column[Row]{Key: "id", Header: "ID", Value: func(r Row) string { return r.ID }},
	output.Column[Row]{Key: "status", Header: "STATUS", Value: func(r Row) string { return r.Status }},
	output.Column[Row]{Key: "created", Header: "CREATED", Value: func(r Row) string { return r.Created }},
)

func ResolveColumns(spec string) ([]output.Column[Row], error) { return Columns.Resolve(spec) }
func DefaultColumns() string                                   { return strings.Join(Columns.DefaultKeys(), ",") }

// packageNotFoundError / forbiddenError attach actionable hints, leaving the
// wrapped *api.Error to drive the exit code.
type packageNotFoundError struct {
	pkg   string
	cause error
}

func (e *packageNotFoundError) Error() string {
	return fmt.Sprintf("package %q not found, or the recovery action / versionCode does not exist: verify with `gplay apps list` and `gplay recovery list --version-code <N>`: %v", e.pkg, e.cause)
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
