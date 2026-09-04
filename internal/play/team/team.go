// Package team performs the hand-rolled HTTP calls for the Google Play
// Developer account's people/permissions surface: `users.*` and `grants.*`
// under developers/{developerId} (ADR-0007: raw HTTP, not the generated SDK).
// Unlike most internal/play/* modules these calls are OUTSIDE the Edits model:
// they target the Developer account, not an app's edit transaction.
//
// The whole upstream surface is 7 methods with two hard shapes (PRD #147):
//
//   - there is NO users.get and NO grants.list/grants.get: a User's Grants
//     are a FIELD of the User resource, so the only way to read a User (or any
//     Grant) is users.list. FindUser powers every read-then-decide path.
//   - users.list is paginated; ListUsers follows nextPageToken to completion
//     so a large team is never silently truncated.
//
// Every failure surfaces as an *api.Error so the gplay exit-code taxonomy maps
// transparently (403→11, 404→30, 5xx→40, network→50).
package team

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// Registry entries for the seven methods of this surface. Resolving at init
// turns an unregistered or vanished method into a CI panic rather than a
// runtime surprise; verb and URL template then come from the Discovery
// snapshot instead of literals kept here (#513, batch 3). Discovery names the
// path parameters after the collections, hence developersId / usersId /
// grantsId for what gplay calls the developer id, the email and the package.
var (
	mUsersList    = apiregistry.MustResolve("androidpublisher.users.list")
	mUsersCreate  = apiregistry.MustResolve("androidpublisher.users.create")
	mUsersPatch   = apiregistry.MustResolve("androidpublisher.users.patch")
	mUsersDelete  = apiregistry.MustResolve("androidpublisher.users.delete")
	mGrantsCreate = apiregistry.MustResolve("androidpublisher.grants.create")
	mGrantsPatch  = apiregistry.MustResolve("androidpublisher.grants.patch")
	mGrantsDelete = apiregistry.MustResolve("androidpublisher.grants.delete")
)

// Operation names for *api.Error tagging, matching the REST reference so log
// readers can correlate them.
const (
	opUsersList    = "users.list"
	opUsersCreate  = "users.create"
	opUsersPatch   = "users.patch"
	opUsersDelete  = "users.delete"
	opGrantsCreate = "grants.create"
	opGrantsPatch  = "grants.patch"
	opGrantsDelete = "grants.delete"
)

// listPageSize is the users.list page size. The API caps it; we request a
// large page so the common (single-page) team needs one round trip, and
// paginate for the rest.
const listPageSize = 100

// User is the API-shaped Developer-account member. json tags mirror the API
// verbatim for the ADR-0003 pass-through. Grants are a field of the User:
// there is no standalone grants endpoint.
type User struct {
	Name                        string   `json:"name,omitempty"`
	Email                       string   `json:"email,omitempty"`
	AccessState                 string   `json:"accessState,omitempty"`
	ExpirationTime              string   `json:"expirationTime,omitempty"`
	Partial                     bool     `json:"partial,omitempty"`
	DeveloperAccountPermissions []string `json:"developerAccountPermissions,omitempty"`
	Grants                      []Grant  `json:"grants,omitempty"`
}

// Grant is the API-shaped per-app access of a User: appLevelPermissions on a
// single package. It is a field of the User resource.
type Grant struct {
	Name                string   `json:"name,omitempty"`
	PackageName         string   `json:"packageName,omitempty"`
	AppLevelPermissions []string `json:"appLevelPermissions,omitempty"`
}

// accountURL fills the account-scoped template of m (users.list,
// users.create). The three helpers below differ only in how many path
// parameters the method declares, which is exactly the distinction the old
// hand-built base/URL pairs encoded.
func accountURL(m apiregistry.Method, developerID string) (string, error) {
	return m.URL(map[string]string{"developersId": developerID})
}

