// Package datasafety performs the write-only Data Safety declaration POST.
// Unlike every other internal/play/* write, applications.dataSafety is
// OUTSIDE the Edits model (ADR-0014): a direct POST on the application, not an
// edits.* resource, so it joins no edit transaction. There is no `get`: the
// live declaration cannot be read back, so this package exposes only Post.
//
// The body is one opaque CSV blob, {"safetyLabels":"<CSV>"}: the same
// import/export CSV the Play Console uses, adopted verbatim (gplay does not
// re-model Google's evolving schema).
package datasafety

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// opDataSafety names the upstream method for *api.Error tagging, matching the
// REST reference (applications.dataSafety) so log readers can correlate it.
const opDataSafety = "applications.dataSafety"

// method is the registry entry this package calls. Resolving it at init makes
// an unregistered or vanished method a startup panic caught by CI rather than a
// runtime surprise; verb and URL then come from the Discovery snapshot instead
// of a literal kept here (#513).
var method = apiregistry.MustResolve("androidpublisher.applications.dataSafety")

// request is the POST body: a single opaque CSV blob under "safetyLabels".
type request struct {
	SafetyLabels string `json:"safetyLabels"`
}

// Post submits csv as the app's Data Safety declaration. It is WRITE-ONLY and
// OUTSIDE the Edits model: a direct POST to
// /applications/{packageName}/dataSafety with body {"safetyLabels":"<CSV>"}.
// It replaces the whole declaration.
//
// Returns the raw response body for the ADR-0003 --output json pass-through,
// which may legitimately be empty (the API echoes nothing meaningful on
// success; the command layer documents that exception), or an *api.Error so
// the gplay exit-code taxonomy maps transparently: 403→11, 404→30, 5xx→40,
// network→50.
func Post(ctx context.Context, hc *http.Client, pkg string, csv []byte) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: method, Op: opDataSafety, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Body:   request{SafetyLabels: string(csv)},
	})
}
