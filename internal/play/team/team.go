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

var (
	mUsersList    = apiregistry.MustResolve("androidpublisher.users.list")
	mUsersCreate  = apiregistry.MustResolve("androidpublisher.users.create")
	mUsersPatch   = apiregistry.MustResolve("androidpublisher.users.patch")
	mUsersDelete  = apiregistry.MustResolve("androidpublisher.users.delete")
	mGrantsCreate = apiregistry.MustResolve("androidpublisher.grants.create")
	mGrantsPatch  = apiregistry.MustResolve("androidpublisher.grants.patch")
	mGrantsDelete = apiregistry.MustResolve("androidpublisher.grants.delete")
)

const (
	opUsersList    = "users.list"
	opUsersCreate  = "users.create"
	opUsersPatch   = "users.patch"
	opUsersDelete  = "users.delete"
	opGrantsCreate = "grants.create"
	opGrantsPatch  = "grants.patch"
	opGrantsDelete = "grants.delete"
)

const listPageSize = 100

type User struct {
	Name                        string   `json:"name,omitempty"`
	Email                       string   `json:"email,omitempty"`
	AccessState                 string   `json:"accessState,omitempty"`
	ExpirationTime              string   `json:"expirationTime,omitempty"`
	Partial                     bool     `json:"partial,omitempty"`
	DeveloperAccountPermissions []string `json:"developerAccountPermissions,omitempty"`
	Grants                      []Grant  `json:"grants,omitempty"`
}

type Grant struct {
	Name                string   `json:"name,omitempty"`
	PackageName         string   `json:"packageName,omitempty"`
	AppLevelPermissions []string `json:"appLevelPermissions,omitempty"`
}

func accountParams(developerID string) map[string]string {
	return map[string]string{"developersId": developerID}
}

func memberParams(developerID, email string) map[string]string {
	return map[string]string{"developersId": developerID, "usersId": email}
}

func grantParams(developerID, email, pkg string) map[string]string {
	return map[string]string{"developersId": developerID, "usersId": email, "grantsId": pkg}
}

type listPage struct {
	Users         []json.RawMessage `json:"users"`
	NextPageToken string            `json:"nextPageToken"`
}

func ListUsers(ctx context.Context, hc *http.Client, developerID string) ([]User, json.RawMessage, error) {
	users, rawAll, err := listUsersRaw(ctx, hc, developerID)
	if err != nil {
		return nil, nil, err
	}

	if rawAll == nil {
		rawAll = []json.RawMessage{}
	}
	merged, err := output.Marshal(struct {
		Users []json.RawMessage `json:"users"`
	}{Users: rawAll})
	if err != nil {
		return nil, nil, &api.Error{Operation: opUsersList, Resource: account(developerID), Message: "marshal merged response: " + err.Error(), Cause: err}
	}
	return users, merged, nil
}

func listUsersRaw(ctx context.Context, hc *http.Client, developerID string) ([]User, []json.RawMessage, error) {
	type member struct {
		user User
		raw  json.RawMessage
	}
	all, _, err := api.Paginate(api.Pager{Op: opUsersList, Resource: account(developerID), What: "users.list"},
		func(token string, _ int) ([]member, string, error) {
			q := url.Values{}
			q.Set("pageSize", strconv.Itoa(listPageSize))
			if token != "" {
				q.Set("pageToken", token)
			}
			var pg listPage
			if _, err := api.DoJSON(ctx, hc, api.Call{
				Method: mUsersList, Op: opUsersList, Resource: account(developerID),
				Params: accountParams(developerID),
				Query:  q,
			}, &pg); err != nil {
				return nil, "", err
			}
			page := make([]member, 0, len(pg.Users))
			for _, rawUser := range pg.Users {
				var usr User
				if err := json.Unmarshal(rawUser, &usr); err != nil {
					return nil, "", api.DecodeFailure(api.Error{Operation: opUsersList, Resource: account(developerID), Message: "decode user: " + err.Error(), Cause: err})
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

func FindUser(ctx context.Context, hc *http.Client, developerID, email string) (*User, bool, error) {
	u, _, found, err := FindUserRaw(ctx, hc, developerID, email)
	return u, found, err
}

type userWriteBody struct {
	Email                       string   `json:"email,omitempty"`
	DeveloperAccountPermissions []string `json:"developerAccountPermissions"`
}

type grantWriteBody struct {
	PackageName         string   `json:"packageName,omitempty"`
	AppLevelPermissions []string `json:"appLevelPermissions"`
}

func CreateUser(ctx context.Context, hc *http.Client, developerID, email string, perms []string) (json.RawMessage, error) {
	return send(ctx, hc, mUsersCreate, opUsersCreate, developerID, accountParams(developerID), nil,
		userWriteBody{Email: email, DeveloperAccountPermissions: nonNil(perms)})
}

func SetUserPermissions(ctx context.Context, hc *http.Client, developerID, email string, perms []string) (json.RawMessage, error) {
	return send(ctx, hc, mUsersPatch, opUsersPatch, developerID, memberParams(developerID, email),
		url.Values{"updateMask": {"developerAccountPermissions"}},
		userWriteBody{DeveloperAccountPermissions: nonNil(perms)})
}

func DeleteUser(ctx context.Context, hc *http.Client, developerID, email string) (json.RawMessage, error) {
	return send(ctx, hc, mUsersDelete, opUsersDelete, developerID, memberParams(developerID, email), nil, nil)
}

func CreateGrant(ctx context.Context, hc *http.Client, developerID, email, pkg string, perms []string) (json.RawMessage, error) {
	return send(ctx, hc, mGrantsCreate, opGrantsCreate, developerID, memberParams(developerID, email), nil,
		grantWriteBody{PackageName: pkg, AppLevelPermissions: nonNil(perms)})
}

func PatchGrant(ctx context.Context, hc *http.Client, developerID, email, pkg string, perms []string) (json.RawMessage, error) {
	return send(ctx, hc, mGrantsPatch, opGrantsPatch, developerID, grantParams(developerID, email, pkg),
		url.Values{"updateMask": {"appLevelPermissions"}},
		grantWriteBody{AppLevelPermissions: nonNil(perms)})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func DeleteGrant(ctx context.Context, hc *http.Client, developerID, email, pkg string) (json.RawMessage, error) {
	return send(ctx, hc, mGrantsDelete, opGrantsDelete, developerID, grantParams(developerID, email, pkg), nil, nil)
}

func send(ctx context.Context, hc *http.Client, m apiregistry.Method, op, developerID string, params map[string]string, q url.Values, body any) (json.RawMessage, error) {
	c := api.Call{Method: m, Op: op, Resource: account(developerID), Params: params, Query: q}
	if body != nil {
		c.Body = body
	}
	return api.Do(ctx, hc, c)
}

func account(developerID string) api.Resource {
	return api.Resource{Kind: api.KindDeveloperAccount, ID: developerID}
}
