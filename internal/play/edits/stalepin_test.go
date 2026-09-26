package edits_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// explicitWrite runs WithEdit in explicit mode with a closure failing like a
// write against the pinned Edit would, over fake.
func explicitWrite(fake *testkit.Fake, cause error) error {
	return edits.WithEdit(context.Background(), &http.Client{Transport: fake}, "com.example.app",
		edits.Options{ExplicitEditID: "edit-pinned"}, func(string) error { return cause })
}

// editsGet answers the edits.get probe on the pinned id with status.
func editsGet(status int, body string) testkit.Responder {
	return func(c testkit.Call) (int, string, bool) {
		if c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/edits/edit-pinned") {
			return status, body, true
		}
		return 0, "", false
	}
}

// TestWithEdit_explicitMode_vanishedEdit_namesThePin covers API-10: a write
// failing because the pinned Edit is gone says which local pin redirected it
// and how to clear it, while the exit and diagnostic codes stay the API's.
func TestWithEdit_explicitMode_vanishedEdit_namesThePin(t *testing.T) {
	notFound := &api.Error{Operation: "tracks.update", Package: "com.example.app", StatusCode: 404, Message: "Not found"}
	expired := &api.Error{Operation: "tracks.update", Package: "com.example.app", StatusCode: 400, Message: "This Edit has expired", Reasons: []string{"editExpired"}}

	cases := []struct {
		name     string
		cause    *api.Error
		probe    []testkit.Responder
		probes   int
		wantCode exit.Code
	}{
		{"404 confirmed by the probe", notFound, []testkit.Responder{editsGet(404, `{"error":{"code":404,"message":"Edit not found"}}`)}, 1, exit.CodeNotFound},
		{"editExpired needs no probe", expired, nil, 0, exit.CodeEditExpired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := testkit.NewFake(tc.probe...)
			err := explicitWrite(fake, tc.cause)

			var stale *edits.StalePinError
			if !errors.As(err, &stale) {
				t.Fatalf("err = %v (%T), want a *StalePinError", err, err)
			}
			for _, want := range []string{"edit-pinned", ".gplay/edit-com.example.app.json", "gplay edits discard --package com.example.app", tc.cause.Message} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
			diag := exit.Classify(err)
			if diag.ExitCode != tc.cause.ExitCode() || diag.Code != tc.wantCode {
				t.Errorf("Classify = (%d, %s), want (%d, %s): the wrap must not move the contract", diag.ExitCode, diag.Code, tc.cause.ExitCode(), tc.wantCode)
			}
			if got := len(fake.Calls()); got != tc.probes {
				t.Errorf("calls = %d, want %d probe(s)", got, tc.probes)
			}
		})
	}
}

// TestWithEdit_explicitMode_otherFailures_passThrough guards the other side:
// a 404 on a resource inside a live Edit (a track not created yet) keeps its
// own error and hint, and a non-404 is never probed.
func TestWithEdit_explicitMode_otherFailures_passThrough(t *testing.T) {
	cases := []struct {
		name   string
		cause  error
		probe  []testkit.Responder
		probes int
	}{
		{"404 inside a live Edit", &api.Error{Operation: "tracks.update", StatusCode: 404, Message: "Track not found"},
			[]testkit.Responder{editsGet(200, `{"id":"edit-pinned","expiryTimeSeconds":"1700000000"}`)}, 1},
		{"probe itself fails", &api.Error{Operation: "tracks.update", StatusCode: 404, Message: "Track not found"},
			[]testkit.Responder{editsGet(500, `{"error":{"code":500,"message":"backend"}}`)}, 1},
		{"403 is not probed", &api.Error{Operation: "tracks.update", StatusCode: 403, Message: "denied"}, nil, 0},
		{"non-API failure is not probed", errors.New("local IO"), nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := testkit.NewFake(tc.probe...)
			err := explicitWrite(fake, tc.cause)
			if err != tc.cause {
				t.Errorf("err = %v (%T), want the cause returned untouched", err, err)
			}
			if got := len(fake.Calls()); got != tc.probes {
				t.Errorf("calls = %d, want %d", got, tc.probes)
			}
		})
	}
}
