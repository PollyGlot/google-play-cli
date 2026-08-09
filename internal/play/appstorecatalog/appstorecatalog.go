// Package appstorecatalog reads the Google Play Catalog Export for app stores
// (`appstorecatalog`), the read-only surface an alternative app store polls to
// mirror Play's public catalog: the catalog app view of one Play app
// (`appstorecatalog.recentappviews.get`) and the update events of a time range
// (`appstorecatalog.recentupdateevents.list`).
//
// Addressing rides the app store package name — the package of the app store on
// whose behalf the request is made — not the calling credential's own app, and
// not an Edit (these endpoints live outside the Edit model entirely). Raw HTTP
// (ADR-0007), never the google-go-sdk; --output json passes the response body
// through verbatim (ADR-0003), so the typed views below stay deliberately
// partial and feed only the human renderers.
package appstorecatalog

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// op* are the native RPC ids, used as the Operation on any *api.Error so the
// shared classifier maps 403 → exit 11, 404 → exit 30, etc.
const (
	opRecentAppViewGet       = "appstorecatalog.recentappviews.get"
	opRecentUpdateEventsList = "appstorecatalog.recentupdateevents.list"
)

// Update type values of a RecentUpdateEvent, per the Discovery snapshot.
const (
	// UpdateTypeModification means the app was modified.
	UpdateTypeModification = "MODIFICATION"
	// UpdateTypeDeletion means the app stopped being eligible for catalog
	// inclusion or was removed from the Play Store.
	UpdateTypeDeletion = "DELETION"
)

// DefaultPageSize is the number of update events the server returns when
// pageSize is unset, and MaxPageSize the cap it coerces larger values down to
// (both per the recentupdateevents.list Discovery description). Named here so
// the command's --help can state them without hard-coding a second copy.
const (
	DefaultPageSize = 100
	MaxPageSize     = 1000
)

// Money mirrors the Money schema: an amount split into whole units (a decimal
// int64 string) plus nano (10^-9) units, tagged with an ISO-4217 currency.
type Money struct {
	CurrencyCode string `json:"currencyCode,omitempty"`
	Units        string `json:"units,omitempty"`
	Nanos        int32  `json:"nanos,omitempty"`
}

// Date mirrors google.type.Date — a whole or partial calendar date. Any
// component may be 0 to mean "unspecified".
type Date struct {
	Year  int `json:"year,omitempty"`
	Month int `json:"month,omitempty"`
	Day   int `json:"day,omitempty"`
}

// DeveloperDetails mirrors the DeveloperDetails schema, deliberately partial:
// only the developer name feeds the human renderers; the contact block reaches
// the caller through the ADR-0003 JSON pass-through.
type DeveloperDetails struct {
	DeveloperName string `json:"developerName,omitempty"`
}

// LocalizedStoreListing mirrors the text fields of one locale's store listing,
// deliberately partial: the full description and the image/video assets are
// intentionally not modelled — they reach the caller through the ADR-0003 JSON
// pass-through.
type LocalizedStoreListing struct {
	LanguageCode     string `json:"languageCode,omitempty"`
	AppName          string `json:"appName,omitempty"`
	ShortDescription string `json:"shortDescription,omitempty"`
}

// LocalizedStoreListings mirrors the LocalizedStoreListings envelope: the
// default language plus one entry per localized store listing.
type LocalizedStoreListings struct {
	DefaultLanguageCode string                  `json:"defaultLanguageCode,omitempty"`
	LocalizedListings   []LocalizedStoreListing `json:"localizedStoreListings,omitempty"`
}

// CatalogPermission mirrors the CatalogPermission schema: one permission the
// app declares, with the optional maxSdkVersion it is scoped to.
type CatalogPermission struct {
	Name          string `json:"name,omitempty"`
	MaxSdkVersion int    `json:"maxSdkVersion,omitempty"`
}

// SdkVersion mirrors CatalogSdkVersion — the SDK range a compatibility
// requirement set covers. int64-formatted fields arrive as JSON strings.
type SdkVersion struct {
	MinSdkVersion    string `json:"minSdkVersion,omitempty"`
	MaxSdkVersion    string `json:"maxSdkVersion,omitempty"`
	TargetSdkVersion string `json:"targetSdkVersion,omitempty"`
}

// DeviceCompatibilityRequirements mirrors the subset of the schema the human
// view summarizes: the SDK range, required ABIs and system features. A device
// is compatible with the app if it satisfies ALL requirements of at least ONE
// of these sets.
type DeviceCompatibilityRequirements struct {
	SdkVersion             *SdkVersion `json:"sdkVersion,omitempty"`
	NativePlatforms        []string    `json:"nativePlatforms,omitempty"`
	RequiredSystemFeatures []string    `json:"requiredSystemFeatures,omitempty"`
}

