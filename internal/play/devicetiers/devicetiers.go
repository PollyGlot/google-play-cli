// Package devicetiers performs the hand-rolled HTTP calls for an app's Device
// Tier Configs (the applications.deviceTierConfigs resource; ADR-0007 raw HTTP).
// Like internal/play/datasafety these calls are app-scoped and OUTSIDE the Edit
// model: a direct POST/GET on /applications/{packageName}/deviceTierConfigs,
// not an edit transaction. The resource is immutable: the API exposes only
// create/get/list (no update/patch/delete), and deviceTierConfigId is
// server-assigned, so a create can never overwrite an existing config.
//
// Each call returns a typed struct (for the table/markdown views) and the
// verbatim response body (for the ADR-0003 --output json pass-through). Every
// failure surfaces as *api.Error so the exit-code taxonomy maps transparently.
package devicetiers

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
	opCreate = "deviceTierConfigs.create"
	opGet    = "deviceTierConfigs.get"
	opList   = "deviceTierConfigs.list"
)

// Registry entries this package calls. Resolving them at init makes an
// unregistered or vanished method a startup panic caught by CI rather than a
// runtime surprise; verb and URL then come from the Discovery snapshot instead
// of literals kept here (#513). Query strings (allowUnknownDevices, paging)
// stay below: the resolver only answers verb and path.
var (
	methodCreate = apiregistry.MustResolve("androidpublisher.applications.deviceTierConfigs.create")
	methodGet    = apiregistry.MustResolve("androidpublisher.applications.deviceTierConfigs.get")
	methodList   = apiregistry.MustResolve("androidpublisher.applications.deviceTierConfigs.list")
)

// Config is the parsed DeviceTierConfig: just the fields the human views need.
// The deviceGroups / deviceTiers / userCountrySets are kept as raw messages so
// the table view can count them without re-modelling Google's nested schema
// (the full body round-trips verbatim via the raw return).
type Config struct {
	DeviceTierConfigID string            `json:"deviceTierConfigId,omitempty"`
	DeviceGroups       []json.RawMessage `json:"deviceGroups,omitempty"`
	DeviceTierSet      *struct {
		DeviceTiers []json.RawMessage `json:"deviceTiers,omitempty"`
	} `json:"deviceTierSet,omitempty"`
	UserCountrySets []json.RawMessage `json:"userCountrySets,omitempty"`
}

// Tiers reports the number of device tiers in the config's tier set.
func (c Config) Tiers() int {
	if c.DeviceTierSet == nil {
		return 0
	}
	return len(c.DeviceTierSet.DeviceTiers)
}

// ListResponse is the parsed ListDeviceTierConfigsResponse.
type ListResponse struct {
	DeviceTierConfigs []Config `json:"deviceTierConfigs,omitempty"`
	NextPageToken     string   `json:"nextPageToken,omitempty"`
}

// Create posts a DeviceTierConfig body and returns the created config (with its
// server-assigned deviceTierConfigId) plus the verbatim response. allowUnknownDevices
// adds the query param only when true (the API default is the strict false).
func Create(ctx context.Context, hc *http.Client, pkg string, body []byte, allowUnknownDevices bool) (Config, json.RawMessage, error) {
	var q url.Values
	if allowUnknownDevices {
		q = url.Values{"allowUnknownDevices": {"true"}}
	}
	return doConfig(ctx, hc, api.Call{
		Method: methodCreate, Op: opCreate, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Query:  q,
		Body:   body,
	})
}

// Get reads a single config by its int64 id.
func Get(ctx context.Context, hc *http.Client, pkg, id string) (Config, json.RawMessage, error) {
	return doConfig(ctx, hc, api.Call{
		Method: methodGet, Op: opGet, Target: pkg,
		Params: map[string]string{"packageName": pkg, "deviceTierConfigId": id},
	})
}

// List reads the app's configs (newest first). pageSize<=0 omits the param
// (API default); pageToken paginates. The nextPageToken is preserved in both
// the typed result and the verbatim raw body so a caller can follow pages.
func List(ctx context.Context, hc *http.Client, pkg string, pageSize int, pageToken string) (ListResponse, json.RawMessage, error) {
	q := url.Values{}
	if pageSize > 0 {
		q.Set("pageSize", strconv.Itoa(pageSize))
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	var lr ListResponse
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: methodList, Op: opList, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Query:  q,
	}, &lr)
	if err != nil {
		return ListResponse{}, nil, err
	}
	return lr, raw, nil
}

// doConfig sends c, parses the 2xx body as a Config, and returns it with the
// verbatim raw body.
func doConfig(ctx context.Context, hc *http.Client, c api.Call) (Config, json.RawMessage, error) {
	var cfg Config
	raw, err := api.DoJSON(ctx, hc, c, &cfg)
	if err != nil {
		return Config{}, nil, err
	}
	return cfg, raw, nil
}
