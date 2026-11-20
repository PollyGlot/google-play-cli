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

var (
	methodCreate = apiregistry.MustResolve("androidpublisher.applications.deviceTierConfigs.create")
	methodGet    = apiregistry.MustResolve("androidpublisher.applications.deviceTierConfigs.get")
	methodList   = apiregistry.MustResolve("androidpublisher.applications.deviceTierConfigs.list")
)

type Config struct {
	DeviceTierConfigID string            `json:"deviceTierConfigId,omitempty"`
	DeviceGroups       []json.RawMessage `json:"deviceGroups,omitempty"`
	DeviceTierSet      *struct {
		DeviceTiers []json.RawMessage `json:"deviceTiers,omitempty"`
	} `json:"deviceTierSet,omitempty"`
	UserCountrySets []json.RawMessage `json:"userCountrySets,omitempty"`
}

func (c Config) Tiers() int {
	if c.DeviceTierSet == nil {
		return 0
	}
	return len(c.DeviceTierSet.DeviceTiers)
}

type ListResponse struct {
	DeviceTierConfigs []Config `json:"deviceTierConfigs,omitempty"`
	NextPageToken     string   `json:"nextPageToken,omitempty"`
}

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

func Get(ctx context.Context, hc *http.Client, pkg, id string) (Config, json.RawMessage, error) {
	return doConfig(ctx, hc, api.Call{
		Method: methodGet, Op: opGet, Target: pkg,
		Params: map[string]string{"packageName": pkg, "deviceTierConfigId": id},
	})
}

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

func doConfig(ctx context.Context, hc *http.Client, c api.Call) (Config, json.RawMessage, error) {
	var cfg Config
	raw, err := api.DoJSON(ctx, hc, c, &cfg)
	if err != nil {
		return Config{}, nil, err
	}
	return cfg, raw, nil
}
