// Package appstore wraps the Android Publisher `appstoreappsreview` resource —
// the surface a third-party Android app store uses to submit the apps it hosts
// to Google for review (the DMA / alternative-distribution obligation). It is
// keyed by the **app store package name** (the path key
// `appstore/{appStorePackageName}/...`), a third addressing axis, distinct from
// both the package axis (`applications/{packageName}/...`) and the
// developer-account axis (ADR-0015): the caller is the store, the subject is
// someone else's app.
//
// This package ships the first slice of that surface (#378 of PRD #377):
// `createappstorehostedapp`, which creates the **hosted app** record. Google's
// own method description makes it the mandatory precondition — "This must be
// called before any other RPCs for this hosted app" — so every later slice
// (update, APK upload, image upload, policy declaration, publish status) hangs
// off a record this call created.
//
// Raw HTTP (ADR-0007), never the google-go-sdk; every upstream failure surfaces
// as an *api.Error so the gplay exit-code taxonomy maps transparently
// (403→11, 404→30, 5xx→40, transport→50).
package appstore

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// opCreateHostedApp tags *api.Error with the REST reference method id.
const opCreateHostedApp = "appstoreappsreview.createAppStoreHostedApp"

// CreateHostedAppRequest mirrors the CreateAppStoreHostedAppRequest schema: the
// package name of the app the store hosts. The app store itself is the path
// key, never a body field.
type CreateHostedAppRequest struct {
	PackageName string `json:"packageName,omitempty"`
}

// CreateHostedApp creates the hosted app record for pkg under the app store
// identified by storePackage, via
// `POST androidpublisher/v3/appstore/{appStorePackageName}/apps:create`.
//
// It returns the verbatim response body for the ADR-0003 --output json
// pass-through. CreateAppStoreHostedAppResponse carries no fields in the
// Discovery snapshot — the acknowledgement IS the result — so there is nothing
// to parse into a typed struct and the raw bytes are the whole return value.
// They may legitimately be `{}` or empty; the command layer, not this package,
// decides what to render for an empty body.
//
// No Edit: the call is not under `/edits/`. Both the app store package name and
// the app package name are path/body values supplied by the caller, so the
// store package is path-escaped rather than interpolated raw.
func CreateHostedApp(ctx context.Context, hc *http.Client, storePackage, pkg string) (json.RawMessage, error) {
	body, err := json.Marshal(CreateHostedAppRequest{PackageName: pkg})
	if err != nil {
		return nil, &api.Error{Operation: opCreateHostedApp, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err}
	}

	u := api.AndroidPubBase + "/appstore/" + url.PathEscape(storePackage) + "/apps:create"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, &api.Error{Operation: opCreateHostedApp, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	return do(hc, opCreateHostedApp, pkg, req)
}

// do runs req and maps the response to (raw body, *api.Error): a non-2xx body is
// parsed for the error envelope, a 2xx body is returned verbatim for the
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
