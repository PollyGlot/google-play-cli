package redact

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// samplePEM is a SYNTHETIC key body: never a real credential, and shaped so a
// leak is unmistakable in a failing test (the canary word is not base64).
const samplePEM = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BLEAKEDSECRETBODY\nQIDAQAB\n-----END PRIVATE KEY-----\n"

// canaries are the markers a leaked credential leaves behind. The canary test
// greps every rendered error for them.
var canaries = []string{"BEGIN PRIVATE KEY", "LEAKEDSECRETBODY", "ya29.", "eyJhbGciOi"}

func TestStringMasksCredentialShapes(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantAbsent  []string
		wantPresent []string
	}{
		{
			name:       "multiline PEM block",
			in:         "could not read credential: bad key: " + samplePEM,
			wantAbsent: []string{"LEAKEDSECRETBODY", "BEGIN PRIVATE KEY"},
			// The diagnostic prefix must survive: masking is not silencing.
			wantPresent: []string{"could not read credential", Mask},
		},
		{
			name:        "RSA-labelled PEM block",
			in:          "-----BEGIN RSA PRIVATE KEY-----\nLEAKEDSECRETBODY\n-----END RSA PRIVATE KEY-----",
			wantAbsent:  []string{"LEAKEDSECRETBODY"},
			wantPresent: []string{Mask},
		},
		{
			name:        "PEM folded onto one line by error wrapping",
			in:          `json: cannot unmarshal: "-----BEGIN PRIVATE KEY-----\nLEAKEDSECRETBODY\n-----END PRIVATE KEY-----\n"`,
			wantAbsent:  []string{"LEAKEDSECRETBODY"},
			wantPresent: []string{Mask},
		},
		{
			name:       "service-account JSON private_key field",
			in:         `{"type":"service_account","client_email":"ci@example.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nLEAKEDSECRETBODY\n-----END PRIVATE KEY-----\n","private_key_id":"abc123def456"}`,
			wantAbsent: []string{"LEAKEDSECRETBODY", "abc123def456"},
			// client_email is NOT a secret: it is the single most useful thing in a
			// misconfiguration report, so it must survive redaction.
			wantPresent: []string{"ci@example.iam.gserviceaccount.com", "service_account"},
		},
		{
			name:       "Authorization header, dump form",
			in:         "GET /v3/applications HTTP/1.1\r\nAuthorization: Bearer ya29.a0AfB_byLEAKEDSECRETBODYxyz\r\n",
			wantAbsent: []string{"LEAKEDSECRETBODY"},
			// The scheme survives so the log still says what kind of credential it was.
			wantPresent: []string{"Authorization: Bearer " + Mask, "/v3/applications"},
		},
		{
			name:        "Authorization header, Go map form",
			in:          `map[Authorization:["Bearer ya29.LEAKEDSECRETBODYxyz"] Accept:["application/json"]]`,
			wantAbsent:  []string{"LEAKEDSECRETBODY"},
			wantPresent: []string{Mask, "application/json"},
		},
		{
			name:        "bare ya29 access token",
			in:          "oauth2: token exchange failed for ya29.c.LEAKEDSECRETBODY0123456789",
			wantAbsent:  []string{"LEAKEDSECRETBODY"},
			wantPresent: []string{"token exchange failed", Mask},
		},
		{
			name:        "JWT assertion",
			in:          "assertion rejected: eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJjaUBleGFtcGxlIn0.LEAKEDSECRETBODYsignature",
			wantAbsent:  []string{"LEAKEDSECRETBODY", "eyJhbGciOi"},
			wantPresent: []string{"assertion rejected", Mask},
		},
		{
			name:        "generic access_token field",
			in:          `{"access_token":"LEAKEDSECRETBODY","expires_in":3599}`,
			wantAbsent:  []string{"LEAKEDSECRETBODY"},
			wantPresent: []string{"expires_in", Mask},
		},
		{
			name:        "two PEM blocks do not collapse into one match",
			in:          "-----BEGIN PRIVATE KEY-----\nAAA\n-----END PRIVATE KEY-----between-----BEGIN PRIVATE KEY-----\nBBB\n-----END PRIVATE KEY-----",
			wantAbsent:  []string{"AAA", "BBB"},
			wantPresent: []string{"between"},
		},
		{
			name:        "benign text passes through untouched",
			in:          "gplay: package com.example.app is not registered; run `gplay apps add com.example.app`",
			wantAbsent:  []string{Mask},
			wantPresent: []string{"com.example.app", "gplay apps add"},
		},
		{
			name: "a dotted identifier is not mistaken for a JWT",
			in:   "gplay: unknown method edits.tracks.update for v3.0.1",
			// No mask at all: a false positive here would blank real diagnostics.
			wantAbsent:  []string{Mask},
			wantPresent: []string{"edits.tracks.update", "v3.0.1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := String(tc.in)
			for _, want := range tc.wantAbsent {
				if strings.Contains(got, want) {
					t.Errorf("output still contains %q:\n%s", want, got)
				}
			}
			for _, want := range tc.wantPresent {
				if !strings.Contains(got, want) {
					t.Errorf("output lost %q:\n%s", want, got)
				}
			}
		})
	}
}

