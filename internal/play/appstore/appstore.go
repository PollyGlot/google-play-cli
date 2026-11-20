package appstore

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const opCreateHostedApp = "appstoreappsreview.createAppStoreHostedApp"

var (
	mCreateHostedApp    = apiregistry.MustResolve("androidpublisher.appstoreappsreview.createappstorehostedapp")
	mUpdateHostedApp    = apiregistry.MustResolve("androidpublisher.appstoreappsreview.updateappstorehostedapp")
	mUpdatePublishState = apiregistry.MustResolve("androidpublisher.appstoreappsreview.updateappstorehostedapppublishstatus")
	mUploadAPK          = apiregistry.MustResolve("androidpublisher.appstoreappsreview.uploadapk")
	mUploadImage        = apiregistry.MustResolve("androidpublisher.appstoreappsreview.uploadimage")
	mUploadPolicy       = apiregistry.MustResolve("androidpublisher.appstoreappsreview.uploadappstoreapppolicydeclarationfile")
)

type CreateHostedAppRequest struct {
	PackageName string `json:"packageName,omitempty"`
}

func CreateHostedApp(ctx context.Context, hc *http.Client, storePackage, pkg string) (json.RawMessage, error) {
	return api.Do(ctx, hc, api.Call{
		Method: mCreateHostedApp, Op: opCreateHostedApp, Target: pkg,
		Params:      map[string]string{"appStorePackageName": storePackage},
		Body:        CreateHostedAppRequest{PackageName: pkg},
		ContentType: jsonUTF8,
	})
}

const jsonUTF8 = "application/json; charset=UTF-8"
