package recovery

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opDeploy       = "apprecovery.deploy"
	opCancel       = "apprecovery.cancel"
	opAddTargeting = "apprecovery.addTargeting"
)

var (
	mDeploy       = apiregistry.MustResolve("androidpublisher.apprecovery.deploy")
	mCancel       = apiregistry.MustResolve("androidpublisher.apprecovery.cancel")
	mAddTargeting = apiregistry.MustResolve("androidpublisher.apprecovery.addTargeting")
)

type addTargetingRequest struct {
	TargetingUpdate *Targeting `json:"targetingUpdate,omitempty"`
}

func actionParams(pkg, id string) map[string]string {
	return map[string]string{"packageName": pkg, "appRecoveryId": id}
}

func Deploy(ctx context.Context, hc *http.Client, pkg, id string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{Method: mDeploy, Op: opDeploy, Target: pkg, Params: actionParams(pkg, id)})
}

func Cancel(ctx context.Context, hc *http.Client, pkg, id string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{Method: mCancel, Op: opCancel, Target: pkg, Params: actionParams(pkg, id)})
}

func AddTargeting(ctx context.Context, hc *http.Client, pkg, id string, t *Targeting) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mAddTargeting, Op: opAddTargeting, Target: pkg,
		Params: actionParams(pkg, id),
		Body:   addTargetingRequest{TargetingUpdate: t},
	})
}
