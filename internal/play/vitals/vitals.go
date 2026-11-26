package vitals

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

type MetricSet struct {
	Name     string
	Resource string
}

func (s MetricSet) QueryMethodID() string {
	return "playdeveloperreporting.vitals." + s.Name + ".query"
}

func (s MetricSet) GetMethodID() string {
	return "playdeveloperreporting.vitals." + s.Name + ".get"
}

var metricSets = []MetricSet{
	{Name: "crashrate", Resource: "crashRateMetricSet"},
	{Name: "anrrate", Resource: "anrRateMetricSet"},
	{Name: "slowstartrate", Resource: "slowStartRateMetricSet"},
	{Name: "slowrenderingrate", Resource: "slowRenderingRateMetricSet"},
	{Name: "excessivewakeuprate", Resource: "excessiveWakeupRateMetricSet"},
	{Name: "lmkrate", Resource: "lmkRateMetricSet"},
	{Name: "stuckbackgroundwakelockrate", Resource: "stuckBackgroundWakelockRateMetricSet"},
	{Name: "anonrssandswapmemoryusage", Resource: "anonRssAndSwapMemoryUsageMetricSet"},
	{Name: "bitmapmemoryusage", Resource: "bitmapMemoryUsageMetricSet"},
}

func MetricSets() []MetricSet {
	out := make([]MetricSet, len(metricSets))
	copy(out, metricSets)
	return out
}

func MetricSetByName(name string) (MetricSet, bool) {
	for _, s := range metricSets {
		if s.Name == name {
			return s, true
		}
	}
	return MetricSet{}, false
}

func MetricSetNames() []string {
	names := make([]string, len(metricSets))
	for i, s := range metricSets {
		names[i] = s.Name
	}
	return names
}

const opQuery = "playdeveloperreporting.vitals.query"

func Query(ctx context.Context, hc *http.Client, set MetricSet, pkg string, body []byte) (json.RawMessage, error) {
	req := map[string]json.RawMessage{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, api.BuildFailure(api.Error{Operation: opQuery, Package: pkg, Message: "encode request: " + err.Error(), Cause: err})
		}
	}
	m, err := apiregistry.Resolve(set.QueryMethodID())
	if err != nil {
		return nil, api.BuildFailure(api.Error{Operation: opQuery, Package: pkg, Message: err.Error(), Cause: err})
	}
	rows, _, err := api.Paginate(api.Pager{Op: opQuery, Target: pkg, What: "vitals :query"},
		func(token string, _ int) ([]json.RawMessage, string, error) {
			if token != "" {
				tok, _ := json.Marshal(token)
				req["pageToken"] = tok
			} else {
				delete(req, "pageToken")
			}
			pageBody, err := json.Marshal(req)
			if err != nil {
				return nil, "", api.BuildFailure(api.Error{Operation: opQuery, Package: pkg, Message: "encode request: " + err.Error(), Cause: err})
			}
			raw, err := api.Do(ctx, hc, api.Call{Method: m, Op: opQuery, Target: pkg, Params: map[string]string{"appsId": pkg}, Body: pageBody})
			if err != nil {
				return nil, "", err
			}
			page, next, derr := decodePage(raw, "rows")
			if derr != nil {
				return nil, "", api.DecodeFailure(api.Error{Operation: opQuery, Package: pkg, Message: "decode response: " + derr.Error(), Cause: derr})
			}
			return page, next, nil
		})
	if err != nil {
		return nil, err
	}
	return rebuildEnvelope("rows", rows)
}

const opDescribe = "playdeveloperreporting.vitals.get"

func Describe(ctx context.Context, hc *http.Client, set MetricSet, pkg string) (json.RawMessage, error) {
	m, err := apiregistry.Resolve(set.GetMethodID())
	if err != nil {
		return nil, api.BuildFailure(api.Error{Operation: opDescribe, Package: pkg, Message: err.Error(), Cause: err})
	}
	return api.Do(ctx, hc, api.Call{Method: m, Op: opDescribe, Target: pkg, Params: map[string]string{"appsId": pkg}})
}

