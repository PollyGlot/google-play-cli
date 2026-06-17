// Package devicetiers performs the hand-rolled HTTP calls for an app's Device
// Tier Configs (the applications.deviceTierConfigs resource; ADR-0007 raw HTTP).
// Like internal/play/datasafety these calls are app-scoped and OUTSIDE the Edit
// model — a direct POST/GET on /applications/{packageName}/deviceTierConfigs,
// not an edit transaction. The resource is immutable: the API exposes only
// create/get/list (no update/patch/delete), and deviceTierConfigId is
// server-assigned, so a create can never overwrite an existing config.
//
// Each call returns a typed struct (for the table/markdown views) and the
// verbatim response body (for the ADR-0003 --output json pass-through). Every
// failure surfaces as *api.Error so the exit-code taxonomy maps transparently.
package devicetiers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opCreate = "deviceTierConfigs.create"
	opGet    = "deviceTierConfigs.get"
	opList   = "deviceTierConfigs.list"
)

// Config is the parsed DeviceTierConfig — just the fields the human views need.
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

func base(pkg string) string {
	return api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) + "/deviceTierConfigs"
}

// Create posts a DeviceTierConfig body and returns the created config (with its
// server-assigned deviceTierConfigId) plus the verbatim response. allowUnknownDevices
// adds the query param only when true (the API default is the strict false).
func Create(ctx context.Context, hc *http.Client, pkg string, body []byte, allowUnknownDevices bool) (Config, json.RawMessage, error) {
	u := base(pkg)
	if allowUnknownDevices {
		u += "?allowUnknownDevices=true"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return Config{}, nil, &api.Error{Operation: opCreate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return doConfig(hc, opCreate, pkg, req)
}

// Get reads a single config by its int64 id.
func Get(ctx context.Context, hc *http.Client, pkg, id string) (Config, json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base(pkg)+"/"+url.PathEscape(id), nil)
	if err != nil {
		return Config{}, nil, &api.Error{Operation: opGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	return doConfig(hc, opGet, pkg, req)
}

// List reads the app's configs (newest first). pageSize<=0 omits the param
// (API default); pageToken paginates. The nextPageToken is preserved in both
// the typed result and the verbatim raw body so a caller can follow pages.
func List(ctx context.Context, hc *http.Client, pkg string, pageSize int, pageToken string) (ListResponse, json.RawMessage, error) {
	u := base(pkg)
	q := url.Values{}
	if pageSize > 0 {
		q.Set("pageSize", strconv.Itoa(pageSize))
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ListResponse{}, nil, &api.Error{Operation: opList, Package: pkg, Message: err.Error(), Cause: err}
	}
	raw, err := do(hc, opList, pkg, req)
	if err != nil {
		return ListResponse{}, nil, err
	}
	var lr ListResponse
	if err := json.Unmarshal(raw, &lr); err != nil {
		return ListResponse{}, nil, &api.Error{Operation: opList, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
	}
	return lr, raw, nil
}

// doConfig executes req, parses the 2xx body as a Config, and returns it with
// the verbatim raw body.
func doConfig(hc *http.Client, op, pkg string, req *http.Request) (Config, json.RawMessage, error) {
	raw, err := do(hc, op, pkg, req)
	if err != nil {
		return Config{}, nil, err
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return Config{}, nil, &api.Error{Operation: op, Package: pkg, Message: "decode response: " + err.Error(), Cause: err}
	}
	return c, raw, nil
}

// do runs req and maps the response to (raw body, *api.Error).
func do(hc *http.Client, op, pkg string, req *http.Request) (json.RawMessage, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: op, Package: pkg, StatusCode: resp.StatusCode, Message: "read response body: " + readErr.Error(), Cause: readErr}
	}
	return json.RawMessage(raw), nil
}
