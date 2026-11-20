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

type localFileError struct {
	path  string
	cause error
}

func (e *localFileError) Error() string { return fmt.Sprintf("--file %s: %v", e.path, e.cause) }
func (e *localFileError) Unwrap() error { return e.cause }
func (e *localFileError) ExitCode() int { return 20 }

func ResolveApplicationID(flag string) (string, error) {
	id := strings.TrimSpace(flag)
	if id == "" {
		return "", &exit.UsageError{Msg: "missing --application-id: the numeric Play Games Services application ID is required"}
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return "", &exit.UsageError{Msg: "invalid --application-id: expected the numeric Play Games Services application ID, got " + strconv.Quote(id)}
		}
	}
	return id, nil
}

func RequireID(id, usage string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", &exit.UsageError{Msg: usage}
	}
	return id, nil
}

func RequireConfirm(confirm bool, msg string) error {
	if confirm {
		return nil
	}
	return exit.SafetyFlag("confirm", "%s", msg)
}

type AchievementRow struct {
	ID     string
	Type   string
	Points int
	State  string
	Name   string
}

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

func BuildAchievementRows(items []games.AchievementConfiguration) []AchievementRow {
	rows := make([]AchievementRow, 0, len(items))
	for _, a := range items {
		rows = append(rows, BuildAchievementRow(a))
	}
	return rows
}

var AchievementColumns = output.NewColumnSet(
	output.Column[AchievementRow]{Key: "id", Header: "ID", Value: func(r AchievementRow) string { return r.ID }},
	output.Column[AchievementRow]{Key: "name", Header: "NAME", Value: func(r AchievementRow) string { return r.Name }},
	output.Column[AchievementRow]{Key: "type", Header: "TYPE", Value: func(r AchievementRow) string { return r.Type }},
	output.Column[AchievementRow]{Key: "points", Header: "POINTS", Value: func(r AchievementRow) string { return strconv.Itoa(r.Points) }},
	output.Column[AchievementRow]{Key: "state", Header: "STATE", Value: func(r AchievementRow) string { return r.State }},
)

func ResolveAchievementColumns(spec string) ([]output.Column[AchievementRow], error) {
	return AchievementColumns.Resolve(spec)
}

func DefaultAchievementColumns() string { return strings.Join(AchievementColumns.DefaultKeys(), ",") }

type LeaderboardRow struct {
	ID    string
	Name  string
	Order string
	Min   string
	Max   string
}

func BuildLeaderboardRow(l games.LeaderboardConfiguration) LeaderboardRow {
	r := LeaderboardRow{ID: l.ID, Order: l.ScoreOrder, Min: l.ScoreMin, Max: l.ScoreMax}
	if d := l.Detail(); d != nil {
		r.Name = d.Name.First()
	}
	return r
}

func BuildLeaderboardRows(items []games.LeaderboardConfiguration) []LeaderboardRow {
	rows := make([]LeaderboardRow, 0, len(items))
	for _, l := range items {
		rows = append(rows, BuildLeaderboardRow(l))
	}
	return rows
}

var LeaderboardColumns = output.NewColumnSet(
	output.Column[LeaderboardRow]{Key: "id", Header: "ID", Value: func(r LeaderboardRow) string { return r.ID }},
	output.Column[LeaderboardRow]{Key: "name", Header: "NAME", Value: func(r LeaderboardRow) string { return r.Name }},
	output.Column[LeaderboardRow]{Key: "order", Header: "ORDER", Value: func(r LeaderboardRow) string { return r.Order }},
	output.Column[LeaderboardRow]{Key: "min", Header: "MIN", Value: func(r LeaderboardRow) string { return r.Min }},
	output.Column[LeaderboardRow]{Key: "max", Header: "MAX", Value: func(r LeaderboardRow) string { return r.Max }},
)

func ResolveLeaderboardColumns(spec string) ([]output.Column[LeaderboardRow], error) {
	return LeaderboardColumns.Resolve(spec)
}

func DefaultLeaderboardColumns() string { return strings.Join(LeaderboardColumns.DefaultKeys(), ",") }

const defaultLocale = "en-US"

type AchievementWrite struct {
	File          string
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
	return strings.TrimSpace(w.Name) != "" ||
		strings.TrimSpace(w.Description) != "" ||
		strings.TrimSpace(w.Type) != "" ||
		strings.TrimSpace(w.InitialState) != "" ||
		w.PointValueSet || w.StepsSet
}

func BuildAchievementBody(stdin io.Reader, w AchievementWrite, requireField bool) ([]byte, error) {
	if w.File != "" {
		if w.hasFieldFlags() {
			return nil, exit.Usagef("--file cannot be combined with field flags (--name, --description, --type, --initial-state, --point-value, --steps-to-unlock)")
		}
		return readJSONSource(stdin, w.File)
	}
	if requireField && !w.hasFieldFlags() {
		return nil, exit.Usagef("nothing to send: pass --file <file> or at least one field flag (--name, --description, --type, --initial-state, --point-value, --steps-to-unlock)")
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

type LeaderboardWrite struct {
	File        string
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

func BuildLeaderboardBody(stdin io.Reader, w LeaderboardWrite, requireField bool) ([]byte, error) {
	if w.File != "" {
		if w.hasFieldFlags() {
			return nil, exit.Usagef("--file cannot be combined with field flags (--name, --score-order, --score-min, --score-max)")
		}
		return readJSONSource(stdin, w.File)
	}
	if requireField && !w.hasFieldFlags() {
		return nil, exit.Usagef("nothing to send: pass --file <file> or at least one field flag (--name, --score-order, --score-min, --score-max)")
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
	b, err := output.Marshal(v)
	if err != nil {
		return nil, exit.Usagef("could not build request body: %v", err)
	}
	return b, nil
}

func readJSONSource(stdin io.Reader, path string) ([]byte, error) {
	var (
		raw []byte
		err error
	)
	if path == "-" {
		if stdin == nil {
			return nil, exit.Usagef("--file -: no stdin available")
		}
		raw, err = io.ReadAll(stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, &localFileError{path: path, cause: err}
	}
	if !json.Valid(raw) {
		return nil, exit.Usagef("--file %s: not valid JSON", path)
	}
	return raw, nil
}

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