// memberURL fills a member-scoped template: users.patch / users.delete, and
// the grants collection, which Discovery keys by the same two parameters.
func memberURL(m apiregistry.Method, developerID, email string) (string, error) {
	return m.URL(map[string]string{"developersId": developerID, "usersId": email})
}

// grantURL fills a single-Grant template (grants.patch / grants.delete),
// whose last segment is the package name.
func grantURL(m apiregistry.Method, developerID, email, pkg string) (string, error) {
	return m.URL(map[string]string{"developersId": developerID, "usersId": email, "grantsId": pkg})
}

// listPage is one users.list response page. Users is kept as raw messages so
// the ADR-0003 pass-through preserves each User object verbatim across the
// pagination merge.
type listPage struct {
	Users         []json.RawMessage `json:"users"`
	NextPageToken string            `json:"nextPageToken"`
}

// ListUsers fetches every member of the developer account, following
// nextPageToken to completion (no silent truncation). It returns the parsed
// Users (for the table/markdown views) and a merged `{"users":[…]}` JSON body
// for the ADR-0003 --output json pass-through, in which each User object is
// the verbatim bytes the API returned (the fully-consumed nextPageToken is
// dropped). An *api.Error surfaces on any failure.
func ListUsers(ctx context.Context, hc *http.Client, developerID string) ([]User, json.RawMessage, error) {
	users, rawAll, err := listUsersRaw(ctx, hc, developerID)
	if err != nil {
		return nil, nil, err
	}

	// Normalise to an empty slice so an account with no members marshals as
	// {"users":[]} rather than {"users":null}: the conventional empty-array
	// shape a consumer parsing `.users` expects.
	if rawAll == nil {
		rawAll = []json.RawMessage{}
	}
	merged, err := json.Marshal(struct {
		Users []json.RawMessage `json:"users"`
	}{Users: rawAll})
	if err != nil {
		return nil, nil, &api.Error{Operation: opUsersList, Package: developerID, Message: "marshal merged response: " + err.Error(), Cause: err}
	}
	return users, merged, nil
}

// listUsersRaw fetches every member of the developer account, following
// nextPageToken to completion (no silent truncation), and returns the parsed
// Users together with the verbatim per-User bytes the API returned: parallel
// slices sharing an index. It is the shared core of ListUsers (which merges the
// raw bytes into one `{"users":[…]}` body) and FindUserRaw (which filters to the
// one matched member). An *api.Error surfaces on any failure.
func listUsersRaw(ctx context.Context, hc *http.Client, developerID string) ([]User, []json.RawMessage, error) {
	var (
		users []User
		raw   []json.RawMessage
		token string
	)
	// seen guards against a server that repeats a pageToken forever.
	seen := map[string]struct{}{}
	for {
		// The pagination query stays hand-built: the resolver answers with
		// the path only (#516).
		u, err := accountURL(mUsersList, developerID)
		if err != nil {
			return nil, nil, &api.Error{Operation: opUsersList, Package: developerID, Message: err.Error(), Cause: err}
		}
		q := url.Values{}
		q.Set("pageSize", strconv.Itoa(listPageSize))
		if token != "" {
			q.Set("pageToken", token)
		}
		u += "?" + q.Encode()

		page, err := getJSON(ctx, hc, mUsersList.Verb, opUsersList, developerID, u)
		if err != nil {
			return nil, nil, err
		}
		var pg listPage
		if err := json.Unmarshal(page, &pg); err != nil {
			return nil, nil, &api.Error{Operation: opUsersList, Package: developerID, Message: "decode response: " + err.Error(), Cause: err}
		}
		for _, rawUser := range pg.Users {
			var usr User
			if err := json.Unmarshal(rawUser, &usr); err != nil {
				return nil, nil, &api.Error{Operation: opUsersList, Package: developerID, Message: "decode user: " + err.Error(), Cause: err}
			}
			users = append(users, usr)
			raw = append(raw, rawUser)
		}
		if pg.NextPageToken == "" {
			break
		}
		if _, dup := seen[pg.NextPageToken]; dup {
			return nil, nil, &api.Error{
				Operation: opUsersList,
				Package:   developerID,
				Message:   "pagination token loop detected in users.list (server repeated a nextPageToken)",
			}
		}
		seen[pg.NextPageToken] = struct{}{}
		token = pg.NextPageToken
	}
	return users, raw, nil
}

