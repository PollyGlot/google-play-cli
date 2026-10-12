// Package reports reads the Bulk reports Play deposits in the developer's
// Reporting bucket: the monthly statistics and financial exports that no JSON
// API serves (PRD #693, ADR-0050). It owns the three things every family
// shares, so a `gplay reports <family>` command stays thin:
//
//   - the family catalogue, declared as data (where a family's exports live,
//     how their object names read, which permission they need);
//   - the selection of the exports to read (latest month, a month range, the
//     split, validated against what the bucket holds, never a frozen list);
//   - the header-driven decoder and the range merge (Decode, Merge) and the
//     JSON envelope (Report).
//
// The month helpers shared with `reviews history` live here too (month.go).
package reports

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/play/gcs"
	"github.com/PollyGlot/google-play-cli/internal/reports/bucket"
)

// Scope says whom a family's exports are about.
type Scope int

const (
	// PerPackage exports carry the package in their name (the statistics):
	// <prefix><package>_<YYYYMM>[_<split>].csv.
	PerPackage Scope = iota
	// AccountWide exports cover every app of the developer account (the
	// finance families): <prefix><YYYYMM>[_<suffix>].<ext>.
	AccountWide
)

// Family is one kind of Bulk report, declared as data: adding a family is one
// entry in catalogue plus its command registration.
type Family struct {
	// Name is the command name, the --family value and report.family in JSON.
	Name string
	// Prefix is the object-key prefix up to the variable part of the name,
	// e.g. "stats/installs/installs_".
	Prefix string
	Scope  Scope
	// Permission is the global Console permission the exports require, named
	// by the 403 message.
	Permission bucket.Permission
	// Page is the Console's Download reports page listing the exports, where
	// the "Copy Cloud Storage URI" button sits.
	Page string
	// DefaultSplit is the split read when --dimension is omitted, if the
	// bucket holds it for the month; "" means the family has none.
	DefaultSplit string
	// MaxObjectBytes caps the bytes fetched for one export.
	MaxObjectBytes int64
	// TableHidden lists the column keys the table leaves out by default (a
	// column constant across rows, or a rarely read counter). Every other
	// column of the header is shown, so a column Google adds appears; when
	// the header holds nothing else, every column is.
	TableHidden []string

	// Summary is the one-line help; About the paragraph --help opens with,
	// before the shared text on months, splits and output.
	Summary string
	About   string
}

// Installs is the install statistics family: daily installs, uninstalls and
// active devices per package, split by one dimension per file.
var Installs = Family{
	Name:           "installs",
	Prefix:         "stats/installs/installs_",
	Scope:          PerPackage,
	Permission:     bucket.ViewAppInformation,
	Page:           "Statistics",
	DefaultSplit:   "overview",
	MaxObjectBytes: gcs.MaxObjectRead,
	TableHidden:    []string{"packageName", "installEvents", "updateEvents", "uninstallEvents", "dailyDeviceUpgrades"},
	Summary:        "Read the monthly install statistics of a package from the Reporting bucket",
	About: `Read the daily install statistics of --package (device and user installs,
uninstalls, active devices) from the monthly Bulk reports Play exports to
gs://<bucket>/stats/installs/installs_<package>_<YYYYMM>_<dimension>.csv.
Play publishes a month 3 to 7 days after it ends.`,
}

// catalogue is every family gplay reads, in `reports list` order.
var catalogue = []Family{Installs}

// Catalogue returns every known family, in `reports list` order.
func Catalogue() []Family { return append([]Family(nil), catalogue...) }

// Names returns the known family names, in catalogue order.
func Names() []string {
	out := make([]string, len(catalogue))
	for i, f := range catalogue {
		out[i] = f.Name
	}
	return out
}

// ListPrefix is the object-key prefix to list the family's exports under:
// narrowed to one package when pkg is set and the family is per package.
func (f Family) ListPrefix(pkg string) string {
	if pkg != "" && f.Scope == PerPackage {
		return f.Prefix + pkg + "_"
	}
	return f.Prefix
}

// Object is one monthly export, as the bucket listing names it.
type Object struct {
	Family  string
	Package string // "" for an account-wide family
	Month   string // YYYYMM
	Split   string // the dimension (or name suffix); "" when the name has none
	Name    string // the full object key
	Size    string // bytes, as the listing reports them
	Updated string // RFC 3339, as the listing reports it
}