type Freshness struct {
	Period        string
	LatestEndTime string
	TimeZone      string
	LatestEnd     time.Time
}

func ParseFreshness(pkg string, body []byte) ([]Freshness, error) {
	var resp struct {
		FreshnessInfo struct {
			Freshnesses []struct {
				AggregationPeriod string `json:"aggregationPeriod"`
				LatestEndTime     struct {
					apiDateTime
					TimeZone struct {
						ID string `json:"id"`
					} `json:"timeZone"`
				} `json:"latestEndTime"`
			} `json:"freshnesses"`
		} `json:"freshnessInfo"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, api.DecodeFailure(api.Error{Operation: opDescribe, Package: pkg, Message: "decode vitals freshness: " + err.Error(), Cause: err})
	}
	out := make([]Freshness, 0, len(resp.FreshnessInfo.Freshnesses))
	for _, f := range resp.FreshnessInfo.Freshnesses {
		out = append(out, Freshness{
			Period:        f.AggregationPeriod,
			LatestEndTime: f.LatestEndTime.format(f.AggregationPeriod == "HOURLY"),
			TimeZone:      f.LatestEndTime.TimeZone.ID,
			LatestEnd:     f.LatestEndTime.civil(),
		})
	}
	return out, nil
}

var catalogEntry = regexp.MustCompile("\\*\\s+`([A-Za-z][A-Za-z0-9]*)`")

func parseCatalog(desc, marker string) []string {
	i := strings.Index(desc, marker)
	if i < 0 {
		return nil
	}
	var out []string
	for _, m := range catalogEntry.FindAllStringSubmatch(desc[i+len(marker):], -1) {
		out = append(out, m[1])
	}
	return out
}

func requestSchema(idx schemaindex.Index, set MetricSet) (schemaindex.Schema, bool) {
	m, ok := idx.Methods[set.QueryMethodID()]
	if !ok || m.Request == "" {
		return schemaindex.Schema{}, false
	}
	s, ok := idx.Schemas[m.Request]
	return s, ok
}

func SupportedMetrics(idx schemaindex.Index, set MetricSet) []string {
	s, ok := requestSchema(idx, set)
	if !ok {
		return nil
	}
	return parseCatalog(s.Properties["metrics"].Description, "Supported metrics:")
}

func SupportedDimensions(idx schemaindex.Index, set MetricSet) []string {
	s, ok := requestSchema(idx, set)
	if !ok {
		return nil
	}
	return parseCatalog(s.Properties["dimensions"].Description, "Supported dimensions:")
}

func cohortProperty(idx schemaindex.Index, set MetricSet) (schemaindex.Property, bool) {
	s, ok := requestSchema(idx, set)
	if !ok {
		return schemaindex.Property{}, false
	}
	p, ok := s.Properties["userCohort"]
	return p, ok
}

func SupportedCohorts(idx schemaindex.Index, set MetricSet) []string {
	p, ok := cohortProperty(idx, set)
	if !ok {
		return nil
	}
	var out []string
	for _, v := range p.Enum {
		if !strings.HasSuffix(v, "_UNSPECIFIED") && strings.Contains(p.Description, "`"+v+"`") {
			out = append(out, v)
		}
	}
	return out
}

func CohortDimensions(idx schemaindex.Index, set MetricSet, cohort string) []string {
	p, ok := cohortProperty(idx, set)
	if !ok {
		return nil
	}
	i := strings.Index(p.Description, "`"+cohort+"`")
	if i < 0 {
		return nil
	}
	paragraph := p.Description[i+len(cohort)+2:]
	for _, other := range p.Enum {
		if j := strings.Index(paragraph, "`"+other+"`"); j >= 0 {
			paragraph = paragraph[:j]
		}
	}
	return parseCatalog(paragraph, "Only the following dimensions are supported:")
}

var periodEntry = regexp.MustCompile(`\*\s+([A-Z][A-Z0-9_]*):`)

func SupportedPeriods(idx schemaindex.Index, set MetricSet) []string {
	reqSchema, ok := requestSchema(idx, set)
	if !ok {
		return nil
	}
	desc := reqSchema.Properties["timelineSpec"].Description
	if i := strings.Index(desc, "Supported aggregation periods:"); i >= 0 {
		var out []string
		for _, m := range periodEntry.FindAllStringSubmatch(desc[i:], -1) {
			out = append(out, m[1])
		}
		if len(out) > 0 {
			return out
		}
	}
	tlName := reqSchema.Properties["timelineSpec"].Ref
	tl, ok := idx.Schemas[tlName]
	if !ok {
		return nil
	}
	var out []string
	for _, v := range tl.Properties["aggregationPeriod"].Enum {
		if strings.HasSuffix(v, "_UNSPECIFIED") {
			continue
		}
		out = append(out, v)
	}
	return out
}

type Row struct {
	Date       string
	Dimensions map[string]string
	Metrics    map[string]string
}

type Timeline struct {
	Rows []Row
}

func (t Timeline) Empty() bool { return len(t.Rows) == 0 }

type apiDateTime struct {
	Year    int `json:"year"`
	Month   int `json:"month"`
	Day     int `json:"day"`
	Hours   int `json:"hours"`
	Minutes int `json:"minutes"`
}

func (d apiDateTime) format(hourly bool) string {
	if d.Year == 0 {
		return ""
	}
	s := fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
	if hourly || d.Hours != 0 || d.Minutes != 0 {
		s += fmt.Sprintf(" %02d:%02d", d.Hours, d.Minutes)
	}
	return s
}

func (d apiDateTime) civil() time.Time {
	if d.Year == 0 {
		return time.Time{}
	}
	return time.Date(d.Year, time.Month(d.Month), d.Day, d.Hours, d.Minutes, 0, 0, time.UTC)
}

type apiDimensionValue struct {
	Dimension   string `json:"dimension"`
	StringValue string `json:"stringValue"`
	Int64Value  string `json:"int64Value"`
	ValueLabel  string `json:"valueLabel"`
}

func (v apiDimensionValue) value() string {
	switch {
	case v.StringValue != "":
		return v.StringValue
	case v.Int64Value != "":
		return v.Int64Value
	default:
		return v.ValueLabel
	}
}

type apiMetricValue struct {
	Metric       string `json:"metric"`
	DecimalValue struct {
		Value string `json:"value"`
	} `json:"decimalValue"`
}

func ParseTimeline(pkg string, body []byte) (Timeline, error) {
	var resp struct {
		Rows []struct {
			StartTime         apiDateTime         `json:"startTime"`
			AggregationPeriod string              `json:"aggregationPeriod"`
			Dimensions        []apiDimensionValue `json:"dimensions"`
			Metrics           []apiMetricValue    `json:"metrics"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Timeline{}, api.DecodeFailure(api.Error{Operation: opQuery, Package: pkg, Message: "decode vitals timeline: " + err.Error(), Cause: err})
	}
	tl := Timeline{Rows: make([]Row, 0, len(resp.Rows))}
	for _, r := range resp.Rows {
		row := Row{
			Date:       r.StartTime.format(r.AggregationPeriod == "HOURLY"),
			Dimensions: make(map[string]string, len(r.Dimensions)),
			Metrics:    make(map[string]string, len(r.Metrics)),
		}
		for _, d := range r.Dimensions {
			row.Dimensions[d.Dimension] = d.value()
		}
		for _, m := range r.Metrics {
			row.Metrics[m.Metric] = m.DecimalValue.Value
		}
		tl.Rows = append(tl.Rows, row)
	}
	return tl, nil
}
