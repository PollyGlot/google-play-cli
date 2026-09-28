package recovery

import (
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

// actionParams addresses .../appRecoveries/{appRecoveryId}:<verb> for one of
// the three lifecycle methods.
func actionParams(pkg, id string) map[string]string {
	return map[string]string{"packageName": pkg, "appRecoveryId": id}
}

// Deploy activates a draft recovery (apprecovery.deploy). Empty request body.
func Deploy(ctx context.Context, hc *http.Client, pkg, id string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{Method: mDeploy, Op: opDeploy, Target: pkg, Params: actionParams(pkg, id)})
}

// Cancel terminates an active recovery (apprecovery.cancel). Empty request body;
// the action persists with status CANCELED and cannot be resumed.
func Cancel(ctx context.Context, hc *http.Client, pkg, id string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{Method: mCancel, Op: opCancel, Target: pkg, Params: actionParams(pkg, id)})
}

// AddTargeting widens a recovery's audience (apprecovery.addTargeting). The
// TargetingUpdate is append-only: it can only add users/regions/sdk-levels.
func AddTargeting(ctx context.Context, hc *http.Client, pkg, id string, t *Targeting) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mAddTargeting, Op: opAddTargeting, Target: pkg,
		Params: actionParams(pkg, id),
		Body:   addTargetingRequest{TargetingUpdate: t},
	})
}
