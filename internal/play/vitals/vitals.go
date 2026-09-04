// Package vitals talks to the Play Developer Reporting API's read-only metric
// sets (crashes/ANR and the other post-launch quality signals, #49). It owns
// the metric-set registry (gplay name → REST resource), the `:query` POST, the
// timeline projection of a query response, and the offline validation of
// user-supplied metrics/dimensions/periods against the embedded Schema index.
//
// Unlike internal/play/reviews this is a DIFFERENT Google service: a distinct
// host (api.ReportingBase) and OAuth scope (token.ReportingScope). Everything
// here is read-only: the API only reads metrics.
//
// Metrics, dimensions and aggregation periods are NEVER invented: the catalog
// is read from the snapshot (ADR-0026). Google documents the supported metrics
// and dimensions of each metric set in the `metrics`/`dimensions` property prose
// of its `:query` request schema, and the aggregation periods as the
// `aggregationPeriod` enum of the TimelineSpec schema: both carried verbatim in
// the embedded Schema index, so validation needs no network and no hand-kept
// list.
package vitals

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/schemaindex"
)

// MetricSet is one queryable vitals metric set: its gplay-facing Name (the
// `vitals query <name>` argument, identical to the Discovery resource segment,
// e.g. "crashrate") and the REST Resource segment its `:query` path hangs off
// (e.g. "crashRateMetricSet"). The pair is validated against the embedded index
// by an offline integrity test, so it cannot drift from the snapshot.
type MetricSet struct {
	Name     string
	Resource string
}

// QueryMethodID is the native RPC id of the set's `:query` method, the key it
// is found under in the Schema index AND in internal/apiregistry: since #513
// this id is also what queryPage resolves the request URL and verb from, so
// the metric-set registry and the API registry meet on a single string rather
// than on two parallel spellings of the same path.
func (s MetricSet) QueryMethodID() string {
	return "playdeveloperreporting.vitals." + s.Name + ".query"
}

// metricSets is the declared set of queryable metric sets: the read-only
// signals the Play Developer Reporting API exposes (#49, memory sets #440). It
// is explicit (like
// discovery.Services) and gated against the embedded index by an integrity test
// so a typo or a snapshot drift fails offline.
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

// MetricSets returns the declared metric sets in registry order.
func MetricSets() []MetricSet {
	out := make([]MetricSet, len(metricSets))
	copy(out, metricSets)
	return out
}

// MetricSetByName resolves a gplay-facing metric-set name to its MetricSet,
// reporting ok=false for an unknown name.
func MetricSetByName(name string) (MetricSet, bool) {
	for _, s := range metricSets {
		if s.Name == name {
			return s, true
		}
	}
	return MetricSet{}, false
}

// MetricSetNames returns the registry names, for an actionable "unknown metric
// set" error.
func MetricSetNames() []string {
	names := make([]string, len(metricSets))
	for i, s := range metricSets {
		names[i] = s.Name
	}
	return names
}

const opQuery = "playdeveloperreporting.vitals.query"

// Query issues the metric set's `:query` POST for pkg, following the response's
// nextPageToken to completion, and returns a rebuilt {rows:[...]} envelope
// (rows verbatim: an ADR-0003 pass-through across the merged pages). The
// metric-set `:query` caps at 1000 rows per page, so a high-cardinality slice
// (e.g. by country over a wide window) would otherwise be silently truncated. A
// non-2xx becomes an *api.Error carrying the status, so the shared classifier
// maps 403 → exit 11, 404 → exit 30, etc.
func Query(ctx context.Context, hc *http.Client, set MetricSet, pkg string, body []byte) (json.RawMessage, error) {
	// Decode the caller's request into a field map so the continuation
	// pageToken can be injected without reshaping the other fields (metrics,
	// timelineSpec, …): they pass through verbatim.
	req := map[string]json.RawMessage{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, &api.Error{Operation: opQuery, Package: pkg, Message: "encode request: " + err.Error(), Cause: err}
		}
	}

	rows := make([]json.RawMessage, 0)
	seen := map[string]struct{}{}
	token := ""
	for {
		if token != "" {
			tok, _ := json.Marshal(token)
			req["pageToken"] = tok
		} else {
			delete(req, "pageToken")
		}
		pageBody, err := json.Marshal(req)
		if err != nil {
			return nil, &api.Error{Operation: opQuery, Package: pkg, Message: "encode request: " + err.Error(), Cause: err}
		}
		raw, err := queryPage(ctx, hc, set, pkg, pageBody)
		if err != nil {
			return nil, err
		}
		pageRows, next, derr := decodePage(raw, "rows")
		if derr != nil {
			return nil, &api.Error{Operation: opQuery, Package: pkg, Message: "decode response: " + derr.Error(), Cause: derr}
		}
		rows = append(rows, pageRows...)
		if next == "" {
			break
		}
		if _, dup := seen[next]; dup {
			return nil, tokenLoopError(opQuery, pkg, "vitals :query")
		}
		seen[next] = struct{}{}
		token = next
	}
	return rebuildEnvelope("rows", rows)
}

