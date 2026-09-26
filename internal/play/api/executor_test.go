package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// execRT records every request (body included) and answers with resp.
type execRT struct {
	reqs   []*http.Request
	bodies []string
	resp   func() *http.Response
	err    error
}

func (r *execRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.reqs = append(r.reqs, req)
	body := ""
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	r.bodies = append(r.bodies, body)
	if r.err != nil {
		return nil, r.err
	}
	return r.resp(), nil
}

func answer(status int, body string) func() *http.Response {
	return func() *http.Response {
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	}
}

var (
	mOrdersGet  = apiregistry.MustResolve("androidpublisher.orders.get")
	mRefund     = apiregistry.MustResolve("androidpublisher.orders.refund")
	mOTPPatch   = apiregistry.MustResolve("androidpublisher.monetization.onetimeproducts.patch")
	mImagesUp   = apiregistry.MustResolve("androidpublisher.edits.images.upload")
	mTracksList = apiregistry.MustResolve("androidpublisher.edits.tracks.list")
)

const pkg = "com.example.app"

func hc(rt http.RoundTripper) *http.Client { return &http.Client{Transport: rt} }

func TestDo_readReturnsBodyVerbatim(t *testing.T) {
	rt := &execRT{resp: answer(200, `{"orderId": "GPA.1"}`)}
	raw, err := api.Do(context.Background(), hc(rt), api.Call{
		Method: mRefund,
		Params: map[string]string{"packageName": pkg, "orderId": "GPA.1"},
		Query:  url.Values{"revoke": {"true"}},
		Target: pkg,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(raw) != `{"orderId": "GPA.1"}` {
		t.Errorf("raw = %q, want the body byte for byte", raw)
	}
	req := rt.reqs[0]
	if req.Method != http.MethodPost || req.URL.String() != "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/com.example.app/orders/GPA.1:refund?revoke=true" {
		t.Errorf("request = %s %s", req.Method, req.URL)
	}
	if ct := req.Header.Get("Content-Type"); ct != "" || rt.bodies[0] != "" {
		t.Errorf("a bodyless call sent Content-Type %q and body %q", ct, rt.bodies[0])
	}
}

func TestDo_bodyEncoding(t *testing.T) {
	cases := []struct {
		name     string
		body     any
		ct       string
		wantBody string
		wantCT   string
	}{
		{"raw message verbatim", json.RawMessage("{ \"productId\" : \"coins\" }"), "", "{ \"productId\" : \"coins\" }", "application/json"},
		{"bytes verbatim", []byte(`{"a":1}`), "", `{"a":1}`, "application/json"},
		{"value encoded", struct {
			ID string `json:"id"`
		}{"x"}, "", `{"id":"x"}`, "application/json"},
		{"content type override", []byte(`{}`), "application/json; charset=UTF-8", `{}`, "application/json; charset=UTF-8"},
	}
	for _, tc := range cases {
		rt := &execRT{resp: answer(200, `{}`)}
		_, err := api.Do(context.Background(), hc(rt), api.Call{
			Method:      mOTPPatch,
			Params:      map[string]string{"packageName": pkg, "productId": "coins"},
			Body:        tc.body,
			ContentType: tc.ct,
			Target:      pkg,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if rt.bodies[0] != tc.wantBody {
			t.Errorf("%s: body = %q, want %q", tc.name, rt.bodies[0], tc.wantBody)
		}
		if got := rt.reqs[0].Header.Get("Content-Type"); got != tc.wantCT {
			t.Errorf("%s: Content-Type = %q, want %q", tc.name, got, tc.wantCT)
		}
		if rt.reqs[0].GetBody == nil {
			t.Errorf("%s: no GetBody, so --retry could not replay the body", tc.name)
		}
	}
}

func TestDo_mediaStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shot.png")
	payload := []byte("\x89PNG fake bytes")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &execRT{resp: answer(200, `{"image":{"id":"i1"}}`)}
	_, err := api.Do(context.Background(), hc(rt), api.Call{
		Method:      mImagesUp,
		Params:      map[string]string{"packageName": pkg, "editId": "e1", "language": "en-US", "imageType": "phoneScreenshots"},
		Media:       true,
		Body:        &api.Stream{Open: func() (io.ReadCloser, error) { return os.Open(path) }, Size: int64(len(payload))},
		ContentType: "image/png",
		Target:      pkg,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	req := rt.reqs[0]
	if want := "https://androidpublisher.googleapis.com/upload/androidpublisher/v3/applications/com.example.app/edits/e1/listings/en-US/phoneScreenshots?uploadType=media"; req.URL.String() != want {
		t.Errorf("url = %s, want %s", req.URL, want)
	}
	if rt.bodies[0] != string(payload) || req.ContentLength != int64(len(payload)) || req.Header.Get("Content-Type") != "image/png" {
		t.Errorf("body=%q len=%d ct=%q", rt.bodies[0], req.ContentLength, req.Header.Get("Content-Type"))
	}
	replay, err := req.GetBody()
	if err != nil {
		t.Fatalf("GetBody: %v", err)
	}
	defer func() { _ = replay.Close() }()
	if b, _ := io.ReadAll(replay); !bytes.Equal(b, payload) {
		t.Errorf("replayed body = %q, want the payload again", b)
	}
}

func TestDo_errorEnvelope(t *testing.T) {
	rt := &execRT{resp: answer(409, `{"error":{"code":409,"message":"edit conflict","errors":[{"reason":"editAlreadyExists"}]}}`)}
	_, err := api.Do(context.Background(), hc(rt), api.Call{Method: mTracksList, Params: map[string]string{"packageName": pkg, "editId": "e1"}, Op: "tracks.list", Target: pkg})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *api.Error", err)
	}
	if apiErr.StatusCode != 409 || apiErr.Message != "edit conflict" || apiErr.Operation != "tracks.list" || apiErr.Package != pkg {
		t.Errorf("error = %+v", apiErr)
	}
	if len(apiErr.Reasons) != 1 || apiErr.Reasons[0] != "editAlreadyExists" || apiErr.ExitCode() != 60 {
		t.Errorf("reasons=%v exit=%d", apiErr.Reasons, apiErr.ExitCode())
	}
}

func TestDo_defaultOpStripsService(t *testing.T) {
	rt := &execRT{resp: answer(404, ``)}
	_, err := api.Do(context.Background(), hc(rt), api.Call{Method: mOrdersGet, Params: map[string]string{"packageName": pkg, "orderId": "x"}, Target: pkg})
	var apiErr *api.Error
	if !errors.As(err, &apiErr) || apiErr.Operation != "orders.get" {
		t.Errorf("err = %v, want Operation orders.get", err)
	}
}

func TestDo_transportErrorExits50(t *testing.T) {
	rt := &execRT{err: errors.New("connection reset")}
	_, err := api.Do(context.Background(), hc(rt), api.Call{Method: mOrdersGet, Params: map[string]string{"packageName": pkg, "orderId": "x"}, Target: pkg})
	if code := exit.For(err); code != 50 {
		t.Errorf("exit = %d, want 50 (err %v)", code, err)
	}
}

func TestDo_badParamsFailBeforeSending(t *testing.T) {
	rt := &execRT{resp: answer(200, `{}`)}
	_, err := api.Do(context.Background(), hc(rt), api.Call{Method: mOrdersGet, Params: map[string]string{"packageName": pkg}, Target: pkg})
	if err == nil || !strings.Contains(err.Error(), `"orderId"`) || len(rt.reqs) != 0 {
		t.Errorf("err=%v requests=%d, want a missing-param error and nothing sent", err, len(rt.reqs))
	}
}

func TestDo_bodyAtLimitIsAccepted(t *testing.T) {
	exact := strings.Repeat(" ", api.MaxAPISuccessBodyRead-2) + "{}"
	rt := &execRT{resp: answer(200, exact)}
	raw, err := api.Do(context.Background(), hc(rt), api.Call{Method: mOrdersGet, Params: map[string]string{"packageName": pkg, "orderId": "x"}, Target: pkg})
	if err != nil || len(raw) != api.MaxAPISuccessBodyRead {
		t.Errorf("len=%d err=%v, want the full %d bytes", len(raw), err, api.MaxAPISuccessBodyRead)
	}
}

func TestDoJSON_decodes(t *testing.T) {
	rt := &execRT{resp: answer(200, `{"orderId":"GPA.1","state":"PROCESSED"}`)}
	var o struct {
		OrderID string `json:"orderId"`
	}
	raw, err := api.DoJSON(context.Background(), hc(rt), api.Call{Method: mOrdersGet, Params: map[string]string{"packageName": pkg, "orderId": "GPA.1"}, Target: pkg}, &o)
	if err != nil || o.OrderID != "GPA.1" || !strings.Contains(string(raw), "PROCESSED") {
		t.Errorf("o=%+v raw=%s err=%v", o, raw, err)
	}
}
