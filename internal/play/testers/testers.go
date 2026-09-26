// Package testers reads and replaces the authorized audience (Google
// Groups) of a track via edits.testers.get / edits.testers.update, inside
// an open Edit. The operations exposed: Get (read the audience) and
// Update (replace it wholesale): back the testers-list and testers-set
// commands. The resource has a SINGLE field, googleGroups[]; individual
// tester emails are not supported by the API.
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

// Registry entries this package calls. Resolving them at init makes an
// unregistered or vanished method a startup panic caught by CI rather than a
// runtime surprise; verb and URL then come from the Discovery snapshot instead
// of literals kept here (#513).
var (
	methodGet    = apiregistry.MustResolve("androidpublisher.edits.testers.get")
	methodUpdate = apiregistry.MustResolve("androidpublisher.edits.testers.update")
)

// Testers is the API-shaped edits.testers resource. googleGroups is the
// ONLY field the API exposes ("email lists are not supported by this
// resource"), so gplay manages Google Groups exclusively. json tags mirror
// the API verbatim (ADR-0003 passthrough).
type Testers struct {
	GoogleGroups []string `json:"googleGroups"`
}

// Get fetches the current Testers resource at edits.testers.get: the
// authorized audience (Google Groups) of a single track. The raw JSON body
// is returned alongside for --output json pass-through (ADR-0003) and for
// diagnostics. Like the tracks reads, it runs inside an Edit the caller has
// already opened.
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

// Update PUTs the full replacement audience to edits.testers.update.
// testers.update is declarative: it REPLACES the whole googleGroups list,
// so passing the desired set is the entire operation (there is no
// add/remove). A nil or empty groups slice is normalized to an explicit
// empty array so the body is {"googleGroups":[]}, the --clear semantics,
// rather than {"googleGroups":null}, which the API would reject. Returns
// the parsed Testers and the raw JSON body for --output json pass-through
// (ADR-0003).
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

// trackParams addresses one track's audience inside an Edit.
func trackParams(pkg, editID, track string) map[string]string {
	return map[string]string{"packageName": pkg, "editId": editID, "track": track}
}

// decode unmarshals a 2xx body into out. A body that does not decode keeps
// the 200 status tag it always had, so its exit code (30) is unchanged.
func decode(op, pkg string, raw json.RawMessage, out any) error {
	if err := json.Unmarshal(raw, out); err != nil {
		return &api.Error{Operation: op, Package: pkg, StatusCode: http.StatusOK, Message: "decode response: " + err.Error(), Cause: err}
	}
	return nil
}
