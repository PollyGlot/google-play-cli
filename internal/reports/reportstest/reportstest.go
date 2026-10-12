// Package reportstest holds the test doubles the `gplay reports` command tests
// share: a Reporting bucket served by the testkit Play fake, and a RunContext
// wired to it with a parsed service account and a resolved developer-id.
package reportstest

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/serviceaccount"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/config"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
	"github.com/PollyGlot/google-play-cli/internal/transport"
)

// DeveloperID is the developer-id NewRC resolves, so the derived bucket is
// pubsite_prod_rev_123456.
const DeveloperID = "123456"

// Bucket is a fake Reporting bucket: its objects by key, and the HTTP status
// forced on any call (list or fetch) whose object key or list prefix starts
// with a Deny key. Sizes overrides the size the listing reports for an
// object (its byte length otherwise), so a test can list an export too large
// to build.
type Bucket struct {
	Objects map[string][]byte
	Deny    map[string]int
	Sizes   map[string]string
}

// Responder answers storage.objects.list (filtered by the prefix query, every
// object reported with its size and a fixed update time) and media fetches.
// An unknown object is a 404, as GCS answers.
func (b Bucket) Responder() testkit.Responder {
	return func(c testkit.Call) (int, string, bool) {
		if c.Host != "storage.googleapis.com" {
			return 0, "", false
		}
		const base = "/storage/v1/b/"
		rest := strings.TrimPrefix(c.Path, base)
		_, after, isObject := strings.Cut(rest, "/o/")
		q, _ := url.ParseQuery(c.Query)
		key := q.Get("prefix")
		if isObject {
			key = after
		}
		for p, status := range b.Deny {
			if strings.HasPrefix(key, p) {
				return status, `{"error":{"code":` + strconv.Itoa(status) + `,"message":"denied by the fake"}}`, true
			}
		}
		if isObject {
			body, ok := b.Objects[after]
			if !ok {
				return http.StatusNotFound, `{"error":{"code":404,"message":"No such object"}}`, true
			}
			return http.StatusOK, string(body), true
		}
		type item struct {
			Name    string `json:"name"`
			Size    string `json:"size"`
			Updated string `json:"updated"`
		}
		items := []item{}
		for name, body := range b.Objects {
			if strings.HasPrefix(name, key) {
				size := strconv.Itoa(len(body))
				if s, ok := b.Sizes[name]; ok {
					size = s
				}
				items = append(items, item{Name: name, Size: size, Updated: "2026-10-04T08:00:00.000Z"})
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		out, _ := json.Marshal(map[string]any{"items": items})
		return http.StatusOK, string(out), true
	}
}

// UTF16LE encodes s as UTF-16LE with a BOM: the byte shape of Play's
// statistics exports.
func UTF16LE(s string) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE})
	for _, u := range utf16.Encode([]rune(s)) {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], u)
		buf.Write(b[:])
	}
	return buf.Bytes()
}

// Zip packs one CSV member the way Play packs a finance export: a ZIP
// holding the report file.
func Zip(t *testing.T, member string, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, err := w.Create(member)
	if err != nil {
		t.Fatalf("zip create %s: %v", member, err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatalf("zip write %s: %v", member, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// NewRC wires a RunContext whose HTTP client goes through rt (behind a
// ScopeObserver, so the token exchange's scope can be asserted), with the
// storage scope declared, a parsed service account, and DeveloperID resolved.
// It returns the observer and the stderr buffer warnings land in.
func NewRC(t *testing.T, rt http.RoundTripper) (*kernel.RunContext, *transport.ScopeObserver, *bytes.Buffer) {
	t.Helper()
	sa, err := serviceaccount.Parse(testkit.ServiceAccountJSON(t))
	if err != nil {
		t.Fatalf("serviceaccount.Parse: %v", err)
	}
	observed, obs := transport.WithScopeObserver(rt)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: observed})
	var stderr bytes.Buffer
	rc := kernel.NewForTest(ctx, kernel.Boot{Stderr: &stderr}, kernel.Inputs{Format: output.FormatJSON, Scope: token.StorageReadOnlyScope})
	rc.Account = sa
	rc.Resolved = &config.Resolved{DeveloperID: DeveloperID}
	return rc, obs, &stderr
}
