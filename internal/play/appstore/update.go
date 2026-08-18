package appstore

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opUpdateHostedApp    = "appstoreappsreview.updateAppStoreHostedApp"
	opUpdatePublishState = "appstoreappsreview.updateAppStoreHostedAppPublishStatus"
)

// Publish states of a hosted app, as the API spells them.
const (
	PublishStatePublished   = "APP_STORE_APP_PUBLISH_STATE_PUBLISHED"
	PublishStateUnpublished = "APP_STORE_APP_PUBLISH_STATE_UNPUBLISHED"
)

// AppDetails mirrors AppStoreAppDetails: who publishes the hosted app and how
// Google reaches them about the review.
type AppDetails struct {
	DeveloperName    string `json:"developerName,omitempty"`
	ContactEmail     string `json:"contactEmail,omitempty"`
	DeveloperWebsite string `json:"developerWebsite,omitempty"`
}

// ActiveApkSet mirrors AppStoreAppActiveApkSet: one base module plus the split
// modules delivered with it. The ids are the ones `appstore upload apk`
// returned.
type ActiveApkSet struct {
	BaseApkID   string   `json:"baseApkId,omitempty"`
	SplitApkIDs []string `json:"splitApkId,omitempty"`
}

// ActiveApks mirrors AppStoreAppActiveApks.
type ActiveApks struct {
	ActiveApkSets []ActiveApkSet `json:"activeApkSets,omitempty"`
}

// StoreListing mirrors AppStoreAppStoreListing: the per-locale store text and
// the image ids `appstore upload image` returned.
type StoreListing struct {
	LanguageCode     string   `json:"languageCode,omitempty"`
	AppName          string   `json:"appName,omitempty"`
	ShortDescription string   `json:"shortDescription,omitempty"`
	FullDescription  string   `json:"fullDescription,omitempty"`
	AppIconID        string   `json:"appIconId,omitempty"`
	ScreenshotIDs    []string `json:"screenshotId,omitempty"`
	VideoLink        string   `json:"videoLink,omitempty"`
}

// PolicyDeclaration mirrors AppStoreAppPolicyDeclaration.
//
// Responses stay json.RawMessage on purpose: PolicyResponse is a seven-way
// oneof (boolean / document / group / keyedGroup / multipleChoice /
// singleChoice / string), and Google adds question types over time. Modelling
// it would freeze today's snapshot into the CLI and force a release every time
// the review questionnaire grows a variant; passing the operator's own JSON
// through keeps every present and future response shape expressible.
type PolicyDeclaration struct {
	DeclarationID string            `json:"declarationId,omitempty"`
	Responses     []json.RawMessage `json:"responses,omitempty"`
}

// UpdateHostedAppRequest mirrors UpdateAppStoreHostedAppRequest — the full
// submission: who the developer is, what binaries are active, what the store
// listing says in each locale, and the answers to Google's policy
// questionnaire.
type UpdateHostedAppRequest struct {
	PackageName                  string              `json:"packageName,omitempty"`
	AppDetails                   *AppDetails         `json:"appDetails,omitempty"`
	ActiveApks                   *ActiveApks         `json:"activeApks,omitempty"`
	ActiveLocalizedStoreListings []StoreListing      `json:"activeLocalizedStoreListings,omitempty"`
	PolicyDeclarations           []PolicyDeclaration `json:"policyDeclarations,omitempty"`
}

// UpdateHostedApp submits the assembled hosted app to Google's review via
// `POST androidpublisher/v3/appstore/{appStorePackageName}/apps:update`.
//
// Per Google this call submits the app to review immediately — there is no
// staging step and no way to recall it, which is why the command layer gates
// it behind --confirm (ADR-0017 destructive tier, refined by ADR-0043).
//
// UpdateAppStoreHostedAppResponse carries no fields: the acknowledgement is the
// result. The raw body is returned regardless for the ADR-0003 pass-through.
//
// body is the operator's own JSON, forwarded **verbatim** apart from
// packageName, which is overwritten with pkg so the resolved target always wins.
// Round-tripping it through UpdateHostedAppRequest instead would silently drop
// anything the struct does not model — a field Google added last week, or a
// typo like `developerNAme` — and this submission cannot be recalled. Sending
// the operator's keys as written means Google validates them: a misspelling
// comes back as a rejection the caller can read, never as a submission quietly
// missing half its content. UpdateHostedAppRequest stays the documented shape
// of that body and is what the command layer parses to summarise it.
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

	u := api.AndroidPubBase + "/appstore/" + url.PathEscape(storePackage) + "/apps:update"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(out))
	if err != nil {
		return nil, &api.Error{Operation: opUpdateHostedApp, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	return do(hc, opUpdateHostedApp, pkg, req)
}

// UpdatePublishStatus flips a hosted app between PUBLISHED and UNPUBLISHED via
// `POST .../apps/{packageName}:updateAppStoreHostedAppPublishStatus`.
//
// state must be one of the PublishState* constants; the command layer maps the
// human-facing `published` / `unpublished` words onto them. Google notes the
// state defaults to PUBLISHED after UpdateAppStoreHostedApp, so this call is
// only needed to pull an app back out of the store (or to put it back).
//
// The response carries no fields — same acknowledgement shape as create.
func UpdatePublishStatus(ctx context.Context, hc *http.Client, storePackage, pkg, state string) (json.RawMessage, error) {
	body, err := json.Marshal(struct {
		PublishState string `json:"publishState"`
	}{PublishState: state})
	if err != nil {
		return nil, &api.Error{Operation: opUpdatePublishState, Package: pkg, Message: "marshal request: " + err.Error(), Cause: err}
	}

	u := api.AndroidPubBase +
		"/appstore/" + url.PathEscape(storePackage) +
		"/apps/" + url.PathEscape(pkg) +
		":updateAppStoreHostedAppPublishStatus"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, &api.Error{Operation: opUpdatePublishState, Package: pkg, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")

	return do(hc, opUpdatePublishState, pkg, req)
}
