// Package datasafety performs the write-only Data Safety declaration POST.
// Unlike every other internal/play/* write, applications.dataSafety is
// OUTSIDE the Edits model (ADR-0014): a direct POST on the application, not an
// edits.* resource, so it joins no edit transaction. There is no `get` — the
// live declaration cannot be read back — so this package exposes only Post.
//
// The body is one opaque CSV blob, {"safetyLabels":"<CSV>"} — the same
// import/export CSV the Play Console uses, adopted verbatim (gplay does not
// re-model Google's evolving schema).
package datasafety

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// opDataSafety names the upstream method for *api.Error tagging, matching the
// REST reference (applications.dataSafety) so log readers can correlate it.
const opDataSafety = "applications.dataSafety"

// request is the POST body: a single opaque CSV blob under "safetyLabels".
type request struct {
	SafetyLabels string `json:"safetyLabels"`
}

// Post submits csv as the app's Data Safety declaration. It is WRITE-ONLY and
// OUTSIDE the Edits model: a direct POST to
// /applications/{packageName}/dataSafety with body {"safetyLabels":"<CSV>"}.
// It replaces the whole declaration.
//
// Returns the raw response body for the ADR-0003 --output json pass-through —
// which may legitimately be empty (the API echoes nothing meaningful on
// success; the command layer documents that exception) — or an *api.Error so
// the gplay exit-code taxonomy maps transparently: 403→11, 404→30, 5xx→40,
// network→50.
func Post(ctx context.Context, hc *http.Client, pkg string, csv []byte) (json.RawMessage, error) {
	payload, err := json.Marshal(request{SafetyLabels: string(csv)})
	if err != nil {
		return nil, &api.Error{Operation: opDataSafety, Package: pkg, Message: "marshal payload: " + err.Error(), Cause: err}
	}

	u := api.AndroidPubBase + "/applications/" + url.PathEscape(pkg) + "/dataSafety"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return nil, &api.Error{Operation: opDataSafety, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: opDataSafety, Package: pkg, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		if readErr != nil {
			return nil, &api.Error{
				Operation:  opDataSafety,
				Package:    pkg,
				StatusCode: resp.StatusCode,
				Message:    "read error response body: " + readErr.Error(),
				Cause:      readErr,
			}
		}
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, &api.Error{
			Operation:  opDataSafety,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{
			Operation:  opDataSafety,
			Package:    pkg,
			StatusCode: resp.StatusCode,
			Message:    "read response body: " + readErr.Error(),
			Cause:      readErr,
		}
	}
	return json.RawMessage(raw), nil
}