// CatalogAppView mirrors the subset of the CatalogAppView schema the human view
// reads. The complete resource — device exclusions, screen support, image
// assets, … — is always available verbatim via --output json (ADR-0003), so
// this stays intentionally partial.
type CatalogAppView struct {
	PackageName                     string                            `json:"packageName,omitempty"`
	AppCategory                     string                            `json:"appCategory,omitempty"`
	AppSubcategory                  string                            `json:"appSubcategory,omitempty"`
	ActiveVersionNames              []string                          `json:"activeVersionNames,omitempty"`
	LastPublishTime                 string                            `json:"lastPublishTime,omitempty"`
	FirstReleaseDate                *Date                             `json:"firstReleaseDate,omitempty"`
	DeliveryToken                   string                            `json:"deliveryToken,omitempty"`
	PriceInTheUnitedStates          *Money                            `json:"priceInTheUnitedStates,omitempty"`
	SalePriceInTheUnitedStates      *Money                            `json:"salePriceInTheUnitedStates,omitempty"`
	IARCCertificateID               string                            `json:"iarcCertificateId,omitempty"`
	IsAdultOnlyAudience             bool                              `json:"isAdultOnlyAudience,omitempty"`
	HasInAppAds                     bool                              `json:"hasInAppAds,omitempty"`
	HasInAppPurchases               bool                              `json:"hasInAppPurchases,omitempty"`
	PrivacyPolicyURL                string                            `json:"privacyPolicyUrl,omitempty"`
	DeveloperDetails                *DeveloperDetails                 `json:"developerDetails,omitempty"`
	LocalizedStoreListings          *LocalizedStoreListings           `json:"localizedStoreListings,omitempty"`
	Permissions                     []CatalogPermission               `json:"permissions,omitempty"`
	PermissionsSdk23                []CatalogPermission               `json:"permissionsSdk23,omitempty"`
	DeviceCompatibilityRequirements []DeviceCompatibilityRequirements `json:"deviceCompatibilityRequirements,omitempty"`
}

// RecentAppView mirrors the RecentAppView envelope returned by
// recentappviews.get: one catalog app view under `appView`.
type RecentAppView struct {
	AppView *CatalogAppView `json:"appView,omitempty"`
}

// GetRecentAppView reads the catalog app view of one Play app via
// appstorecatalog.recentappviews.get. storePkg is the app store package name
// (the store on whose behalf the request is made), playPkg the Play app being
// looked up; both are path parameters and are escaped. It returns the parsed
// envelope and the verbatim body for the ADR-0003 --output json pass-through.
// No Edit: the GET hangs off /appstorecatalog/, outside the Edit model.
func GetRecentAppView(ctx context.Context, hc *http.Client, storePkg, playPkg string) (RecentAppView, json.RawMessage, error) {
	u := api.AndroidPubBase + "/appstorecatalog/" + url.PathEscape(storePkg) +
		"/recentAppViews/" + url.PathEscape(playPkg)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return RecentAppView{}, nil, &api.Error{Operation: opRecentAppViewGet, Package: playPkg, Message: err.Error(), Cause: err}
	}
	raw, err := do(hc, opRecentAppViewGet, playPkg, req)
	if err != nil {
		return RecentAppView{}, nil, err
	}
	var v RecentAppView
	if err := json.Unmarshal(raw, &v); err != nil {
		return RecentAppView{}, nil, &api.Error{Operation: opRecentAppViewGet, Package: playPkg, Message: "decode response: " + err.Error(), Cause: err}
	}
	return v, raw, nil
}

// RecentUpdateEvent mirrors the RecentUpdateEvent schema: one entry of the
// incremental catalog-sync feed — which Play app changed, when, and whether the
// change was a MODIFICATION or a DELETION.
type RecentUpdateEvent struct {
	PlayAppPackageName string `json:"playAppPackageName,omitempty"`
	EventTime          string `json:"eventTime,omitempty"`
	UpdateType         string `json:"updateType,omitempty"`
}

// ListRecentUpdateEventsResponse mirrors the ListRecentUpdateEvents envelope:
// one page of update events plus the continuation token.
type ListRecentUpdateEventsResponse struct {
	RecentUpdateEvents []RecentUpdateEvent `json:"recentUpdateEvents,omitempty"`
	NextPageToken      string              `json:"nextPageToken,omitempty"`
}

// ListRecentUpdateEvents reads one page of update events for the apps eligible
// for the app store's catalog, in the [startTime, endTime) range. Both times are
// REQUIRED by the API and travel as RFC 3339 query parameters (the caller
// validates them client-side so a malformed range never costs a round trip).
// Pagination is caller-driven — one page per call, the accessible-apps /
// device-tiers convention: pass pageToken from a previous response's
// NextPageToken to fetch the next page, keeping every other parameter identical.
// pageSize <= 0 lets the server apply its default (DefaultPageSize; larger
// values are coerced down to MaxPageSize). Returns the parsed page and the
// verbatim body for the ADR-0003 --output json pass-through.
func ListRecentUpdateEvents(ctx context.Context, hc *http.Client, storePkg, startTime, endTime string, pageSize int, pageToken string) (ListRecentUpdateEventsResponse, json.RawMessage, error) {
	q := url.Values{}
	q.Set("startTime", startTime)
	q.Set("endTime", endTime)
	if pageSize > 0 {
		q.Set("pageSize", strconv.Itoa(pageSize))
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	u := api.AndroidPubBase + "/appstorecatalog/" + url.PathEscape(storePkg) +
		"/recentUpdateEvents?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ListRecentUpdateEventsResponse{}, nil, &api.Error{Operation: opRecentUpdateEventsList, Message: err.Error(), Cause: err}
	}
	raw, err := do(hc, opRecentUpdateEventsList, "", req)
	if err != nil {
		return ListRecentUpdateEventsResponse{}, nil, err
	}
	var resp ListRecentUpdateEventsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return ListRecentUpdateEventsResponse{}, nil, &api.Error{Operation: opRecentUpdateEventsList, Message: "decode response: " + err.Error(), Cause: err}
	}
	return resp, raw, nil
}

// do runs req and maps the response to (raw body, *api.Error): a non-2xx body
// is parsed for the error envelope, a 2xx body is returned verbatim for the
// ADR-0003 pass-through.
func do(hc *http.Client, op, pkg string, req *http.Request) (json.RawMessage, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(b, resp.StatusCode)
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "read response body: " + readErr.Error(), Cause: readErr}
	}
	return json.RawMessage(raw), nil
}
