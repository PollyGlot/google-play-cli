// Package gamescmd holds the wiring shared by the ten `gplay games` leaves
// (achievements + leaderboards × list/view/create/update/delete): Play Games
// application-ID resolution, the destructive-tier --confirm gate, the ADR-0018
// shared table machinery, request-body construction for the writes, and
// 404/403 hint classification. Keeping it in one place mirrors
// commands/device-tiers/devicetierscmd and keeps the leaves thin.
//
// Addressing rides the Play Games application ID (--application-id, numeric) for
// list/create and the resource ID (positional) for view/update/delete: a
// distinct axis from the Android package (ADR-0033), so there is no project-pin
// cascade: --application-id is simply required where the API needs it.
package gamescmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/games"
)

// usageError is a CLI-misuse error with ExitCode()=2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Usagef builds a usage error (exit 2) for the leaves to share.
func Usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// localFileError is a client-side --from-json read failure (exit 20), matching
// the convention bundles/customapps use for unreadable local inputs.
type localFileError struct {
	path  string
	cause error
}

func (e *localFileError) Error() string { return fmt.Sprintf("--from-json %s: %v", e.path, e.cause) }
func (e *localFileError) Unwrap() error { return e.cause }
func (e *localFileError) ExitCode() int { return 20 }

// ResolveApplicationID validates the required --application-id (the numeric Play
// Games Services application ID). Unlike the package axis there is no project
// pin to fall back on, so an empty value is CLI misuse (exit 2).
func ResolveApplicationID(flag string) (string, error) {
	id := strings.TrimSpace(flag)
	if id == "" {
		return "", &usageError{msg: "missing --application-id: the numeric Play Games Services application ID is required"}
	}
	// The Play Games application ID is a numeric console ID. Rejecting a
	// non-numeric value here (e.g. a package name pasted by mistake) turns a
	// misleading upstream 404 into a local, agent-resolvable exit-2 usage error.
	for _, r := range id {
		if r < '0' || r > '9' {
			return "", &usageError{msg: "invalid --application-id: expected the numeric Play Games Services application ID, got " + strconv.Quote(id)}
		}
	}
	return id, nil
}

// RequireID validates a required positional resource ID (achievement /
// leaderboard), returning a usage error (exit 2) naming the verb when empty.
func RequireID(id, usage string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", &usageError{msg: usage}
	}
	return id, nil
}

// RequireConfirm enforces the destructive-tier gate (ADR-0017): a missing
// --confirm is exit 3 (SafetyFlagError) naming the flag, deterministically
// resolvable by re-running with it.
func RequireConfirm(confirm bool, msg string) error {
	if confirm {
		return nil
	}
	return exit.SafetyFlag("confirm", "%s", msg)
}

// --- table machinery (ADR-0018) -------------------------------------------

// AchievementRow is the synthesized one-line-per-achievement view. It is NOT
// the API shape; the JSON view bypasses it for the raw pass-through (ADR-0003).
type AchievementRow struct {
	ID     string
	Type   string
	Points int
	State  string
	Name   string
}

// BuildAchievementRow projects one API achievement into a row, reading the
// draft (or, absent a draft, the published) detail for the human fields.
func BuildAchievementRow(a games.AchievementConfiguration) AchievementRow {
	r := AchievementRow{ID: a.ID, Type: a.AchievementType, State: a.InitialState}
	if d := a.Detail(); d != nil {
		if d.PointValue != nil {
			r.Points = *d.PointValue
		}
		r.Name = d.Name.First()
	}
	return r
}

// BuildAchievementRows projects a slice of achievements.
func BuildAchievementRows(items []games.AchievementConfiguration) []AchievementRow {
	rows := make([]AchievementRow, 0, len(items))
	for _, a := range items {
		rows = append(rows, BuildAchievementRow(a))
	}
	return rows
}

// AchievementColumns is the single source of truth for the achievements table.
// Keys mirror the API field names (ADR-0003); declaration order is the default.
var AchievementColumns = output.NewColumnSet(
	output.Column[AchievementRow]{Key: "id", Header: "ID", Value: func(r AchievementRow) string { return r.ID }},
	output.Column[AchievementRow]{Key: "name", Header: "NAME", Value: func(r AchievementRow) string { return r.Name }},
	output.Column[AchievementRow]{Key: "type", Header: "TYPE", Value: func(r AchievementRow) string { return r.Type }},
	output.Column[AchievementRow]{Key: "points", Header: "POINTS", Value: func(r AchievementRow) string { return strconv.Itoa(r.Points) }},
	output.Column[AchievementRow]{Key: "state", Header: "STATE", Value: func(r AchievementRow) string { return r.State }},
)

// ResolveAchievementColumns turns a --columns spec into validated columns.
func ResolveAchievementColumns(spec string) ([]output.Column[AchievementRow], error) {
	return AchievementColumns.Resolve(spec)
}

