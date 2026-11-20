package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/metadata/diff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/validate"
	"github.com/PollyGlot/google-play-cli/internal/play/details"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/listings"
)

type Opts struct {
	Package string

	DryRun bool

	Confirm bool

	Prune bool

	AllowLocale []string

	ExplicitEditID string

	Commit edits.CommitOptions
}

type Result struct {
	Package    string
	DryRun     bool
	Diff       diff.Result
	Patched    map[string]json.RawMessage
	Created    []string
	Pruned     []string
	Incomplete []IncompleteLocale
}

type IncompleteLocale struct {
	Locale  string
	Missing []string
}

type LocaleError struct {
	Locale string
	Err    error
}

func (e *LocaleError) Error() string { return e.Err.Error() }
func (e *LocaleError) Unwrap() error { return e.Err }

func confirmRequired(prune bool) error {
	if prune {
		return exit.SafetyFlag("confirm", "metadata apply --prune publishes to live production and deletes online locales; pass --confirm to proceed (preview first with --dry-run)")
	}
	return exit.SafetyFlag("confirm", "metadata apply publishes Listings live to all users immediately; pass --confirm to proceed (preview first with --dry-run)")
}

type ValidationError struct{ Problems []validate.Problem }

func (e *ValidationError) Error() string {
	lines := make([]string, 0, len(e.Problems)+1)
	lines = append(lines, fmt.Sprintf("metadata validation failed (%d problem(s)):", len(e.Problems)))
	for _, p := range e.Problems {
		lines = append(lines, "  - "+p.Message)
	}
	return strings.Join(lines, "\n")
}

func (e *ValidationError) ExitCode() int { return 20 }

type PruneDefaultLanguageError struct{ Locale string }

func (e *PruneDefaultLanguageError) Error() string {
	return fmt.Sprintf(
		"refusing to prune the defaultLanguage Listing %q: Play requires it to exist; add %q to your metadata tree, or run apply without --prune",
		e.Locale, e.Locale)
}

func (e *PruneDefaultLanguageError) ExitCode() int { return 2 }

type EmptyTreePruneError struct{}

func (e *EmptyTreePruneError) Error() string {
	return "refusing to --prune against an empty local metadata tree: this would delete every online locale's Listing except the defaultLanguage. Check --dir points at your metadata directory (run `gplay metadata pull` to populate it), or drop --prune"
}

func (e *EmptyTreePruneError) ExitCode() int { return 2 }

var errNoChanges = errors.New("metadata: no changes to apply")

func Apply(ctx context.Context, hc *http.Client, local listing.Tree, opts Opts) (*Result, error) {
	if !opts.DryRun && !opts.Confirm {
		return nil, confirmRequired(opts.Prune)
	}

	if blocking := blockingProblems(local, allowSet(opts.AllowLocale)); len(blocking) > 0 {
		return nil, &ValidationError{Problems: blocking}
	}

	if opts.Prune && len(local) == 0 {
		return nil, &EmptyTreePruneError{}
	}

	result := &Result{Package: opts.Package, DryRun: opts.DryRun}

	if opts.DryRun {
		if err := edits.WithReadOnlyEdit(ctx, hc, opts.Package, func(editID string) error {
			d, online, err := plan(ctx, hc, opts, editID, local)
			if err != nil {
				return err
			}
			result.Diff = d
			result.Incomplete = incompleteCreates(local, online, d)
			return nil
		}); err != nil {
			return nil, err
		}
		return result, nil
	}

	patched := make(map[string]json.RawMessage)
	var created, pruned []string
	err := edits.WithEdit(ctx, hc, opts.Package, edits.Options{ExplicitEditID: opts.ExplicitEditID, Commit: opts.Commit}, func(editID string) error {
		d, online, err := plan(ctx, hc, opts, editID, local)
		if err != nil {
			return err
		}
		result.Diff = d
		if !d.HasChanges() {
			return errNoChanges
		}
		result.Incomplete = incompleteCreates(local, online, d)
		for _, loc := range changedLocales(d) {
			body := writeBody(local, loc, d)
			write, isNew := listings.Patch, false
			if _, live := online[loc]; !live {
				write, isNew = listings.Update, true
			}
			raw, e := write(ctx, hc, opts.Package, editID, loc, body)
			if e != nil {
				return &LocaleError{Locale: loc, Err: e}
			}
			patched[loc] = raw
			if isNew {
				created = append(created, loc)
			}
		}
		for _, loc := range deleteLocales(d) {
			if e := listings.Delete(ctx, hc, opts.Package, editID, loc); e != nil {
				return &LocaleError{Locale: loc, Err: e}
			}
			pruned = append(pruned, loc)
		}
		return nil
	})
	if err != nil && !errors.Is(err, errNoChanges) {
		return nil, err
	}
	if len(patched) > 0 {
		result.Patched = patched
	}
	result.Created = created
	result.Pruned = pruned
	return result, nil
}

