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

type Scope int

const (
	PerPackage Scope = iota
	AccountWide
)

type Family struct {
	Name           string
	Prefix         string
	Scope          Scope
	PerProduct     bool
	Container      Container
	BuyerLocation  bool
	Permission     bucket.Permission
	Page           string
	DefaultSplit   string
	MaxObjectBytes int64
	TableHidden    []string

	Summary string
	About   string
}

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

var Ratings = Family{
	Name:           "ratings",
	Prefix:         "stats/ratings/ratings_",
	Scope:          PerPackage,
	Permission:     bucket.ViewAppInformation,
	Page:           "Statistics",
	DefaultSplit:   "overview",
	MaxObjectBytes: gcs.MaxObjectRead,
	TableHidden:    []string{"packageName"},
	Summary:        "Read the monthly ratings statistics of a package from the Reporting bucket",
	About: `Read the daily ratings statistics of --package (the day's average rating
and the cumulative one) from the monthly Bulk reports Play exports to
gs://<bucket>/stats/ratings/ratings_<package>_<YYYYMM>_<dimension>.csv.
Play publishes a month 3 to 7 days after it ends.`,
}

var Crashes = Family{
	Name:           "crashes",
	Prefix:         "stats/crashes/crashes_",
	Scope:          PerPackage,
	Permission:     bucket.ViewAppInformation,
	Page:           "Statistics",
	DefaultSplit:   "overview",
	MaxObjectBytes: gcs.MaxObjectRead,
	TableHidden:    []string{"packageName"},
	Summary:        "Read the monthly crash and ANR statistics of a package from the Reporting bucket",
	About: `Read the daily crash and ANR counts of --package from the monthly Bulk
reports Play exports to
gs://<bucket>/stats/crashes/crashes_<package>_<YYYYMM>_<dimension>.csv.
For clustered issues, rates and thresholds, gplay vitals reads the Play
Developer Reporting API instead. Play publishes a month 3 to 7 days after it
ends.`,
}

var StorePerformance = Family{
	Name:           "store-performance",
	Prefix:         "stats/store_performance/store_performance_",
	Scope:          PerPackage,
	Permission:     bucket.ViewAppInformation,
	Page:           "Statistics",
	MaxObjectBytes: gcs.MaxObjectRead,
	TableHidden:    []string{"packageName"},
	Summary:        "Read the monthly Store listing performance of a package from the Reporting bucket",
	About: `Read the daily Store listing performance of --package (visitors,
acquisitions and conversion rate) from the monthly Bulk reports Play exports
to gs://<bucket>/stats/store_performance/store_performance_<package>_<YYYYMM>_<dimension>.csv,
one file per breakdown: country, or traffic_source (with the search term and
UTM fields). There is no overview file, so --dimension is required in
practice. Play publishes a month 3 to 7 days after it ends.`,
}

var SubscriptionStats = Family{
	Name:           "subscription-stats",
	Prefix:         "financial-stats/subscriptions/subscriptions_",
	Scope:          PerPackage,
	PerProduct:     true,
	Permission:     bucket.ViewFinancialData,
	Page:           "Financial",
	DefaultSplit:   "country",
	MaxObjectBytes: gcs.MaxObjectRead,
	TableHidden:    []string{"packageName", "productId"},
	Summary:        "Read the monthly statistics of a subscription product from the Reporting bucket",
	About: `Read the daily statistics of the subscription product --product of
--package (new, canceled and active subscriptions) from the monthly Bulk
reports Play exports to
gs://<bucket>/financial-stats/subscriptions/subscriptions_<package>_<productId>_<YYYYMM>_<dimension>.csv.
The product id may hold underscores: the object name is read from the
package, the month and the final dimension. Run
gplay reports list --family subscription-stats --package <package> to see the
product ids present.`,
}

var Earnings = Family{
	Name:           "earnings",
	Prefix:         "earnings/earnings_",
	Scope:          AccountWide,
	Container:      ZIP,
	BuyerLocation:  true,
	Permission:     bucket.ViewFinancialData,
	Page:           "Financial",
	MaxObjectBytes: MaxZipObjectBytes,
	TableHidden:    []string{"transactionTime", "taxType", "refundType", "hardware", "currencyConversionRate", "basePlanId", "offerId", "groupId", "firstUsd1mEligible", "promotionId"},
	Summary:        "Read the monthly earnings report of the developer account from the Reporting bucket",
	About: `Read the monthly earnings report of the developer account (every charge,
fee, tax and refund Google paid out, one row each, all apps together) from the
Bulk report Play exports to gs://<bucket>/earnings/earnings_<YYYYMM>[_<suffix>].zip,
a ZIP holding the CSV. Every file of the month is read, whatever its suffix.
Play publishes a month around the 5th of the next one.`,
}

var Sales = Family{
	Name:           "sales",
	Prefix:         "sales/salesreport_",
	Scope:          AccountWide,
	Container:      ZIP,
	BuyerLocation:  true,
	Permission:     bucket.ViewFinancialData,
	Page:           "Financial",
	MaxObjectBytes: MaxZipObjectBytes,
	TableHidden:    []string{"orderChargedTimestamp", "deviceModel", "basePlanId", "offerId", "groupId", "firstUsd1mEligible", "promotionId", "couponValue", "discountRate", "featuredProductId", "priceExperimentId"},
	Summary:        "Read the monthly estimated sales report of the developer account from the Reporting bucket",
	About: `Read the monthly estimated sales report of the developer account (every
order, one row each, all apps together) from the Bulk report Play exports to
gs://<bucket>/sales/salesreport_<YYYYMM>.zip, a ZIP holding the CSV. The
figures are estimates published with low latency; gplay reports earnings has
what Google paid out.`,
}

