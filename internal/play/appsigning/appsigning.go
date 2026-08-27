// Package appsigning performs the hand-rolled HTTP calls for Play App Signing
// with a self-hosted Google Cloud KMS key (the appsigning resource; ADR-0007
// raw HTTP). Two methods, both POST custom verbs on the application resource:
// appsigning.enrollApp and appsigning.rotateAppSigningKey.
//
// Like internal/play/orders and internal/play/recovery these calls sit OUTSIDE
// the Edit model: they target /applications/{name}:appSigning directly, never an
// editId. The path parameter the API calls `name` is either the Android package
// name or the app ID, so it is a plain path segment.
//
// The surface is reserved for organizations that keep key custody in an
// external Cloud KMS: standard enrollment (a Google-generated or Google-managed
// key) is not exposed by the API at all, and rotating a Google-managed key goes
// through the Play Console UI only.
package appsigning

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"

	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// op* tag *api.Error with the REST reference method id.
const (
	opEnroll = "appsigning.enrollApp"
	opRotate = "appsigning.rotateAppSigningKey"
)

// HelpCenterURL is Google's reference for the Cloud KMS prerequisites (an
// active key plus the IAM grants to Google Play). Named once here so the two
// commands' help text cannot drift apart.
const HelpCenterURL = "https://support.google.com/googleplay/android-developer/answer/9842756"

// --- request body types (mirror the API schemas verbatim) ---

// cloudKmsKey mirrors CloudKmsKey: the resource id of the private key hosted in
// the developer's Cloud KMS.
type cloudKmsKey struct {
	CryptoKeyVersionResource string `json:"cryptoKeyVersionResource,omitempty"`
}

// cloudKmsKeyAndCert mirrors CloudKmsKeyAndCert. PemCertificate is a `format:
// byte` field: encoding/json base64-encodes a []byte, which is exactly the
// proto3-JSON wire shape the API expects, so the CLI hands over raw file bytes.
type cloudKmsKeyAndCert struct {
	CloudKmsKey    *cloudKmsKey `json:"cloudKmsKey,omitempty"`
	PemCertificate []byte       `json:"pemCertificate,omitempty"`
}

type enrollExistingApp struct {
	CloudKmsKey *cloudKmsKey `json:"cloudKmsKey,omitempty"`
}

type enrollNewApp struct {
	CloudKmsKeyAndCert *cloudKmsKeyAndCert `json:"cloudKmsKeyAndCert,omitempty"`
}

// enrollAppRequest mirrors EnrollAppRequest. enrollExistingApp and enrollNewApp
// are the two branches of a oneof: exactly one is ever set, which is why the
// command validates the flag combination client-side rather than letting the
// API answer with a shape error.
type enrollAppRequest struct {
	EnrollExistingApp    *enrollExistingApp `json:"enrollExistingApp,omitempty"`
	EnrollNewApp         *enrollNewApp      `json:"enrollNewApp,omitempty"`
	PemUploadCertificate []byte             `json:"pemUploadCertificate,omitempty"`
}

type rotatedCloudKmsKey struct {
	CloudKmsKeyAndCert        *cloudKmsKeyAndCert `json:"cloudKmsKeyAndCert,omitempty"`
	SigningCertificateLineage []byte              `json:"signingCertificateLineage,omitempty"`
}

type rotateAppSigningKeyRequest struct {
	RotatedCloudKmsKey *rotatedCloudKmsKey `json:"rotatedCloudKmsKey,omitempty"`
	KeyRotationReason  string              `json:"keyRotationReason,omitempty"`
}

// --- response types ---

// CertificateHashes mirrors the CertificateHashes schema: the three hex digests
// of a certificate. The complete body is always available verbatim under
// --output json (ADR-0003), so this stays exactly as wide as the human view.
type CertificateHashes struct {
	CertificateHashMd5    string `json:"certificateHashMd5,omitempty"`
	CertificateHashSha1   string `json:"certificateHashSha1,omitempty"`
	CertificateHashSha256 string `json:"certificateHashSha256,omitempty"`
}

// EnrollResponse mirrors EnrollAppResponse. UploadCertificate is set iff the
// request carried pemUploadCertificate.
type EnrollResponse struct {
	SigningCertificate *CertificateHashes `json:"signingCertificate,omitempty"`
	UploadCertificate  *CertificateHashes `json:"uploadCertificate,omitempty"`
}

// RotateResponse mirrors RotateAppSigningKeyResponse.
type RotateResponse struct {
	RotatedKeyCertificate *CertificateHashes `json:"rotatedKeyCertificate,omitempty"`
}

// --- rotation reasons ---

// rotationReasons maps the CLI's lowercase-hyphen --reason choices to the API
// enum. KEY_ROTATION_REASON_UNSPECIFIED is deliberately absent: the API rejects
// it, so it is not a choice gplay can offer.
var rotationReasons = map[string]string{
	"compromised-key":                "COMPROMISED_KEY",
	"use-stronger-key":               "USE_STRONGER_KEY",
	"use-same-key-for-multiple-apps": "USE_SAME_KEY_FOR_MULTIPLE_APPS",
	"routine-key-upgrade":            "ROUTINE_KEY_UPGRADE",
	"other":                          "OTHER",
}