// FindUserRaw reads a single member (and their Grants) by email, returning the
// parsed User and the verbatim bytes users.list returned for it: the raw object
// an addressed read (`team users view`) passes through under ADR-0003. The API
// has no users.get, so it lists to completion and filters. Email match is
// case-insensitive (Google stores the address as entered, but addresses are
// case-insensitive). Returns (nil, nil, false, nil) when no member matches.
func FindUserRaw(ctx context.Context, hc *http.Client, developerID, email string) (*User, json.RawMessage, bool, error) {
	users, raw, err := listUsersRaw(ctx, hc, developerID)
	if err != nil {
		return nil, nil, false, err
	}
	want := strings.ToLower(strings.TrimSpace(email))
	for i := range users {
		if strings.ToLower(users[i].Email) == want {
			return &users[i], raw[i], true, nil
		}
	}
	return nil, nil, false, nil
}

// FindUser reads a single member (and their Grants) by email: the "read a User
// + their grants" helper every read-then-decide path reuses (#150). It is
// FindUserRaw without the raw pass-through bytes, for callers that only need the
// parsed shape. Returns (nil, false, nil) when no member matches.
func FindUser(ctx context.Context, hc *http.Client, developerID, email string) (*User, bool, error) {
	u, _, found, err := FindUserRaw(ctx, hc, developerID, email)
	return u, found, err
}

// Write-body types. They deliberately do NOT use omitempty on the permission
// arrays: a declarative replace must send an explicit [] to clear permissions
// (the same nil→[] normalisation testers.Update makes), which omitempty would
// drop, leaving the field absent and the clear a no-op.
type userWriteBody struct {
	Email                       string   `json:"email,omitempty"`
	DeveloperAccountPermissions []string `json:"developerAccountPermissions"`
}

type grantWriteBody struct {
	PackageName         string   `json:"packageName,omitempty"`
	AppLevelPermissions []string `json:"appLevelPermissions"`
}

// CreateUser invites a member with account-wide permissions (users.create).
// Returns the raw response body for the ADR-0003 pass-through.
func CreateUser(ctx context.Context, hc *http.Client, developerID, email string, perms []string) (json.RawMessage, error) {
	u, err := accountURL(mUsersCreate, developerID)
	if err != nil {
		return nil, &api.Error{Operation: opUsersCreate, Package: developerID, Message: err.Error(), Cause: err}
	}
	return sendJSON(ctx, hc, mUsersCreate.Verb, opUsersCreate, developerID, u, userWriteBody{Email: email, DeveloperAccountPermissions: nonNil(perms)})
}

// SetUserPermissions replaces a member's account-wide permissions declaratively
// (users.patch with updateMask=developerAccountPermissions). perms is sent as
// an explicit array (nil normalised to []) so an empty set clears the
// permissions rather than omitting the field.
func SetUserPermissions(ctx context.Context, hc *http.Client, developerID, email string, perms []string) (json.RawMessage, error) {
	u, err := memberURL(mUsersPatch, developerID, email)
	if err != nil {
		return nil, &api.Error{Operation: opUsersPatch, Package: developerID, Message: err.Error(), Cause: err}
	}
	u += "?updateMask=developerAccountPermissions"
	return sendJSON(ctx, hc, mUsersPatch.Verb, opUsersPatch, developerID, u, userWriteBody{DeveloperAccountPermissions: nonNil(perms)})
}

