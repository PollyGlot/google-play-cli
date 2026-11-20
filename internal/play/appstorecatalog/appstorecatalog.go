package appstorecatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opRecentAppViewGet       = "appstorecatalog.recentappviews.get"
	opRecentUpdateEventsList = "appstorecatalog.recentupdateevents.list"
)

var (
	mRecentAppViewGet       = apiregistry.MustResolve("androidpublisher.appstorecatalog.recentappviews.get")
	mRecentUpdateEventsList = apiregistry.MustResolve("androidpublisher.appstorecatalog.recentupdateevents.list")
)

const (
	UpdateTypeModification = "MODIFICATION"
	UpdateTypeDeletion     = "DELETION"
)

const (
	DefaultPageSize = 100
	MaxPageSize     = 1000
)

type Money struct {
	CurrencyCode string `json:"currencyCode,omitempty"`
	Units        string `json:"units,omitempty"`
	Nanos        int32  `json:"nanos,omitempty"`
}

type Date struct {
	Year  int `json:"year,omitempty"`
	Month int `json:"month,omitempty"`
	Day   int `json:"day,omitempty"`
}

type DeveloperDetails struct {
	DeveloperName string `json:"developerName,omitempty"`
}

type LocalizedStoreListing struct {
	LanguageCode     string `json:"languageCode,omitempty"`
	AppName          string `json:"appName,omitempty"`
	ShortDescription string `json:"shortDescription,omitempty"`
}

type LocalizedStoreListings struct {
	DefaultLanguageCode string                  `json:"defaultLanguageCode,omitempty"`
	LocalizedListings   []LocalizedStoreListing `json:"localizedStoreListings,omitempty"`
}

type CatalogPermission struct {
	Name          string `json:"name,omitempty"`
	MaxSdkVersion int    `json:"maxSdkVersion,omitempty"`
}

type SdkVersion struct {
	MinSdkVersion    string `json:"minSdkVersion,omitempty"`
	MaxSdkVersion    string `json:"maxSdkVersion,omitempty"`
	TargetSdkVersion string `json:"targetSdkVersion,omitempty"`
}

type DeviceCompatibilityRequirements struct {
	SdkVersion             *SdkVersion `json:"sdkVersion,omitempty"`
	NativePlatforms        []string    `json:"nativePlatforms,omitempty"`
	RequiredSystemFeatures []string    `json:"requiredSystemFeatures,omitempty"`
}

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

type RecentAppView struct {
	AppView *CatalogAppView `json:"appView,omitempty"`
}

func GetRecentAppView(ctx context.Context, hc *http.Client, storePkg, playPkg string) (RecentAppView, json.RawMessage, error) {
	var v RecentAppView
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mRecentAppViewGet, Op: opRecentAppViewGet, Target: playPkg,
		Params: map[string]string{"appStorePackageName": storePkg, "playAppPackageName": playPkg},
	}, &v)
	if err != nil {
		return RecentAppView{}, nil, err
	}
	return v, raw, nil
}

type RecentUpdateEvent struct {
	PlayAppPackageName string `json:"playAppPackageName,omitempty"`
	EventTime          string `json:"eventTime,omitempty"`
	UpdateType         string `json:"updateType,omitempty"`
}

type ListRecentUpdateEventsResponse struct {
	RecentUpdateEvents []RecentUpdateEvent `json:"recentUpdateEvents,omitempty"`
	NextPageToken      string              `json:"nextPageToken,omitempty"`
}

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
	var resp ListRecentUpdateEventsResponse
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mRecentUpdateEventsList, Op: opRecentUpdateEventsList,
		Params: map[string]string{"appStorePackageName": storePkg},
		Query:  q,
	}, &resp)
	if err != nil {
		return ListRecentUpdateEventsResponse{}, nil, err
	}
	return resp, raw, nil
}