func TestStringEmpty(t *testing.T) {
	if got := String(""); got != "" {
		t.Errorf("String(%q) = %q, want empty", "", got)
	}
	if got := Bytes(nil); got != nil {
		t.Errorf("Bytes(nil) = %v, want nil", got)
	}
}

// The filter must sit on the writer, not on each call site: anything written
// through the wrapped writer is masked, whatever printed it.
func TestWriterMasks(t *testing.T) {
	var buf bytes.Buffer
	w := Writer(&buf)

	_, _ = fmt.Fprintf(w, "gplay: could not read credential: %s\n", samplePEM)

	got := buf.String()
	if strings.Contains(got, "LEAKEDSECRETBODY") {
		t.Fatalf("writer leaked key material:\n%s", got)
	}
	if !strings.Contains(got, "could not read credential") {
		t.Errorf("writer dropped the diagnostic:\n%s", got)
	}
}

// A short write is an error to most callers, and masking legitimately changes
// the byte count, so Write must report the INPUT length.
func TestWriterReportsInputLength(t *testing.T) {
	var buf bytes.Buffer
	w := Writer(&buf)
	in := []byte(samplePEM)
	n, err := w.Write(in)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(in) {
		t.Errorf("Write returned n=%d, want %d (the input length)", n, len(in))
	}
	if buf.Len() == len(in) {
		t.Errorf("nothing was masked: output is byte-identical to input")
	}
}

func TestWriterIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	once := Writer(&buf)
	twice := Writer(once)
	if once != twice {
		t.Errorf("wrapping an already-redacting writer stacked a second filter")
	}
}

func TestWriterNilIsDiscard(t *testing.T) {
	if got := Writer(nil); got != io.Discard {
		t.Errorf("Writer(nil) = %v, want io.Discard", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("boom") }

func TestWriterForwardsError(t *testing.T) {
	if _, err := Writer(failingWriter{}).Write([]byte("x")); err == nil {
		t.Error("expected the underlying write error to surface")
	}
}

// The canary test slice #460 asks for: take the failure messages gplay really
// renders when a credential is bad, push them through the stderr boundary, and
// grep the result for any credential marker.
func TestCanaryNoCredentialMarkersInRenderedErrors(t *testing.T) {
	saJSON := `{"type":"service_account","project_id":"p","private_key_id":"kid-LEAKEDSECRETBODY","private_key":"` +
		strings.ReplaceAll(samplePEM, "\n", `\n`) + `","client_email":"ci@example.iam.gserviceaccount.com","token_uri":"https://oauth2.googleapis.com/token"}`

	// Failure fixtures: the shapes a real gplay run produces when credentials go
	// wrong, each wrapping the offending bytes the way Go's stdlib does.
	fixtures := []error{
		fmt.Errorf("could not read credential: invalid character 'x' in %s", saJSON),
		fmt.Errorf("could not read credential: parsing key: %s", samplePEM),
		fmt.Errorf("oauth2: cannot fetch token: 401 Unauthorized\nResponse: %s", `{"error":"invalid_grant","access_token":"ya29.LEAKEDSECRETBODY"}`),
		fmt.Errorf("request failed: headers=map[Authorization:[\"Bearer eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiJ4In0.LEAKEDSECRETBODYsig\"]]"),
	}

	for i, fixture := range fixtures {
		var buf bytes.Buffer
		// Exactly what cmd/gplay/main.go does on the failure path.
		_, _ = fmt.Fprintln(Writer(&buf), "gplay:", fixture)

		got := buf.String()
		for _, canary := range canaries {
			if strings.Contains(got, canary) {
				t.Errorf("fixture %d leaked canary %q through the stderr boundary:\n%s", i, canary, got)
			}
		}
		if !strings.HasPrefix(got, "gplay: ") {
			t.Errorf("fixture %d lost the gplay: prefix:\n%s", i, got)
		}
	}
}
