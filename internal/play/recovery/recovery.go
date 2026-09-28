// Package recovery performs the hand-rolled HTTP calls for App Recovery (the
// apprecovery resource; ADR-0007 raw HTTP). A recovery action is a targeted
// incident-response remediation that pushes users impacted by a bad release
// back to a safe app version via remote in-app update. Like internal/play/team
// these calls are app-scoped and OUTSIDE the Edit model: they target
// /applications/{packageName}/appRecoveries, with their own appRecoveryId and a
// draft→active→canceled lifecycle, never an editId.
//
// This file carries the draft + read surface (Create, List); the
// production-impacting lifecycle leaves (Deploy, Cancel, AddTargeting) live in
// lifecycle.go. Every failure surfaces as *api.Error so the exit-code taxonomy
// maps transparently.
package recovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opCreate = "apprecovery.create"
	opList   = "apprecovery.list"
)

// m* are the registry entries this package calls. Resolving them at init turns
// an unregistered or vanished method into a startup panic CI catches (the
// registry tests resolve every entry), never a runtime surprise for a user;
// verb and URL template then come from the Discovery snapshot instead of
// literals maintained here (#513).
var (
	mCreate = apiregistry.MustResolve("androidpublisher.apprecovery.create")
	mList   = apiregistry.MustResolve("androidpublisher.apprecovery.list")
)

// Action is the parsed AppRecoveryAction: the fields the human views need. The
// full body round-trips verbatim via the raw return (ADR-0003).
type Action struct {
	AppRecoveryID  string `json:"appRecoveryId,omitempty"`
	Status         string `json:"status,omitempty"`
	CreateTime     string `json:"createTime,omitempty"`
	LastUpdateTime string `json:"lastUpdateTime,omitempty"`
}

// ListResponse is the parsed ListAppRecoveriesResponse.
type ListResponse struct {
	RecoveryActions []Action `json:"recoveryActions,omitempty"`
}

// --- request body types (mirror the API verbatim) ---

type remoteInAppUpdate struct {
	IsRemoteInAppUpdateRequested bool `json:"isRemoteInAppUpdateRequested"`
}

type allUsers struct {
	IsAllUsersRequested bool `json:"isAllUsersRequested"`
}

type androidSdks struct {
	SdkLevels []int64 `json:"sdkLevels,omitempty"`
}

type regions struct {
	RegionCode []string `json:"regionCode,omitempty"`
}

type versionList struct {
	VersionCodes []int64 `json:"versionCodes,omitempty"`
}

// Targeting is the audience selector shared by create (full) and add-targeting
// (the append-only subset). The user dimension (allUsers / androidSdks /
// regions) and, for create, the version dimension (versionList) combine.
type Targeting struct {
	AllUsers    *allUsers    `json:"allUsers,omitempty"`
	AndroidSdks *androidSdks `json:"androidSdks,omitempty"`
	Regions     *regions     `json:"regions,omitempty"`
	VersionList *versionList `json:"versionList,omitempty"`
}

type createDraftAppRecoveryRequest struct {
	RemoteInAppUpdate *remoteInAppUpdate `json:"remoteInAppUpdate,omitempty"`
	Targeting         *Targeting         `json:"targeting,omitempty"`
}

// CreateOpts is the request-shaped input the command builds from flags.
type CreateOpts struct {
	VersionCodes      []int64 // the bad APK versionCode(s) the recovery targets
	AllUsers          bool
	Regions           []string
	SdkLevels         []int64
	RemoteInAppUpdate bool // default true: the only recovery type Play models today
}

// BuildTargeting assembles the audience selector from the user-dimension flags
// (allUsers / regions / sdkLevels). It is exported so add-targeting (lifecycle.go)
// and create share one builder. Returns nil when no user selector is set.
func BuildTargeting(all bool, regionCodes []string, sdkLevels []int64) *Targeting {
	t := &Targeting{}
	set := false
	if all {
		t.AllUsers = &allUsers{IsAllUsersRequested: true}
		set = true
	}
	if len(regionCodes) > 0 {
		t.Regions = &regions{RegionCode: regionCodes}
		set = true
	}
	if len(sdkLevels) > 0 {
		t.AndroidSdks = &androidSdks{SdkLevels: sdkLevels}
		set = true
	}
	if !set {
		return nil
	}
	return t
}

// Create posts a draft recovery action and returns the parsed Action plus the
// verbatim AppRecoveryAction response.
func Create(ctx context.Context, hc *http.Client, pkg string, opts CreateOpts) (Action, json.RawMessage, error) {
	t := BuildTargeting(opts.AllUsers, opts.Regions, opts.SdkLevels)
	if t == nil {
		t = &Targeting{}
	}
	if len(opts.VersionCodes) > 0 {
		t.VersionList = &versionList{VersionCodes: opts.VersionCodes}
	}
	var a Action
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mCreate, Op: opCreate, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Body: createDraftAppRecoveryRequest{
			RemoteInAppUpdate: &remoteInAppUpdate{IsRemoteInAppUpdateRequested: opts.RemoteInAppUpdate},
			Targeting:         t,
		},
	}, &a)
	if err != nil {
		return Action{}, nil, err
	}
	return a, raw, nil
}

// List reads the recovery actions for a versionCode (required by the API). It
// returns the parsed actions and the verbatim ListAppRecoveriesResponse.
func List(ctx context.Context, hc *http.Client, pkg string, versionCode int64) (ListResponse, json.RawMessage, error) {
	var lr ListResponse
	raw, err := api.DoJSON(ctx, hc, api.Call{
		Method: mList, Op: opList, Target: pkg,
		Params: map[string]string{"packageName": pkg},
		Query:  url.Values{"versionCode": {strconv.FormatInt(versionCode, 10)}},
	}, &lr)
	if err != nil {
		return ListResponse{}, nil, err
	}
	return lr, raw, nil
}