// Objects parses a bucket listing into the family's exports, sorted by
// package, month and split. Names are read leniently: the package may hold
// underscores (the month segment anchors the parse), the extension's case is
// ignored, and the split is whatever follows the month. When pkg is set, only
// that package's exports are kept: the list prefix for com.x also matches
// com.x_y. Names that do not parse are returned in skipped, for a verbose log.
func (f Family) Objects(listing []gcs.Object, pkg string) (objs []Object, skipped []string) {
	re := f.nameRE()
	for _, o := range listing {
		m := re.FindStringSubmatch(o.Name)
		if m == nil {
			skipped = append(skipped, o.Name)
			continue
		}
		obj := Object{Family: f.Name, Name: o.Name, Size: o.Size, Updated: o.Updated}
		if f.Scope == PerPackage {
			obj.Package, obj.Month, obj.Split = m[1], m[2], m[3]
		} else {
			obj.Month, obj.Split = m[1], m[2]
		}
		if pkg != "" && obj.Package != pkg {
			continue
		}
		objs = append(objs, obj)
	}
	sort.SliceStable(objs, func(i, j int) bool {
		a, b := objs[i], objs[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Month != b.Month {
			return a.Month < b.Month
		}
		return a.Split < b.Split
	})
	return objs, skipped
}

// nameRE matches the family's object names. For a per-package family the
// greedy package group stops at the last _YYYYMM, so a package holding
// underscores still parses.
func (f Family) nameRE() *regexp.Regexp {
	if f.Scope == AccountWide {
		return regexp.MustCompile(`^` + regexp.QuoteMeta(f.Prefix) + `(\d{6})(?:_([^/]+?))?\.(?i:csv|zip)$`)
	}
	return regexp.MustCompile(`^` + regexp.QuoteMeta(f.Prefix) + `([^/]+)_(\d{6})(?:_([^/]+?))?\.(?i:csv)$`)
}

// ErrNoReport means the bucket holds no export matching the request: the
// package has none for the family yet, or none for the months asked.
var ErrNoReport = errors.New("no report")

// SplitError means the split asked for (or the family's default, when none
// was) is not in the bucket for the months selected. It names the splits that
// are, so the caller is never left guessing.
type SplitError struct {
	Requested string   // "" when the default was looked for
	Default   string   // the family's default split
	Months    []string // YYYYMM months looked at
	Available []string // splits present for those months, sorted
}

func (e *SplitError) Error() string {
	months := make([]string, len(e.Months))
	for i, m := range e.Months {
		months[i] = DisplayMonth(m)
	}
	when := strings.Join(months, ", ")
	if len(months) > 3 {
		when = months[0] + ".." + months[len(months)-1]
	}
	avail := strings.Join(e.Available, ", ")
	if e.Requested == "" {
		return fmt.Sprintf("no %q report for %s, so no default dimension: pass --dimension with one of: %s", e.Default, when, avail)
	}
	return fmt.Sprintf("dimension %q has no report for %s: pass --dimension with one of: %s", e.Requested, when, avail)
}

// Selection is the exports to read for one request.
type Selection struct {
	Split   string   // the split read ("" for a family without one)
	Months  []string // YYYYMM months read, chronological
	Objects []Object // one per month read, chronological
	Missing []string // YYYYMM months of the request with no export for Split
}

// Select picks the exports to read among objs (one family, one package). With
// no months it reads the latest month present; otherwise each month given (a
// --month, or a --since/--until range). The split is checked against the
// splits the bucket holds for those months: an empty split picks the family's
// default when present, and fails naming the present splits otherwise. A
// month without an export for the split lands in Missing (a range skips it);
// when nothing at all matches, the error is ErrNoReport.
func (f Family) Select(objs []Object, months []string, split string) (Selection, error) {
	if len(objs) == 0 {
		return Selection{}, ErrNoReport
	}
	if len(months) == 0 {
		latest := ""
		for _, o := range objs {
			if o.Month > latest {
				latest = o.Month
			}
		}
		months = []string{latest}
	}
	want := make(map[string]bool, len(months))
	for _, m := range months {
		want[m] = true
	}
	byKey := map[string]Object{} // month + "/" + split
	present := map[string]bool{}
	for _, o := range objs {
		if want[o.Month] {
			byKey[o.Month+"/"+o.Split] = o
			present[o.Split] = true
		}
	}
	if len(present) == 0 {
		return Selection{}, ErrNoReport
	}
	requested := split
	if split == "" {
		split = f.DefaultSplit
	}
	if !present[split] {
		avail := make([]string, 0, len(present))
		for s := range present {
			avail = append(avail, s)
		}
		sort.Strings(avail)
		return Selection{}, &SplitError{Requested: requested, Default: f.DefaultSplit, Months: months, Available: avail}
	}
	sel := Selection{Split: split}
	for _, m := range months {
		o, ok := byKey[m+"/"+split]
		if !ok {
			sel.Missing = append(sel.Missing, m)
			continue
		}
		sel.Months = append(sel.Months, m)
		sel.Objects = append(sel.Objects, o)
	}
	return sel, nil
}
