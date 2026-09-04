// Package testers reads and replaces the authorized audience (Google
// Groups) of a track via edits.testers.get / edits.testers.update, inside
// an open Edit. The operations exposed: Get (read the audience) and
// Update (replace it wholesale): back the testers-list and testers-set
// commands. The resource has a SINGLE field, googleGroups[]; individual
// tester emails are not supported by the API.
package testers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	u, err := methodGet.URL(map[string]string{
		"packageName": pkg,
		"editId":      editID,
		"track":       track,
	})
	if err != nil {
		return nil, nil, &api.Error{Operation: opTestersGet, Package: pkg, Message: err.Error(), Cause: err}
	}

	req, err := http.NewRequestWithContext(ctx, methodGet.Verb, u, nil)
	if err != nil {
		return nil, nil, &api.Error{Operation: opTestersGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opTestersGet, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{
			Operation:  opTestersGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var parsed Testers
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{
			Operation:  opTestersGet,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
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
	payload, err := json.Marshal(Testers{GoogleGroups: groups})
	if err != nil {
		return nil, nil, &api.Error{Operation: opTestersUpdate, Package: pkg, Message: "marshal payload: " + err.Error(), Cause: err}
	}

	u, err := methodUpdate.URL(map[string]string{
		"packageName": pkg,
		"editId":      editID,
		"track":       track,
	})
	if err != nil {
		return nil, nil, &api.Error{Operation: opTestersUpdate, Package: pkg, Message: err.Error(), Cause: err}
	}

	req, err := http.NewRequestWithContext(ctx, methodUpdate.Verb, u, bytes.NewReader(payload))
	if err != nil {
		return nil, nil, &api.Error{Operation: opTestersUpdate, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, &api.Error{Operation: opTestersUpdate, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, nil, &api.Error{
			Operation:  opTestersUpdate,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	var parsed Testers
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, raw, &api.Error{
			Operation:  opTestersUpdate,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "decode response: " + err.Error(),
			Cause:      err,
		}
	}
	return &parsed, raw, nil
}
