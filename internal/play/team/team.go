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
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/output"
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

// accountParams addresses an account-scoped template (users.list,
// users.create). The three helpers differ only in how many path parameters
// the method declares, which is exactly the distinction the old hand-built
// base/URL pairs encoded.
func accountParams(developerID string) map[string]string {
	return map[string]string{"developersId": developerID}
}

// memberParams addresses a member-scoped template: users.patch / users.delete,
// and the grants collection, which Discovery keys by the same two parameters.
func memberParams(developerID, email string) map[string]string {
	return map[string]string{"developersId": developerID, "usersId": email}
}

// grantParams addresses a single-Grant template (grants.patch /
// grants.delete), whose last segment is the package name.
func grantParams(developerID, email, pkg string) map[string]string {
	return map[string]string{"developersId": developerID, "usersId": email, "grantsId": pkg}
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
	merged, err := output.Marshal(struct {
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
	type member struct {
		user User
		raw  json.RawMessage
	}
	all, _, err := api.Paginate(api.Pager{Op: opUsersList, Target: developerID, What: "users.list"},
		func(token string, _ int) ([]member, string, error) {
			q := url.Values{}
			q.Set("pageSize", strconv.Itoa(listPageSize))
			if token != "" {
				q.Set("pageToken", token)
			}
			var pg listPage
			if _, err := api.DoJSON(ctx, hc, api.Call{
				Method: mUsersList, Op: opUsersList, Target: developerID,
				Params: accountParams(developerID),
				Query:  q,
			}, &pg); err != nil {
				return nil, "", err
			}
			page := make([]member, 0, len(pg.Users))
			for _, rawUser := range pg.Users {
				var usr User
				if err := json.Unmarshal(rawUser, &usr); err != nil {
					return nil, "", &api.Error{Operation: opUsersList, Package: developerID, Message: "decode user: " + err.Error(), Cause: err}
				}
				page = append(page, member{user: usr, raw: rawUser})
			}
			return page, pg.NextPageToken, nil
		})
	if err != nil {
		return nil, nil, err
	}
	var (
		users []User
		raw   []json.RawMessage
	)
	for _, m := range all {
		users = append(users, m.user)
		raw = append(raw, m.raw)
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
	return send(ctx, hc, mUsersCreate, opUsersCreate, developerID, accountParams(developerID), nil,
		userWriteBody{Email: email, DeveloperAccountPermissions: nonNil(perms)})
}

// SetUserPermissions replaces a member's account-wide permissions declaratively
// (users.patch with updateMask=developerAccountPermissions). perms is sent as
// an explicit array (nil normalised to []) so an empty set clears the
// permissions rather than omitting the field.
func SetUserPermissions(ctx context.Context, hc *http.Client, developerID, email string, perms []string) (json.RawMessage, error) {
	return send(ctx, hc, mUsersPatch, opUsersPatch, developerID, memberParams(developerID, email),
		url.Values{"updateMask": {"developerAccountPermissions"}},
		userWriteBody{DeveloperAccountPermissions: nonNil(perms)})
}

// DeleteUser off-boards a member (users.delete), targeted by email path. A
// 2xx (often an empty body) yields nil, nil.
func DeleteUser(ctx context.Context, hc *http.Client, developerID, email string) (json.RawMessage, error) {
	return send(ctx, hc, mUsersDelete, opUsersDelete, developerID, memberParams(developerID, email), nil, nil)
}

// CreateGrant grants a member per-app access (grants.create). perms is the
// resolved appLevelPermissions for the package.
func CreateGrant(ctx context.Context, hc *http.Client, developerID, email, pkg string, perms []string) (json.RawMessage, error) {
	return send(ctx, hc, mGrantsCreate, opGrantsCreate, developerID, memberParams(developerID, email), nil,
		grantWriteBody{PackageName: pkg, AppLevelPermissions: nonNil(perms)})
}

// PatchGrant replaces an existing grant's app-level permissions declaratively
// (grants.patch with updateMask=appLevelPermissions).
func PatchGrant(ctx context.Context, hc *http.Client, developerID, email, pkg string, perms []string) (json.RawMessage, error) {
	return send(ctx, hc, mGrantsPatch, opGrantsPatch, developerID, grantParams(developerID, email, pkg),
		url.Values{"updateMask": {"appLevelPermissions"}},
		grantWriteBody{AppLevelPermissions: nonNil(perms)})
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
	return send(ctx, hc, mGrantsDelete, opGrantsDelete, developerID, grantParams(developerID, email, pkg), nil, nil)
}

// send performs a write with m's verb and template. A nil body sends none (and
// no Content-Type); any other value is JSON-encoded. The Package field of an
// *api.Error carries the developerID for these account-scoped calls, so the
// error string identifies the target. Returns the raw 2xx body (possibly
// empty).
func send(ctx context.Context, hc *http.Client, m apiregistry.Method, op, developerID string, params map[string]string, q url.Values, body any) (json.RawMessage, error) {
	c := api.Call{Method: m, Op: op, Target: developerID, Params: params, Query: q}
	if body != nil {
		c.Body = body
	}
	return api.Do(ctx, hc, c)
}