// DefaultAchievementColumns is the default --columns help string.
func DefaultAchievementColumns() string { return strings.Join(AchievementColumns.DefaultKeys(), ",") }

// LeaderboardRow is the synthesized one-line-per-leaderboard view.
type LeaderboardRow struct {
	ID    string
	Name  string
	Order string
	Min   string
	Max   string
}

// BuildLeaderboardRow projects one API leaderboard into a row.
func BuildLeaderboardRow(l games.LeaderboardConfiguration) LeaderboardRow {
	r := LeaderboardRow{ID: l.ID, Order: l.ScoreOrder, Min: l.ScoreMin, Max: l.ScoreMax}
	if d := l.Detail(); d != nil {
		r.Name = d.Name.First()
	}
	return r
}

// BuildLeaderboardRows projects a slice of leaderboards.
func BuildLeaderboardRows(items []games.LeaderboardConfiguration) []LeaderboardRow {
	rows := make([]LeaderboardRow, 0, len(items))
	for _, l := range items {
		rows = append(rows, BuildLeaderboardRow(l))
	}
	return rows
}

// LeaderboardColumns is the single source of truth for the leaderboards table.
var LeaderboardColumns = output.NewColumnSet(
	output.Column[LeaderboardRow]{Key: "id", Header: "ID", Value: func(r LeaderboardRow) string { return r.ID }},
	output.Column[LeaderboardRow]{Key: "name", Header: "NAME", Value: func(r LeaderboardRow) string { return r.Name }},
	output.Column[LeaderboardRow]{Key: "order", Header: "ORDER", Value: func(r LeaderboardRow) string { return r.Order }},
	output.Column[LeaderboardRow]{Key: "min", Header: "MIN", Value: func(r LeaderboardRow) string { return r.Min }},
	output.Column[LeaderboardRow]{Key: "max", Header: "MAX", Value: func(r LeaderboardRow) string { return r.Max }},
)

// ResolveLeaderboardColumns turns a --columns spec into validated columns.
func ResolveLeaderboardColumns(spec string) ([]output.Column[LeaderboardRow], error) {
	return LeaderboardColumns.Resolve(spec)
}

// DefaultLeaderboardColumns is the default --columns help string.
func DefaultLeaderboardColumns() string { return strings.Join(LeaderboardColumns.DefaultKeys(), ",") }

// --- write-body construction ----------------------------------------------

// defaultLocale is the BCP 47 locale a --name/--description with no --locale
// lands under.
const defaultLocale = "en-US"

// AchievementWrite carries the create/update inputs for an achievement. When
// FromJSON is set the body is that file's bytes verbatim (ADR-0003 symmetry:
// what `view --output json` emits round-trips here); otherwise the body is
// built from the field flags. The *Set flags let an explicit zero (e.g.
// --point-value 0) be distinguished from an unset flag.
type AchievementWrite struct {
	FromJSON      string
	Name          string
	Description   string
	Locale        string
	Type          string
	InitialState  string
	PointValue    int
	PointValueSet bool
	StepsToUnlock int
	StepsSet      bool
}

func (w AchievementWrite) hasFieldFlags() bool {
	// Trim the string flags so a whitespace-only value (e.g. --type=' ') is not
	// counted as set: it would otherwise pass the requireField check yet marshal
	// to nothing (the body builder trims before assigning), sending an empty body.
	return strings.TrimSpace(w.Name) != "" ||
		strings.TrimSpace(w.Description) != "" ||
		strings.TrimSpace(w.Type) != "" ||
		strings.TrimSpace(w.InitialState) != "" ||
		w.PointValueSet || w.StepsSet
}

// BuildAchievementBody resolves the request body for create/update: the
// --from-json file (verbatim, validated as JSON) or a body built from the field
// flags. The two modes are mutually exclusive. requireField rejects an empty
// flag-built body (create/update with nothing to send).
func BuildAchievementBody(stdin io.Reader, w AchievementWrite, requireField bool) ([]byte, error) {
	if w.FromJSON != "" {
		if w.hasFieldFlags() {
			return nil, Usagef("--from-json cannot be combined with field flags (--name, --description, --type, --initial-state, --point-value, --steps-to-unlock)")
		}
		return readJSONSource(stdin, w.FromJSON)
	}
	if requireField && !w.hasFieldFlags() {
		return nil, Usagef("nothing to send: pass --from-json <file> or at least one field flag (--name, --description, --type, --initial-state, --point-value, --steps-to-unlock)")
	}
	cfg := games.AchievementConfiguration{
		AchievementType: strings.TrimSpace(w.Type),
		InitialState:    strings.TrimSpace(w.InitialState),
	}
	if w.PointValueSet {
		cfg.Draft = ensureAchDraft(cfg.Draft)
		pv := w.PointValue
		cfg.Draft.PointValue = &pv
	}
	if w.StepsSet {
		steps := w.StepsToUnlock
		cfg.StepsToUnlock = &steps
	}
	locale := localeOr(w.Locale)
	if w.Name != "" {
		cfg.Draft = ensureAchDraft(cfg.Draft)
		cfg.Draft.Name = bundle(locale, w.Name)
	}
	if w.Description != "" {
		cfg.Draft = ensureAchDraft(cfg.Draft)
		cfg.Draft.Description = bundle(locale, w.Description)
	}
	return marshalBody(cfg)
}