// queryPage issues a single `:query` POST and returns the verbatim 2xx page
// body or an *api.Error.
func queryPage(ctx context.Context, hc *http.Client, set MetricSet, pkg string, body []byte) (json.RawMessage, error) {
	// Resolved per call rather than at init: the set is a value the caller
	// supplies (ErrorCountsMetricSet lives outside metricSets), so the id is
	// only known here. Resolve memoises the index and the registry index, so
	// this is two map lookups.
	m, err := apiregistry.Resolve(set.QueryMethodID())
	if err != nil {
		return nil, &api.Error{Operation: opQuery, Package: pkg, Message: err.Error(), Cause: err}
	}
	u, err := m.URL(map[string]string{"appsId": pkg})
	if err != nil {
		return nil, &api.Error{Operation: opQuery, Package: pkg, Message: err.Error(), Cause: err}
	}

	req, err := http.NewRequestWithContext(ctx, m.Verb, u, bytes.NewReader(body))
	if err != nil {
		return nil, &api.Error{Operation: opQuery, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: opQuery, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(errBody, resp.StatusCode)
		return nil, &api.Error{
			Operation:  opQuery,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{
			Operation:  opQuery,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "read response: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	return raw, nil
}

// --- validation against the embedded Schema index --------------------------

// catalogEntry matches one `* `name“ bullet leader in a "Supported metrics" /
// "Supported dimensions" prose list. Only the bullet-leading backtick token is
// captured, so inline mentions of a name inside a description (e.g. "rolling
// average of `crashRate`") are not mistaken for catalog entries.
var catalogEntry = regexp.MustCompile("\\*\\s+`([A-Za-z][A-Za-z0-9]*)`")

// parseCatalog extracts the backtick-quoted identifiers listed after marker in
// a Discovery property description (e.g. "**Supported metrics:**"). Returns nil
// when the marker is absent.
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

// requestSchema returns the index Schema for a metric set's `:query` request
// body, resolved through the method's Request ref. The bool is false when the
// index lacks the method or schema (a corrupt index: guarded by the integrity
// test).
func requestSchema(idx schemaindex.Index, set MetricSet) (schemaindex.Schema, bool) {
	m, ok := idx.Methods[set.QueryMethodID()]
	if !ok || m.Request == "" {
		return schemaindex.Schema{}, false
	}
	s, ok := idx.Schemas[m.Request]
	return s, ok
}

// SupportedMetrics returns the metric names a metric set accepts, read verbatim
// from the `metrics` property prose of its query request schema in idx.
func SupportedMetrics(idx schemaindex.Index, set MetricSet) []string {
	s, ok := requestSchema(idx, set)
	if !ok {
		return nil
	}
	return parseCatalog(s.Properties["metrics"].Description, "Supported metrics:")
}

// SupportedDimensions returns the dimension names a metric set accepts, read
// verbatim from the `dimensions` property prose of its query request schema.
func SupportedDimensions(idx schemaindex.Index, set MetricSet) []string {
	s, ok := requestSchema(idx, set)
	if !ok {
		return nil
	}
	return parseCatalog(s.Properties["dimensions"].Description, "Supported dimensions:")
}

// periodEntry matches one `* DAILY:` bullet leader of the "Supported
// aggregation periods" prose. Unlike metrics and dimensions, Google writes the
// period names bare (no backticks), in SCREAMING_SNAKE_CASE.
var periodEntry = regexp.MustCompile(`\*\s+([A-Z][A-Z0-9_]*):`)

// SupportedPeriods returns the aggregation periods a metric set accepts (a
// subset of DAILY, HOURLY, FULL_RANGE), read verbatim from the "Supported
// aggregation periods" prose of its request schema's `timelineSpec` property.
// The set matters: the memory sets are DAILY-only where crashrate also accepts
// HOURLY, so an unsupported --period is rejected offline instead of coming back
// as a server 400 (#440). When the prose is missing, the TimelineSpec enum is
// the fallback, so the validation never invents a period either way.
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

// --- timeline projection of a query response -------------------------------

// Row is one datapoint of a metric-set timeline: the period's start Date, the
// dimension values that slice it (keyed by dimension name), and the metric
// values (keyed by metric name). Maps keep the projection independent of column
// order, which the command layer fixes from --metrics/--by.
type Row struct {
	Date       string
	Dimensions map[string]string
	Metrics    map[string]string
}

// Timeline is the parsed, render-ready view of a `:query` response.
type Timeline struct {
	Rows []Row
}

// Empty reports whether the query returned no datapoints: distinct from "zero
// crashes", which is why the command layer warns about freshness on an empty
// window.
func (t Timeline) Empty() bool { return len(t.Rows) == 0 }

// apiDateTime mirrors the google.type.DateTime fields the timeline reads.
type apiDateTime struct {
	Year    int `json:"year"`
	Month   int `json:"month"`
	Day     int `json:"day"`
	Hours   int `json:"hours"`
	Minutes int `json:"minutes"`
}

// format renders the datapoint date, appending the HH:MM time when hourly (so
// an HOURLY timeline's midnight bucket reads "… 00:00" consistently with every
// other hour) or when a non-zero time is present. A zero Year is an unset
// datapoint (e.g. an open-ended anomaly window) and renders empty.
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

// ParseTimeline projects a `:query` response body into a Timeline. The verbatim
// body is still what `--output json` emits (ADR-0003); this projection feeds
// only the table/markdown renderers.
func ParseTimeline(body []byte) (Timeline, error) {
	var resp struct {
		Rows []struct {
			StartTime         apiDateTime         `json:"startTime"`
			AggregationPeriod string              `json:"aggregationPeriod"`
			Dimensions        []apiDimensionValue `json:"dimensions"`
			Metrics           []apiMetricValue    `json:"metrics"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return Timeline{}, fmt.Errorf("decode vitals timeline: %w", err)
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
