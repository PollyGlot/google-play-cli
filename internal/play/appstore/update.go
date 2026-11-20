package appstore

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opUpdateHostedApp    = "appstoreappsreview.updateAppStoreHostedApp"
	opUpdatePublishState = "appstoreappsreview.updateAppStoreHostedAppPublishStatus"
)

const (
	PublishStatePublished   = "APP_STORE_APP_PUBLISH_STATE_PUBLISHED"
	PublishStateUnpublished = "APP_STORE_APP_PUBLISH_STATE_UNPUBLISHED"
)

type AppDetails struct {
	DeveloperName    string `json:"developerName,omitempty"`
	ContactEmail     string `json:"contactEmail,omitempty"`
	DeveloperWebsite string `json:"developerWebsite,omitempty"`
}

type ActiveApkSet struct {
	BaseApkID   string   `json:"baseApkId,omitempty"`
	SplitApkIDs []string `json:"splitApkId,omitempty"`
}

type ActiveApks struct {
	ActiveApkSets []ActiveApkSet `json:"activeApkSets,omitempty"`
}

type StoreListing struct {
	LanguageCode     string   `json:"languageCode,omitempty"`
	AppName          string   `json:"appName,omitempty"`
	ShortDescription string   `json:"shortDescription,omitempty"`
	FullDescription  string   `json:"fullDescription,omitempty"`
	AppIconID        string   `json:"appIconId,omitempty"`
	ScreenshotIDs    []string `json:"screenshotId,omitempty"`
	VideoLink        string   `json:"videoLink,omitempty"`
}

type PolicyDeclaration struct {
	DeclarationID string            `json:"declarationId,omitempty"`
	Responses     []json.RawMessage `json:"responses,omitempty"`
}

type UpdateHostedAppRequest struct {
	PackageName                  string              `json:"packageName,omitempty"`
	AppDetails                   *AppDetails         `json:"appDetails,omitempty"`
	ActiveApks                   *ActiveApks         `json:"activeApks,omitempty"`
	ActiveLocalizedStoreListings []StoreListing      `json:"activeLocalizedStoreListings,omitempty"`
	PolicyDeclarations           []PolicyDeclaration `json:"policyDeclarations,omitempty"`
}

func UpdateHostedApp(ctx context.Context, hc *http.Client, storePackage, pkg string, body json.RawMessage) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, &api.Error{Operation: opUpdateHostedApp, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err}
	}
	target, err := json.Marshal(pkg)
	if err != nil {
		return nil, &api.Error{Operation: opUpdateHostedApp, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err}
	}
	fields["packageName"] = target

	out, err := json.Marshal(fields)
	if err != nil {
		return nil, &api.Error{Operation: opUpdateHostedApp, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err}
	}

	return api.Do(ctx, hc, api.Call{
		Method: mUpdateHostedApp, Op: opUpdateHostedApp, Target: pkg,
		Params:      map[string]string{"appStorePackageName": storePackage},
		Body:        out,
		ContentType: jsonUTF8,
	})
}

func UpdatePublishStatus(ctx context.Context, hc *http.Client, storePackage, pkg, state string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mUpdatePublishState, Op: opUpdatePublishState, Target: pkg,
		Params: map[string]string{"appStorePackageName": storePackage, "packageName": pkg},
		Body: struct {
			PublishState string `json:"publishState"`
		}{PublishState: state},
		ContentType: jsonUTF8,
	})
}
