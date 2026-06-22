package download_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/oauth2"

	downloadcmd "github.com/PollyGlot/google-play-cli/commands/releases/generated/download"
	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

const apkBytes = "PK\x03\x04generated-apk-payload"

type dlRT struct {
	mu     sync.Mutex
	calls  []string
	getURL string
	status int
}

func (r *dlRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.URL.Host == "oauth2.googleapis.com" || strings.HasSuffix(req.URL.Path, "/token") {
		r.calls = append(r.calls, "POST /token")
		return jsonResp(200, `{"access_token":"a.b.c","token_type":"Bearer","expires_in":3600}`), nil
	}
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	r.getURL = req.URL.String()
	if r.status != 0 {
		return jsonResp(r.status, `{"error":{"message":"nope"}}`), nil
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
		Body:       io.NopCloser(strings.NewReader(apkBytes)),
	}, nil
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func signedSAJSON(t *testing.T) []byte {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	raw, _ := json.Marshal(map[string]any{"type": "service_account", "project_id": "p", "private_key": string(pemBytes), "client_email": "ci@p.iam.gserviceaccount.com", "token_uri": "https://oauth2.googleapis.com/token"})
	return raw
}

func newRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(signedSAJSON(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: rt})
	var stdout, stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stdout: &stdout, Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON})
	rc.Account = sa
	return rc, &stdout, &stderr
}

// TestRun_writesBytesToFile asserts the artifact lands on disk, the request
// carried alt=media on the :download endpoint, and the ✓ went to stderr (not the
// data path).
func TestRun_writesBytesToFile(t *testing.T) {
	rt := &dlRT{}
	rc, stdout, stderr := newRC(t, rt)
	dest := filepath.Join(t.TempDir(), "out.apk")

	err := downloadcmd.Run(rc, downloadcmd.Input{Package: "com.example.app", VersionCode: 142, DownloadID: "dl-abc", Dest: dest})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	got, rerr := os.ReadFile(dest)
	if rerr != nil {
		t.Fatalf("ReadFile: %v", rerr)
	}
	if string(got) != apkBytes {
		t.Errorf("file = %q, want %q", got, apkBytes)
	}
	if !strings.Contains(rt.getURL, "/generatedApks/142/downloads/dl-abc:download") || !strings.Contains(rt.getURL, "alt=media") {
		t.Errorf("url %q missing :download / alt=media", rt.getURL)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should stay empty for a file download, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "✓") || !strings.Contains(stderr.String(), dest) {
		t.Errorf("stderr %q should carry the ✓ confirmation naming the dest", stderr.String())
	}
}

// TestRun_destDash_streamsToStdout asserts --dest - puts the raw bytes on stdout.
func TestRun_destDash_streamsToStdout(t *testing.T) {
	rt := &dlRT{}
	rc, stdout, stderr := newRC(t, rt)

	err := downloadcmd.Run(rc, downloadcmd.Input{Package: "com.example.app", VersionCode: 142, DownloadID: "dl-abc", Dest: "-"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if stdout.String() != apkBytes {
		t.Errorf("stdout = %q, want raw apk bytes", stdout.String())
	}
	if !strings.Contains(stderr.String(), "✓") || !strings.Contains(stderr.String(), "stdout") {
		t.Errorf("stderr %q should confirm the stdout stream", stderr.String())
	}
}

// TestRun_404_exit30_noPartialFile asserts an unknown downloadId fails with exit
// 30 and leaves no partial file behind.
func TestRun_404_exit30_noPartialFile(t *testing.T) {
	rt := &dlRT{status: 404}
	rc, _, _ := newRC(t, rt)
	dest := filepath.Join(t.TempDir(), "out.apk")

	err := downloadcmd.Run(rc, downloadcmd.Input{Package: "com.example.app", VersionCode: 142, DownloadID: "bad", Dest: dest})
	assertExit(t, err, 30)
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("a failed download must not leave a partial file at %s", dest)
	}
}

// TestRun_403_exit11 covers the refusal path.
func TestRun_403_exit11(t *testing.T) {
	rt := &dlRT{status: 403}
	rc, _, _ := newRC(t, rt)
	err := downloadcmd.Run(rc, downloadcmd.Input{Package: "com.example.app", VersionCode: 142, DownloadID: "x", Dest: "-"})
	assertExit(t, err, 11)
}

// TestRun_validation_exit2_noNetwork asserts the missing-argument guards fire
// before any network call.
func TestRun_validation_exit2_noNetwork(t *testing.T) {
	cases := []struct {
		name string
		in   downloadcmd.Input
	}{
		{"missing downloadId", downloadcmd.Input{Package: "com.example.app", VersionCode: 142, Dest: "-"}},
		{"missing version", downloadcmd.Input{Package: "com.example.app", DownloadID: "x", Dest: "-"}},
		{"missing dest", downloadcmd.Input{Package: "com.example.app", VersionCode: 142, DownloadID: "x"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rt := &dlRT{}
			rc, _, _ := newRC(t, rt)
			err := downloadcmd.Run(rc, c.in)
			assertExit(t, err, 2)
			if len(rt.calls) != 0 {
				t.Errorf("must not reach the network; calls=%v", rt.calls)
			}
		})
	}
}

func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	var c interface{ ExitCode() int }
	if !errors.As(err, &c) || c.ExitCode() != want {
		t.Fatalf("err = %v, want exit %d", err, want)
	}
}