func ensureAchDraft(d *games.AchievementConfigurationDetail) *games.AchievementConfigurationDetail {
	if d == nil {
		return &games.AchievementConfigurationDetail{}
	}
	return d
}

// LeaderboardWrite carries the create/update inputs for a leaderboard.
type LeaderboardWrite struct {
	FromJSON    string
	Name        string
	Locale      string
	ScoreOrder  string
	ScoreMin    int64
	ScoreMinSet bool
	ScoreMax    int64
	ScoreMaxSet bool
}

func (w LeaderboardWrite) hasFieldFlags() bool {
	return strings.TrimSpace(w.Name) != "" ||
		strings.TrimSpace(w.ScoreOrder) != "" ||
		w.ScoreMinSet || w.ScoreMaxSet
}

// BuildLeaderboardBody resolves the request body for create/update. scoreMin /
// scoreMax are int64s the API encodes as JSON strings, so an explicit value is
// rendered with strconv (0 is a legitimate score bound, hence the *Set flags).
func BuildLeaderboardBody(stdin io.Reader, w LeaderboardWrite, requireField bool) ([]byte, error) {
	if w.FromJSON != "" {
		if w.hasFieldFlags() {
			return nil, Usagef("--from-json cannot be combined with field flags (--name, --score-order, --score-min, --score-max)")
		}
		return readJSONSource(stdin, w.FromJSON)
	}
	if requireField && !w.hasFieldFlags() {
		return nil, Usagef("nothing to send: pass --from-json <file> or at least one field flag (--name, --score-order, --score-min, --score-max)")
	}
	cfg := games.LeaderboardConfiguration{ScoreOrder: strings.TrimSpace(w.ScoreOrder)}
	if w.ScoreMinSet {
		cfg.ScoreMin = strconv.FormatInt(w.ScoreMin, 10)
	}
	if w.ScoreMaxSet {
		cfg.ScoreMax = strconv.FormatInt(w.ScoreMax, 10)
	}
	if w.Name != "" {
		cfg.Draft = &games.LeaderboardConfigurationDetail{Name: bundle(localeOr(w.Locale), w.Name)}
	}
	return marshalBody(cfg)
}

// bundle builds a single-locale LocalizedStringBundle.
func bundle(locale, value string) *games.LocalizedStringBundle {
	return &games.LocalizedStringBundle{
		Translations: []games.LocalizedString{{Locale: locale, Value: value}},
	}
}

func localeOr(locale string) string {
	if l := strings.TrimSpace(locale); l != "" {
		return l
	}
	return defaultLocale
}

func marshalBody(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, Usagef("could not build request body: %v", err)
	}
	return b, nil
}

// readJSONSource reads the --from-json source (a file, or stdin when the path is
// "-") and validates it parses as JSON, so a malformed payload is caught here
// (exit 2) rather than as an opaque API 400. A read failure is a local IO error
// (exit 20).
func readJSONSource(stdin io.Reader, path string) ([]byte, error) {
	var (
		raw []byte
		err error
	)
	if path == "-" {
		if stdin == nil {
			return nil, Usagef("--from-json -: no stdin available")
		}
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, &localFileError{path: path, cause: err}
	}
	if !json.Valid(raw) {
		return nil, Usagef("--from-json %s: not valid JSON", path)
	}
	return raw, nil
}

// --- error classification --------------------------------------------------

type notFoundError struct {
	ref   string
	cause error
}

func (e *notFoundError) Error() string {
	return fmt.Sprintf("%q not found: check the resource ID, or the --application-id, and list with `gplay games achievements list --application-id <id>` / `gplay games leaderboards list --application-id <id>`: %v", e.ref, e.cause)
}
func (e *notFoundError) Unwrap() error { return e.cause }

type forbiddenError struct {
	ref   string
	cause error
}

func (e *forbiddenError) Error() string {
	return fmt.Sprintf("service account is not granted access to Play Games application %q: in the Play Console grant it the Play Games Services permission, then retry: %v", e.ref, e.cause)
}
func (e *forbiddenError) Unwrap() error { return e.cause }

// Classify adds the 404/403 hints to a gamesConfiguration failure, leaving the
// wrapped *api.Error to drive the exit code (404→30, 403→11). Every other
// failure propagates unchanged.
func Classify(ref string, err error) error {
	var apiErr *api.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusNotFound:
			return &notFoundError{ref: ref, cause: err}
		case http.StatusForbidden:
			return &forbiddenError{ref: ref, cause: err}
		}
	}
	return err
}
