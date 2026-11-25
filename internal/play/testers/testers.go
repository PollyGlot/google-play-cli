package testers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opTestersGet    = "testers.get"
	opTestersUpdate = "testers.update"
)

var (
	methodGet    = apiregistry.MustResolve("androidpublisher.edits.testers.get")
	methodUpdate = apiregistry.MustResolve("androidpublisher.edits.testers.update")
)

type Testers struct {
	GoogleGroups []string `json:"googleGroups"`
}

func Get(ctx context.Context, hc *http.Client, pkg, editID, track string) (*Testers, json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodGet, Op: opTestersGet, Target: pkg,
		Params: trackParams(pkg, editID, track),
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed Testers
	if err := decode(opTestersGet, pkg, raw, &parsed); err != nil {
		return nil, raw, err
	}
	return &parsed, raw, nil
}

func Update(ctx context.Context, hc *http.Client, pkg, editID, track string, groups []string) (*Testers, json.RawMessage, error) {
	if groups == nil {
		groups = []string{}
	}
	raw, err := api.Do(ctx, hc, api.Call{
		Method: methodUpdate, Op: opTestersUpdate, Target: pkg,
		Params: trackParams(pkg, editID, track),
		Body:   Testers{GoogleGroups: groups},
	})
	if err != nil {
		return nil, nil, err
	}
	var parsed Testers
	if err := decode(opTestersUpdate, pkg, raw, &parsed); err != nil {
		return nil, raw, err
	}
	return &parsed, raw, nil
}

func trackParams(pkg, editID, track string) map[string]string {
	return map[string]string{"packageName": pkg, "editId": editID, "track": track}
}

func decode(op, pkg string, raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return api.DecodeFailure(api.Error{Operation: op, Package: pkg, Message: "decode response: " + err.Error(), Cause: err})
	}
	return nil
}
