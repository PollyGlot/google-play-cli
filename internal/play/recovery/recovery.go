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

var (
	mCreate = apiregistry.MustResolve("androidpublisher.apprecovery.create")
	mList   = apiregistry.MustResolve("androidpublisher.apprecovery.list")
)

type Action struct {
	AppRecoveryID  string `json:"appRecoveryId,omitempty"`
	Status         string `json:"status,omitempty"`
	CreateTime     string `json:"createTime,omitempty"`
	LastUpdateTime string `json:"lastUpdateTime,omitempty"`
}

type ListResponse struct {
	RecoveryActions []Action `json:"recoveryActions,omitempty"`
}

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

type VersionRange struct {
	Start int64 `json:"versionCodeStart"`
	End   int64 `json:"versionCodeEnd"`
}

type Targeting struct {
	AllUsers     *allUsers     `json:"allUsers,omitempty"`
	AndroidSdks  *androidSdks  `json:"androidSdks,omitempty"`
	Regions      *regions      `json:"regions,omitempty"`
	VersionList  *versionList  `json:"versionList,omitempty"`
	VersionRange *VersionRange `json:"versionRange,omitempty"`
}

type createDraftAppRecoveryRequest struct {
	RemoteInAppUpdate *remoteInAppUpdate `json:"remoteInAppUpdate,omitempty"`
	Targeting         *Targeting         `json:"targeting,omitempty"`
}

type CreateOpts struct {
	VersionCodes      []int64
	VersionRange      *VersionRange
	AllUsers          bool
	Regions           []string
	SdkLevels         []int64
	RemoteInAppUpdate bool
}

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

func Create(ctx context.Context, hc *http.Client, pkg string, opts CreateOpts) (Action, json.RawMessage, error) {
	t := BuildTargeting(opts.AllUsers, opts.Regions, opts.SdkLevels)
	if t == nil {
		t = &Targeting{}
	}
	if len(opts.VersionCodes) > 0 {
		t.VersionList = &versionList{VersionCodes: opts.VersionCodes}
	}
	t.VersionRange = opts.VersionRange
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
