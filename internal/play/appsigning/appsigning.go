package appsigning

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/apiregistry"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

const (
	opEnroll = "appsigning.enrollApp"
	opRotate = "appsigning.rotateAppSigningKey"
)

var (
	mEnroll = apiregistry.MustResolve("androidpublisher.appsigning.enrollApp")
	mRotate = apiregistry.MustResolve("androidpublisher.appsigning.rotateAppSigningKey")
)

const HelpCenterURL = "https://support.google.com/googleplay/android-developer/answer/9842756"

type cloudKmsKey struct {
	CryptoKeyVersionResource string `json:"cryptoKeyVersionResource,omitempty"`
}

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

type CertificateHashes struct {
	CertificateHashMd5    string `json:"certificateHashMd5,omitempty"`
	CertificateHashSha1   string `json:"certificateHashSha1,omitempty"`
	CertificateHashSha256 string `json:"certificateHashSha256,omitempty"`
}

type EnrollResponse struct {
	SigningCertificate *CertificateHashes `json:"signingCertificate,omitempty"`
	UploadCertificate  *CertificateHashes `json:"uploadCertificate,omitempty"`
}

type RotateResponse struct {
	RotatedKeyCertificate *CertificateHashes `json:"rotatedKeyCertificate,omitempty"`
}

var rotationReasons = map[string]string{
	"compromised-key":                "COMPROMISED_KEY",
	"use-stronger-key":               "USE_STRONGER_KEY",
	"use-same-key-for-multiple-apps": "USE_SAME_KEY_FOR_MULTIPLE_APPS",
	"routine-key-upgrade":            "ROUTINE_KEY_UPGRADE",
	"other":                          "OTHER",
}

func RotationReason(choice string) (string, bool) {
	v, ok := rotationReasons[choice]
	return v, ok
}

func RotationReasons() []string {
	out := make([]string, 0, len(rotationReasons))
	for k := range rotationReasons {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type EnrollOpts struct {
	KmsKeyResource       string
	NewApp               bool
	PemCertificate       []byte
	PemUploadCertificate []byte
}

type RotateOpts struct {
	KmsKeyResource string
	PemCertificate []byte
	Lineage        []byte
	Reason         string
}

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
	raw, err := post(ctx, hc, mEnroll, opEnroll, name, req, &out)
	if err != nil {
		return EnrollResponse{}, nil, err
	}
	return out, raw, nil
}

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
	raw, err := post(ctx, hc, mRotate, opRotate, name, req, &out)
	if err != nil {
		return RotateResponse{}, nil, err
	}
	return out, raw, nil
}

func post(ctx context.Context, hc *http.Client, m apiregistry.Method, op, name string, body any, out any) (json.RawMessage, error) {
	raw, err := api.Do(ctx, hc, api.Call{Method: m, Op: op, Resource: signingTarget(name), Params: map[string]string{"name": name}, Body: body})
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return nil, api.DecodeFailure(api.Error{Operation: op, Resource: signingTarget(name), Message: "decode response: " + err.Error(), Cause: err})
	}
	return raw, nil
}

func signingTarget(name string) api.Resource {
	if name != "" && strings.Trim(name, "0123456789") == "" {
		return api.Resource{Kind: api.KindApp, ID: name}
	}
	return api.PackageResource(name)
}
