package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// The production-impacting lifecycle leaves: deploy (activate a draft), cancel
// (terminate (irreversible), add-targeting (widen the audience) append-only).
// Each is a POST to a custom verb on the recovery resource. deploy and cancel
// take an empty request body; add-targeting carries a TargetingUpdate (the
// append-only subset of Targeting). All return the (often empty {}) response
// verbatim: gplay never fabricates a status object (ADR-0003).

const (
	opDeploy       = "apprecovery.deploy"
	opCancel       = "apprecovery.cancel"
	opAddTargeting = "apprecovery.addTargeting"
)

// m* are the registry entries this package calls. Resolving them at init turns
// an unregistered or vanished method into a startup panic CI catches (the
// registry tests resolve every entry), never a runtime surprise for a user;
// verb and URL template then come from the Discovery snapshot instead of
// literals maintained here (#513). The colon verbs (`:deploy`,
// `:cancel`, `:addTargeting`) are part of the snapshot's flatPath, so the
// template carries them and no suffix is concatenated here.
var (
	mDeploy       = apiregistry.MustResolve("androidpublisher.apprecovery.deploy")
	mCancel       = apiregistry.MustResolve("androidpublisher.apprecovery.cancel")
	mAddTargeting = apiregistry.MustResolve("androidpublisher.apprecovery.addTargeting")
)

type addTargetingRequest struct {
	TargetingUpdate *Targeting `json:"targetingUpdate,omitempty"`
}

// actionURL resolves .../appRecoveries/{appRecoveryId}:<verb> for one of the
// three lifecycle methods.
func actionURL(m apiregistry.Method, pkg, id string) (string, error) {
	return m.URL(map[string]string{"packageName": pkg, "appRecoveryId": id})
}

// Deploy activates a draft recovery (apprecovery.deploy). Empty request body.
func Deploy(ctx context.Context, hc *http.Client, pkg, id string) (json.RawMessage, error) {
	return emptyPost(ctx, hc, mDeploy, opDeploy, pkg, id)
}

// Cancel terminates an active recovery (apprecovery.cancel). Empty request body;
// the action persists with status CANCELED and cannot be resumed.
func Cancel(ctx context.Context, hc *http.Client, pkg, id string) (json.RawMessage, error) {
	return emptyPost(ctx, hc, mCancel, opCancel, pkg, id)
}

// AddTargeting widens a recovery's audience (apprecovery.addTargeting). The
// TargetingUpdate is append-only: it can only add users/regions/sdk-levels.
func AddTargeting(ctx context.Context, hc *http.Client, pkg, id string, t *Targeting) (json.RawMessage, error) {
	body, err := json.Marshal(addTargetingRequest{TargetingUpdate: t})
	if err != nil {
		return nil, &api.Error{Operation: opAddTargeting, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err}
	}
	u, err := actionURL(mAddTargeting, pkg, id)
	if err != nil {
		return nil, &api.Error{Operation: opAddTargeting, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, mAddTargeting.Verb, u, bytes.NewReader(body))
	if err != nil {
		return nil, &api.Error{Operation: opAddTargeting, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, opAddTargeting, pkg, req)
}

// emptyPost issues a POST with no request body (Content-Length 0): the shape
// the deploy/cancel custom verbs expect.
func emptyPost(ctx context.Context, hc *http.Client, m apiregistry.Method, op, pkg, id string) (json.RawMessage, error) {
	u, err := actionURL(m, pkg, id)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, m.Verb, u, http.NoBody)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: pkg, Message: err.Error(), Cause: err}
	}
	return do(hc, op, pkg, req)
}