// RotationReason maps a --reason choice to the API enum. The bool reports
// whether the choice is known; the caller turns a miss into a usage error that
// lists RotationReasons().
func RotationReason(choice string) (string, bool) {
	v, ok := rotationReasons[choice]
	return v, ok
}

// RotationReasons lists the accepted --reason choices, sorted, for help text
// and for the usage error raised on an unknown value.
func RotationReasons() []string {
	out := make([]string, 0, len(rotationReasons))
	for k := range rotationReasons {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- calls ---

// EnrollOpts is the request-shaped input the command builds from its flags.
// NewApp picks the EnrollNewApp branch (which requires PemCertificate); the
// zero value picks EnrollExistingApp (which must not carry one).
type EnrollOpts struct {
	KmsKeyResource       string
	NewApp               bool
	PemCertificate       []byte
	PemUploadCertificate []byte
}

// RotateOpts is the request-shaped input for a key rotation. Reason is the API
// enum value (map a CLI choice through RotationReason first).
type RotateOpts struct {
	KmsKeyResource string
	PemCertificate []byte
	Lineage        []byte
	Reason         string
}

func appSigningURL(name, verb string) string {
	return api.AndroidPubBase + "/applications/" + url.PathEscape(name) + "/appSigning:" + verb
}

// Enroll enrolls an app into Play App Signing with a self-hosted Cloud KMS key
// (appsigning.enrollApp). It returns the parsed response plus the verbatim body
// for the ADR-0003 --output json pass-through. Irreversible and externally
// visible (the live signing key of the app changes), so the command gates it
// behind --confirm before reaching here.
func Enroll(ctx context.Context, hc *http.Client, name string, opts EnrollOpts) (EnrollResponse, json.RawMessage, error) {
	key := &cloudKmsKey{CryptoKeyVersionResource: opts.KmsKeyResource}
	req := enrollAppRequest{PemUploadCertificate: opts.PemUploadCertificate}
	if opts.NewApp {
		req.EnrollNewApp = &enrollNewApp{CloudKmsKeyAndCert: &cloudKmsKeyAndCert{
			CloudKmsKey:    key,
			PemCertificate: opts.PemCertificate,
		}}
	} else {
		req.EnrollExistingApp = &enrollExistingApp{CloudKmsKey: key}
	}
	var out EnrollResponse
	raw, err := post(ctx, hc, opEnroll, name, appSigningURL(name, "enrollApp"), req, &out)
	if err != nil {
		return EnrollResponse{}, nil, err
	}
	return out, raw, nil
}

// Rotate rotates an app's signing key to a new self-hosted Cloud KMS key
// (appsigning.rotateAppSigningKey). The proof-of-rotation lineage is produced by
// apksigner, never by gplay: the CLI only carries the file's bytes.
func Rotate(ctx context.Context, hc *http.Client, name string, opts RotateOpts) (RotateResponse, json.RawMessage, error) {
	req := rotateAppSigningKeyRequest{
		RotatedCloudKmsKey: &rotatedCloudKmsKey{
			CloudKmsKeyAndCert: &cloudKmsKeyAndCert{
				CloudKmsKey:    &cloudKmsKey{CryptoKeyVersionResource: opts.KmsKeyResource},
				PemCertificate: opts.PemCertificate,
			},
			SigningCertificateLineage: opts.Lineage,
		},
		KeyRotationReason: opts.Reason,
	}
	var out RotateResponse
	raw, err := post(ctx, hc, opRotate, name, appSigningURL(name, "rotateAppSigningKey"), req, &out)
	if err != nil {
		return RotateResponse{}, nil, err
	}
	return out, raw, nil
}

// post marshals body, POSTs it, and decodes the 2xx response into out while
// returning the verbatim bytes. A non-2xx surfaces as *api.Error so the
// exit-code taxonomy maps transparently.
func post(ctx context.Context, hc *http.Client, op, name, u string, body any, out any) (json.RawMessage, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: name, Message: "marshal request: " + err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return nil, &api.Error{Operation: op, Package: name, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := hc.Do(req)
	if err != nil {
		return nil, &api.Error{Operation: op, Package: name, Message: err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		eb, _ := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPIErrorBodyRead))
		msg, reasons := api.ParseErrorEnvelope(eb, resp.StatusCode)
		return nil, &api.Error{Operation: op, Package: name, StatusCode: resp.StatusCode, Message: msg, Reasons: reasons}
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, api.MaxAPISuccessBodyRead))
	if readErr != nil {
		return nil, &api.Error{Operation: op, Package: name, StatusCode: resp.StatusCode, Message: "read response body: " + readErr.Error(), Cause: readErr}
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, &api.Error{Operation: op, Package: name, StatusCode: resp.StatusCode, Message: "decode response: " + err.Error(), Cause: err}
	}
	return json.RawMessage(raw), nil
}
