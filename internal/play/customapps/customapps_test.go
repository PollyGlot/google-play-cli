package customapps_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/testkit"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/customapps"
)

const sessionURI = "https://playcustomapp.googleapis.com/upload/session/abc123"

type resumeRT struct {
	t *testing.T

	initStatus int
	putStatus  int
	putBody    string

	calls        int
	initMethod   string
	initURL      string
	initBody     string
	initCType    string
	uploadCType  string
	uploadLength string
	putBytes     []byte
	putRange     string
}

func (r *resumeRT) serve(req *http.Request) (*http.Response, error) {
	r.calls++
	if req.Body != nil {
		defer func() { _ = req.Body.Close() }()
	}
	switch req.Method {
	case http.MethodPost:
		r.initMethod = req.Method
		r.initURL = req.URL.String()
		r.initCType = req.Header.Get("Content-Type")
		r.uploadCType = req.Header.Get("X-Upload-Content-Type")
		r.uploadLength = req.Header.Get("X-Upload-Content-Length")
		r.initBody = string(testkit.ReadBody(req))
		status := r.initStatus
		if status == 0 {
			status = http.StatusOK
		}
		resp := testkit.Response(status, "")
		if status >= 200 && status < 300 {
			resp.Header.Set("Location", sessionURI)
		}
		return resp, nil

	case http.MethodPut:
		r.putBytes = testkit.ReadBody(req)
		r.putRange = req.Header.Get("Content-Range")
		status := r.putStatus
		if status == 0 {
			status = http.StatusOK
		}
		return testkit.Response(status, r.putBody), nil

	default:
		r.t.Fatalf("unexpected method %s", req.Method)
		return nil, nil
	}
}

func writeArtifact(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	return p
}

const okResp = `{"title":"My Internal App","languageCode":"en-US","packageName":"com.example.internal","organizations":[{"organizationId":"org-123","organizationName":"Acme"}]}`

func TestCreate_resumableShape(t *testing.T) {
	rt := &resumeRT{t: t, putBody: okResp}
	hc := &http.Client{Transport: testkit.RoundTripFunc(rt.serve)}
	const artifactBytes = "PK\x03\x04 fake bundle bytes"
	artifact := writeArtifact(t, "app.aab", artifactBytes)

	app, raw, err := customapps.Create(context.Background(), hc, "1234567890", artifact, customapps.CreateOpts{
		Title:        "My Internal App",
		LanguageCode: "en-US",
		Organizations: []customapps.Organization{
			{OrganizationID: "org-123", OrganizationName: "Acme"},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rt.initMethod != http.MethodPost {
		t.Errorf("initiate method = %s, want POST", rt.initMethod)
	}
	for _, want := range []string{
		"playcustomapp.googleapis.com/upload/playcustomapp/v1",
		"/accounts/1234567890/customApps",
		"uploadType=resumable",
	} {
		if !strings.Contains(rt.initURL, want) {
			t.Errorf("initiate url %q missing %q", rt.initURL, want)
		}
	}
	if !strings.HasPrefix(rt.initCType, "application/json") {
		t.Errorf("initiate content-type = %q, want application/json", rt.initCType)
	}
	for _, want := range []string{`"title":"My Internal App"`, `"languageCode":"en-US"`, `"organizationId":"org-123"`} {
		if !strings.Contains(rt.initBody, want) {
			t.Errorf("initiate body %q missing %q", rt.initBody, want)
		}
	}
	if rt.uploadCType != "application/octet-stream" {
		t.Errorf("X-Upload-Content-Type = %q, want application/octet-stream", rt.uploadCType)
	}
	if want := strconv.Itoa(len(artifactBytes)); rt.uploadLength != want {
		t.Errorf("X-Upload-Content-Length = %q, want %q", rt.uploadLength, want)
	}
	if got := string(rt.putBytes); got != artifactBytes {
		t.Errorf("chunk PUT body = %q, want the artifact bytes verbatim", got)
	}
	if !strings.HasPrefix(rt.putRange, "bytes 0-") {
		t.Errorf("chunk Content-Range = %q, want a bytes 0-… range", rt.putRange)
	}
	if app.PackageName != "com.example.internal" {
		t.Errorf("packageName = %q, want com.example.internal", app.PackageName)
	}
	if strings.TrimSpace(string(raw)) != okResp {
		t.Errorf("raw = %s, want verbatim pass-through", raw)
	}
}

func TestCreate_noOrganizations_omitsField(t *testing.T) {
	rt := &resumeRT{t: t, putBody: okResp}
	hc := &http.Client{Transport: testkit.RoundTripFunc(rt.serve)}
	artifact := writeArtifact(t, "app.aab", "bytes")

	if _, _, err := customapps.Create(context.Background(), hc, "42", artifact, customapps.CreateOpts{
		Title:        "T",
		LanguageCode: "en-US",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if strings.Contains(rt.initBody, "organizations") {
		t.Errorf("initiate metadata should omit organizations when none given: %q", rt.initBody)
	}
}

func TestCreate_403_apiError(t *testing.T) {
	rt := &resumeRT{t: t, initStatus: 403}
	hc := &http.Client{Transport: testkit.RoundTripFunc(rt.serve)}
	artifact := writeArtifact(t, "app.aab", "bytes")

	_, _, err := customapps.Create(context.Background(), hc, "42", artifact, customapps.CreateOpts{Title: "T", LanguageCode: "en-US"})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.Error", err, err)
	}
	if apiErr.StatusCode != 403 {
		t.Errorf("status = %d, want 403", apiErr.StatusCode)
	}
	if apiErr.ExitCode() != 11 {
		t.Errorf("exit = %d, want 11", apiErr.ExitCode())
	}
}

func TestCreate_missingArtifact_exit20_noNetwork(t *testing.T) {
	rt := &resumeRT{t: t, putBody: okResp}
	hc := &http.Client{Transport: testkit.RoundTripFunc(rt.serve)}

	_, _, err := customapps.Create(context.Background(), hc, "42", filepath.Join(t.TempDir(), "nope.aab"), customapps.CreateOpts{Title: "T", LanguageCode: "en-US"})
	var ioErr *customapps.LocalIOError
	if !errors.As(err, &ioErr) {
		t.Fatalf("err = %v (%T), want *LocalIOError", err, err)
	}
	if ioErr.ExitCode() != 20 {
		t.Errorf("exit = %d, want 20", ioErr.ExitCode())
	}
	if rt.calls != 0 {
		t.Errorf("a missing artifact must make no network call; calls=%d", rt.calls)
	}
}

func TestCreate_directory_exit20(t *testing.T) {
	rt := &resumeRT{t: t, putBody: okResp}
	hc := &http.Client{Transport: testkit.RoundTripFunc(rt.serve)}

	_, _, err := customapps.Create(context.Background(), hc, "42", t.TempDir(), customapps.CreateOpts{Title: "T", LanguageCode: "en-US"})
	var ioErr *customapps.LocalIOError
	if !errors.As(err, &ioErr) {
		t.Fatalf("err = %v (%T), want *LocalIOError", err, err)
	}
	if rt.calls != 0 {
		t.Errorf("a directory must make no network call; calls=%d", rt.calls)
	}
}
