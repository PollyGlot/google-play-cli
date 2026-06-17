package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// The production-impacting lifecycle leaves: deploy (activate a draft), cancel
// (terminate — irreversible), add-targeting (widen the audience — append-only).
// Each is a POST to a custom verb on the recovery resource. deploy and cancel
// take an empty request body; add-targeting carries a TargetingUpdate (the
// append-only subset of Targeting). All return the (often empty {}) response
// verbatim — gplay never fabricates a status object (ADR-0003).

const (
	opDeploy       = "apprecovery.deploy"
	opCancel       = "apprecovery.cancel"
	opAddTargeting = "apprecovery.addTargeting"
)

type addTargetingRequest struct {
	TargetingUpdate *Targeting `json:"targetingUpdate,omitempty"`
}

// actionURL builds .../appRecoveries/{id}:{verb} — the colon-verb is a literal
// path suffix, not an escaped segment.
func actionURL(pkg, id, verb string) string {
	return base(pkg) + "/" + url.PathEscape(id) + ":" + verb
}

// Deploy activates a draft recovery (apprecovery.deploy). Empty request body.
func Deploy(ctx context.Context, hc *http.Client, pkg, id string) (json.RawMessage, error) {
	return emptyPost(ctx, hc, opDeploy, pkg, actionURL(pkg, id, "deploy"))
}

// Cancel terminates an active recovery (apprecovery.cancel). Empty request body;
// the action persists with status CANCELED and cannot be resumed.
func Cancel(ctx context.Context, hc *http.Client, pkg, id string) (json.RawMessage, error) {
	return emptyPost(ctx, hc, opCancel, pkg, actionURL(pkg, id, "cancel"))
}

// AddTargeting widens a recovery's audience (apprecovery.addTargeting). The
// TargetingUpdate is append-only — it can only add users/regions/sdk-levels.
func AddTargeting(ctx context.Context, hc *http.Client, pkg, id string, t *Targeting) (json.RawMessage, error) {
	body, err := json.Marshal(addTargetingRequest{TargetingUpdate: t})
	if err != nil {
		return nil, &api.Error{Operation: opAddTargeting, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, actionURL(pkg, id, "addTargeting"), bytes.NewReader(body))
	if err != nil {
		return nil, &api.Error{Operation: opAddTargeting, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opAddTargeting, pkg, req)
}

// emptyPost issues a POST with no request body (Content-Length 0) — the shape
// the deploy/cancel custom verbs expect.
func emptyPost(ctx context.Context, hc *http.Client, op, pkg, u string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, http.NoBody)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	return do(hc, op, pkg, req)
}
