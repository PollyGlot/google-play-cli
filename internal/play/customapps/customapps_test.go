// Package customapps_test exercises the hand-rolled multipart/related upload to
// playcustomapp.accounts.customApps.create against a RoundTripper mock — no
// network. It asserts the wire shape (URL, uploadType=multipart, the JSON
// metadata part and the artifact media part) and the error mapping.
package customapps_test

import (
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/customapps"
)

// captureRT records the single create POST and returns a canned response.
type captureRT struct {
	t *testing.T

	status int
	body   string

	calls       int
	method      string
	url         string
	contentType string
	metaPart    string
	mediaPart   []byte
}

func (r *captureRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.calls++
	r.method = req.Method
	r.url = req.URL.String()
	r.contentType = req.Header.Get("Content-Type")

	// Parse the multipart/related body so the test asserts the real wire shape,
	// not an internal representation.
	mediaType, params, err := mime.ParseMediaType(r.contentType)
	if err == nil && strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(req.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				r.t.Fatalf("read multipart part: %v", err)
			}
			data, _ := io.ReadAll(p)
			ct := p.Header.Get("Content-Type")
			switch {
			case strings.Contains(ct, "application/json"):
				r.metaPart = string(data)
			default:
				r.mediaPart = data
			}
		}
	}

	return &http.Response{
		StatusCode: r.status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(r.body)),
	}, nil
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

// TestCreate_multipartShape asserts the POST hits the playcustomapp upload
// endpoint with uploadType=multipart, a multipart/related content type, the
// JSON metadata part (title/languageCode/organizations) and the artifact bytes
// as the media part — and that the response is parsed + passed through verbatim.
func TestCreate_multipartShape(t *testing.T) {
	rt := &captureRT{t: t, status: 200, body: okResp}
	hc := &http.Client{Transport: rt}
	artifact := writeArtifact(t, "app.aab", "PK\x03\x04 fake bundle bytes")

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
	if rt.method != http.MethodPost {
		t.Errorf("method = %s, want POST", rt.method)
	}
	for _, want := range []string{
		"playcustomapp.googleapis.com/upload/playcustomapp/v1",
		"/accounts/1234567890/customApps",
		"uploadType=multipart",
	} {
		if !strings.Contains(rt.url, want) {
			t.Errorf("url %q missing %q", rt.url, want)
		}
	}
	if !strings.HasPrefix(rt.contentType, "multipart/related;") {
		t.Errorf("content-type = %q, want multipart/related", rt.contentType)
	}
	for _, want := range []string{`"title":"My Internal App"`, `"languageCode":"en-US"`, `"organizationId":"org-123"`} {
		if !strings.Contains(rt.metaPart, want) {
			t.Errorf("metadata part %q missing %q", rt.metaPart, want)
		}
	}
	if got := string(rt.mediaPart); got != "PK\x03\x04 fake bundle bytes" {
		t.Errorf("media part = %q, want the artifact bytes verbatim", got)
	}
	// Parsed response carries the output-only packageName.
	if app.PackageName != "com.example.internal" {
		t.Errorf("packageName = %q, want com.example.internal", app.PackageName)
	}
	// Raw is the verbatim API body (ADR-0003).
	if strings.TrimSpace(string(raw)) != okResp {
		t.Errorf("raw = %s, want verbatim pass-through", raw)
	}
}

// TestCreate_noOrganizations_omitsField asserts that with no orgs the metadata
// part has no organizations key (the app then defaults to the account's org).
func TestCreate_noOrganizations_omitsField(t *testing.T) {
	rt := &captureRT{t: t, status: 200, body: okResp}
	hc := &http.Client{Transport: rt}
	artifact := writeArtifact(t, "app.aab", "bytes")

	if _, _, err := customapps.Create(context.Background(), hc, "42", artifact, customapps.CreateOpts{
		Title:        "T",
		LanguageCode: "en-US",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if strings.Contains(rt.metaPart, "organizations") {
		t.Errorf("metadata part should omit organizations when none given: %q", rt.metaPart)
	}
}

// TestCreate_403_apiError asserts a 403 surfaces as an *api.Error mapping to
// exit 11 (authorization) — the layer the command turns into an
// agent-resolvable refusal.
func TestCreate_403_apiError(t *testing.T) {
	rt := &captureRT{t: t, status: 403, body: `{"error":{"code":403,"message":"caller lacks CAN_CREATE_MANAGED_PLAY_APPS","errors":[{"reason":"forbidden"}]}}`}
	hc := &http.Client{Transport: rt}
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

// TestCreate_missingArtifact_exit20_noNetwork asserts an unreadable artifact is
// a client-side *LocalIOError (exit 20) and makes no HTTP call.
func TestCreate_missingArtifact_exit20_noNetwork(t *testing.T) {
	rt := &captureRT{t: t, status: 200, body: okResp}
	hc := &http.Client{Transport: rt}

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

// TestCreate_directory_exit20 asserts a non-regular path is rejected up front.
func TestCreate_directory_exit20(t *testing.T) {
	rt := &captureRT{t: t, status: 200, body: okResp}
	hc := &http.Client{Transport: rt}

	_, _, err := customapps.Create(context.Background(), hc, "42", t.TempDir(), customapps.CreateOpts{Title: "T", LanguageCode: "en-US"})
	var ioErr *customapps.LocalIOError
	if !errors.As(err, &ioErr) {
		t.Fatalf("err = %v (%T), want *LocalIOError", err, err)
	}
	if rt.calls != 0 {
		t.Errorf("a directory must make no network call; calls=%d", rt.calls)
	}
}