func plan(ctx context.Context, hc *http.Client, opts Opts, editID string, local listing.Tree) (diff.Result, listing.Tree, error) {
	apiListings, _, err := listings.List(ctx, hc, opts.Package, editID)
	if err != nil {
		return diff.Result{}, nil, err
	}
	online := onlineTree(apiListings)
	d := diff.Compute(opts.Package, local, online, opts.Prune)

	if opts.Prune && d.Summary.Delete > 0 {
		defLang, err := details.GetDefaultLanguage(ctx, hc, opts.Package, editID)
		if err != nil {
			return diff.Result{}, nil, err
		}
		for _, c := range d.Changes {
			if c.Op == diff.OpDelete && c.Locale == defLang {
				return diff.Result{}, nil, &PruneDefaultLanguageError{Locale: defLang}
			}
		}
	}
	return d, online, nil
}

func onlineTree(apiListings []listings.Listing) listing.Tree {
	tr := make(listing.Tree, len(apiListings))
	for _, al := range apiListings {
		l := listing.NewListing(al.Language)
		if al.Title != "" {
			l.Set(listing.Title, al.Title)
		}
		if al.ShortDescription != "" {
			l.Set(listing.ShortDescription, al.ShortDescription)
		}
		if al.FullDescription != "" {
			l.Set(listing.FullDescription, al.FullDescription)
		}
		if al.Video != "" {
			l.Set(listing.Video, al.Video)
		}
		tr[al.Language] = l
	}
	return tr
}

func blockingProblems(local listing.Tree, allow map[string]bool) []validate.Problem {
	var out []validate.Problem
	for _, p := range validate.Validate(local, allow) {
		if p.Kind == validate.KindRequiredMissing {
			continue
		}
		out = append(out, p)
	}
	return out
}

func incompleteCreates(local, online listing.Tree, d diff.Result) []IncompleteLocale {
	created := make(map[string]bool)
	for _, loc := range changedLocales(d) {
		if _, live := online[loc]; !live {
			created[loc] = true
		}
	}
	var out []IncompleteLocale
	for _, p := range validate.Validate(local, nil) {
		if p.Kind != validate.KindRequiredMissing || !created[p.Locale] {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Locale == p.Locale {
			out[n-1].Missing = append(out[n-1].Missing, p.Field)
			continue
		}
		out = append(out, IncompleteLocale{Locale: p.Locale, Missing: []string{p.Field}})
	}
	return out
}

func allowSet(locales []string) map[string]bool {
	if len(locales) == 0 {
		return nil
	}
	m := make(map[string]bool, len(locales))
	for _, l := range locales {
		m[l] = true
	}
	return m
}

func changedLocales(d diff.Result) []string {
	seen := make(map[string]bool)
	for _, c := range d.Changes {
		switch c.Op {
		case diff.OpCreate, diff.OpUpdate, diff.OpClear:
			seen[c.Locale] = true
		case diff.OpUnchanged, diff.OpUntouchedLocale, diff.OpDelete:
		}
	}
	out := make([]string, 0, len(seen))
	for loc := range seen {
		out = append(out, loc)
	}
	sort.Strings(out)
	return out
}

func deleteLocales(d diff.Result) []string {
	var out []string
	for _, c := range d.Changes {
		if c.Op == diff.OpDelete {
			out = append(out, c.Locale)
		}
	}
	sort.Strings(out)
	return out
}

func writeBody(local listing.Tree, loc string, d diff.Result) []byte {
	m := map[string]string{"language": loc}
	ll := local[loc]
	for _, c := range d.Changes {
		if c.Locale != loc {
			continue
		}
		switch c.Op {
		case diff.OpCreate, diff.OpUpdate, diff.OpClear:
			spec, ok := listing.SpecByKey(c.Field)
			if !ok {
				continue
			}
			v, _ := ll.Get(spec.Field)
			m[c.Field] = v
		case diff.OpUnchanged, diff.OpUntouchedLocale, diff.OpDelete:
		}
	}
	body, _ := json.Marshal(m)
	return body
}
