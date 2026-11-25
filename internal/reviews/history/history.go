package history

import (
	"encoding/csv"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/reports"
)

type Row struct {
	PackageName                      string `json:"packageName"`
	AppVersionCode                   string `json:"appVersionCode"`
	AppVersionName                   string `json:"appVersionName"`
	ReviewerLanguage                 string `json:"reviewerLanguage"`
	Device                           string `json:"device"`
	ReviewSubmitDateAndTime          string `json:"reviewSubmitDateAndTime"`
	ReviewSubmitMillisSinceEpoch     string `json:"reviewSubmitMillisSinceEpoch"`
	ReviewLastUpdateDateAndTime      string `json:"reviewLastUpdateDateAndTime"`
	ReviewLastUpdateMillisSinceEpoch string `json:"reviewLastUpdateMillisSinceEpoch"`
	StarRating                       string `json:"starRating"`
	ReviewTitle                      string `json:"reviewTitle"`
	ReviewText                       string `json:"reviewText"`
	DeveloperReplyDateAndTime        string `json:"developerReplyDateAndTime"`
	DeveloperReplyMillisSinceEpoch   string `json:"developerReplyMillisSinceEpoch"`
	DeveloperReplyText               string `json:"developerReplyText"`
	ReviewLink                       string `json:"reviewLink"`
}

var header = map[string]func(*Row, string){
	"Package Name":                          func(r *Row, v string) { r.PackageName = v },
	"App Version Code":                      func(r *Row, v string) { r.AppVersionCode = v },
	"App Version Name":                      func(r *Row, v string) { r.AppVersionName = v },
	"Reviewer Language":                     func(r *Row, v string) { r.ReviewerLanguage = v },
	"Device":                                func(r *Row, v string) { r.Device = v },
	"Review Submit Date and Time":           func(r *Row, v string) { r.ReviewSubmitDateAndTime = v },
	"Review Submit Millis Since Epoch":      func(r *Row, v string) { r.ReviewSubmitMillisSinceEpoch = v },
	"Review Last Update Date and Time":      func(r *Row, v string) { r.ReviewLastUpdateDateAndTime = v },
	"Review Last Update Millis Since Epoch": func(r *Row, v string) { r.ReviewLastUpdateMillisSinceEpoch = v },
	"Star Rating":                           func(r *Row, v string) { r.StarRating = v },
	"Review Title":                          func(r *Row, v string) { r.ReviewTitle = v },
	"Review Text":                           func(r *Row, v string) { r.ReviewText = v },
	"Developer Reply Date and Time":         func(r *Row, v string) { r.DeveloperReplyDateAndTime = v },
	"Developer Reply Millis Since Epoch":    func(r *Row, v string) { r.DeveloperReplyMillisSinceEpoch = v },
	"Developer Reply Text":                  func(r *Row, v string) { r.DeveloperReplyText = v },
	"Review Link":                           func(r *Row, v string) { r.ReviewLink = v },
}

func Parse(raw []byte) ([]Row, error) {
	text, err := reports.DecodeText(raw)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	rd := csv.NewReader(strings.NewReader(text))
	rd.FieldsPerRecord = -1
	records, err := rd.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse reviews CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}

	setters := make([]func(*Row, string), len(records[0]))
	for i, h := range records[0] {
		setters[i] = header[strings.TrimSpace(h)]
	}

	rows := make([]Row, 0, len(records)-1)
	for _, rec := range records[1:] {
		var row Row
		for i, cell := range rec {
			if i < len(setters) && setters[i] != nil {
				setters[i](&row, cell)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func monthRE(pkg string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|/)reviews_` + regexp.QuoteMeta(pkg) + `_(\d{6})\.csv$`)
}

func ObjectName(pkg, yyyymm string) string {
	return ReviewsPrefix(pkg) + yyyymm + ".csv"
}

func ReviewsPrefix(pkg string) string {
	return "reviews/reviews_" + pkg + "_"
}

func LatestMonth(pkg string, names []string) string {
	re := monthRE(pkg)
	var months []string
	for _, n := range names {
		if m := re.FindStringSubmatch(n); m != nil {
			months = append(months, m[1])
		}
	}
	if len(months) == 0 {
		return ""
	}
	sort.Strings(months)
	return months[len(months)-1]
}

func NormalizeMonth(month string) (string, error) { return reports.NormalizeMonth(month) }

func MonthRange(from, to string) ([]string, error) { return reports.MonthRange(from, to) }

func Merge(rows []Row) []Row {
	type keyed struct {
		row    Row
		submit int64
	}
	out := make([]keyed, 0, len(rows))
	pos := make(map[string]int, len(rows))
	for _, r := range rows {
		k := keyed{row: r, submit: millis(r.ReviewSubmitMillisSinceEpoch)}
		if r.ReviewLink == "" {
			out = append(out, k)
			continue
		}
		if i, seen := pos[r.ReviewLink]; seen {
			if millis(r.ReviewLastUpdateMillisSinceEpoch) >= millis(out[i].row.ReviewLastUpdateMillisSinceEpoch) {
				out[i] = k
			}
			continue
		}
		pos[r.ReviewLink] = len(out)
		out = append(out, k)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].submit < out[j].submit })
	merged := make([]Row, len(out))
	for i := range out {
		merged[i] = out[i].row
	}
	return merged
}

func millis(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