var catalogue = []Family{Installs, Ratings, Crashes, StorePerformance, SubscriptionStats, Earnings, Sales}

func Catalogue() []Family { return append([]Family(nil), catalogue...) }

func Names() []string {
	out := make([]string, len(catalogue))
	for i, f := range catalogue {
		out[i] = f.Name
	}
	return out
}

func (f Family) ListPrefix(pkg string) string {
	if pkg != "" && f.Scope == PerPackage {
		return f.Prefix + pkg + "_"
	}
	return f.Prefix
}

func (f Family) ProductListPrefix(pkg, product string) string {
	return f.ListPrefix(pkg) + product + "_"
}

type Object struct {
	Family  string
	Package string
	Product string
	Month   string
	Split   string
	Name    string
	Size    string
	Updated string
}

func (f Family) Objects(listing []gcs.Object, pkg string) (objs []Object, skipped []string) {
	re := f.nameRE(pkg)
	for _, o := range listing {
		m := re.FindStringSubmatch(o.Name)
		if m == nil {
			skipped = append(skipped, o.Name)
			continue
		}
		obj := Object{Family: f.Name, Name: o.Name, Size: o.Size, Updated: o.Updated}
		switch {
		case f.Scope == PerPackage && f.PerProduct:
			obj.Package, obj.Product, obj.Month, obj.Split = m[1], m[2], m[3], m[4]
		case f.Scope == PerPackage:
			obj.Package, obj.Month, obj.Split = m[1], m[2], m[3]
		default:
			obj.Month, obj.Split = m[1], m[2]
		}
		if pkg != "" && f.Scope == PerPackage && obj.Package != pkg {
			continue
		}
		objs = append(objs, obj)
	}
	sort.SliceStable(objs, func(i, j int) bool {
		a, b := objs[i], objs[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Product != b.Product {
			return a.Product < b.Product
		}
		if a.Month != b.Month {
			return a.Month < b.Month
		}
		return a.Split < b.Split
	})
	return objs, skipped
}

const monthRE = `(\d{4}(?:0[1-9]|1[0-2]))`

func (f Family) nameRE(pkg string) *regexp.Regexp {
	if f.Scope == AccountWide {
		return regexp.MustCompile(`^` + regexp.QuoteMeta(f.Prefix) + monthRE + `(?:_([^/]+?))?\.(?i:csv|zip)$`)
	}
	if f.PerProduct {
		pkgRE := `([^_/]+)`
		if pkg != "" {
			pkgRE = `(` + regexp.QuoteMeta(pkg) + `)`
		}
		return regexp.MustCompile(`^` + regexp.QuoteMeta(f.Prefix) + pkgRE + `_([^/]+)_` + monthRE + `(?:_([^/]+?))?\.(?i:csv)$`)
	}
	return regexp.MustCompile(`^` + regexp.QuoteMeta(f.Prefix) + `([^/]+)_` + monthRE + `(?:_([^/]+?))?\.(?i:csv)$`)
}

var ErrNoReport = errors.New("no report")

type SplitError struct {
	Requested string
	Default   string
	Months    []string
	Available []string
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
	if e.Requested == "" && e.Default == "" {
		return fmt.Sprintf("this report has no default dimension: pass --dimension with one of: %s (present for %s)", avail, when)
	}
	if e.Requested == "" {
		return fmt.Sprintf("no %q report for %s, so no default dimension: pass --dimension with one of: %s", e.Default, when, avail)
	}
	return fmt.Sprintf("dimension %q has no report for %s: pass --dimension with one of: %s", e.Requested, when, avail)
}

type Selection struct {
	Split   string
	Months  []string
	Objects []Object
	Missing []string
}

func (f Family) Select(objs []Object, months []string, split string) (Selection, error) {
	if len(objs) == 0 {
		return Selection{}, ErrNoReport
	}
	if f.Scope == AccountWide {
		return selectAccountWide(objs, months)
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
	byKey := map[string]Object{}
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

func selectAccountWide(objs []Object, months []string) (Selection, error) {
	if len(months) == 0 {
		latest := ""
		for _, o := range objs {
			if o.Month > latest {
				latest = o.Month
			}
		}
		months = []string{latest}
	}
	byMonth := map[string][]Object{}
	for _, o := range objs {
		byMonth[o.Month] = append(byMonth[o.Month], o)
	}
	var sel Selection
	for _, m := range months {
		found := byMonth[m]
		if len(found) == 0 {
			sel.Missing = append(sel.Missing, m)
			continue
		}
		sort.SliceStable(found, func(i, j int) bool { return found[i].Name < found[j].Name })
		sel.Months = append(sel.Months, m)
		sel.Objects = append(sel.Objects, found...)
	}
	if len(sel.Objects) == 0 {
		return Selection{}, ErrNoReport
	}
	return sel, nil
}