// DeleteUser off-boards a member (users.delete), targeted by email path. A
// 2xx (often an empty body) yields nil, nil.
func DeleteUser(ctx context.Context, hc *http.Client, developerID, email string) (json.RawMessage, error) {
	u, err := memberURL(mUsersDelete, developerID, email)
	if err != nil {
		return nil, &api.Error{Operation: opUsersDelete, Package: developerID, Message: err.Error(), Cause: err}
	}
	return sendJSON(ctx, hc, mUsersDelete.Verb, opUsersDelete, developerID, u, nil)
}

// CreateGrant grants a member per-app access (grants.create). perms is the
// resolved appLevelPermissions for the package.
func CreateGrant(ctx context.Context, hc *http.Client, developerID, email, pkg string, perms []string) (json.RawMessage, error) {
	u, err := memberURL(mGrantsCreate, developerID, email)
	if err != nil {
		return nil, &api.Error{Operation: opGrantsCreate, Package: developerID, Message: err.Error(), Cause: err}
	}
	return sendJSON(ctx, hc, mGrantsCreate.Verb, opGrantsCreate, developerID, u, grantWriteBody{PackageName: pkg, AppLevelPermissions: nonNil(perms)})
}

// PatchGrant replaces an existing grant's app-level permissions declaratively
// (grants.patch with updateMask=appLevelPermissions).
func PatchGrant(ctx context.Context, hc *http.Client, developerID, email, pkg string, perms []string) (json.RawMessage, error) {
	u, err := grantURL(mGrantsPatch, developerID, email, pkg)
	if err != nil {
		return nil, &api.Error{Operation: opGrantsPatch, Package: developerID, Message: err.Error(), Cause: err}
	}
	u += "?updateMask=appLevelPermissions"
	return sendJSON(ctx, hc, mGrantsPatch.Verb, opGrantsPatch, developerID, u, grantWriteBody{AppLevelPermissions: nonNil(perms)})
}

// nonNil normalises a nil slice to an empty one so a declarative write emits an
// explicit [] (clearing the set) rather than null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// DeleteGrant revokes a member's per-app access (grants.delete), targeted by
// email+package path, leaving the member in the account.
func DeleteGrant(ctx context.Context, hc *http.Client, developerID, email, pkg string) (json.RawMessage, error) {
	u, err := grantURL(mGrantsDelete, developerID, email, pkg)
	if err != nil {
		return nil, &api.Error{Operation: opGrantsDelete, Package: developerID, Message: err.Error(), Cause: err}
	}
	return sendJSON(ctx, hc, mGrantsDelete.Verb, opGrantsDelete, developerID, u, nil)
}

// getJSON performs a read and returns the raw 2xx body, or an *api.Error. The
// verb is passed in rather than hard-coded so it comes from the resolved
// method, like the URL (#513).
func getJSON(ctx context.Context, hc *http.Client, verb, op, developerID, u string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, verb, u, nil)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: developerID, Message: err.Error(), Cause: err}
	}
	return doRequest(hc, op, developerID, req)
}

// sendJSON performs a write with the resolved method's verb. When body is non-nil it is
// JSON-marshalled and Content-Type is set. Returns the raw 2xx body (possibly
// empty) or an *api.Error.
func sendJSON(ctx context.Context, hc *http.Client, method, op, developerID, u string, body any) (json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, &api.Error{Operation: op, Package: developerID, Message: "marshal payload: " + err.Error(), Cause: err}
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: developerID, Message: err.Error(), Cause: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return doRequest(hc, op, developerID, req)
}

// doRequest executes req and maps the response to (raw body, *api.Error). The
// Package field carries the developerID for these account-scoped calls so the
// error string identifies the target.
func doRequest(hc *http.Client, op, developerID string, req *http.Request) (json.RawMessage, error) {
	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: developerID, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(body, resp.StatusCode)
		return nil, &api.Error{
			Operation:  op,
			Package:    developerID,
			StatusCode: resp.StatusCode,
			Message:    msg,
			Reasons:    reasons,
		}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: op, Package: developerID, StatusCode: resp.StatusCode, Message: "read response body: " + readErr.Error(), Cause: readErr}
	}
	return json.RawMessage(raw), nil
}
