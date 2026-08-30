// Package enroll implements `gplay signing enroll`: enroll an app into Play App
// Signing with a self-hosted Google Cloud KMS key (appsigning.enrollApp).
//
// This is NOT standard Play App Signing enrollment: enrolling with a
// Google-generated or Google-managed key cannot be done through any API, only
// the Play Console. The method exists for enterprises whose compliance rules
// force them to keep key custody in their own Cloud KMS instance.
//
// DESTRUCTIVE tier (ADR-0017/0043): enrollment changes the live signing key of
// a real app and is irreversible with an external effect, so it requires
// --confirm (missing → exit 3) and is MarkMutating (GPLAY_READONLY → exit 4).
// Ships [experimental] (ADR-0010/0042): a low-traffic enterprise surface whose
// flags may still settle.
package enroll

import (
	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/signing/signingcmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
)

// Input is the request-shaped struct cobra builds from the flags.
type Input struct {
	Package    string
	KmsKey     string
	NewApp     bool
	KmsCert    string
	UploadCert string
	Confirm    bool
	DryRun     bool
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.KmsKey == "" {
		return nil, signingcmd.Usagef("missing --kms-key: pass the Cloud KMS crypto key VERSION resource (projects/*/locations/*/keyRings/*/cryptoKeys/*/cryptoKeyVersions/*)")
	}
	// The API's request is a oneof: enrollNewApp carries the key AND its
	// certificate, enrollExistingApp carries the key alone. Enforcing the pair
	// here turns a silent wrong-branch request into a named flag error.
	if in.NewApp && in.KmsCert == "" {
		return nil, signingcmd.Usagef("--new-app requires --kms-cert <file.pem>: enrolling a new app registers the certificate of the KMS key at the same time")
	}
	if !in.NewApp && in.KmsCert != "" {
		return nil, signingcmd.Usagef("--kms-cert is only valid with --new-app: enrolling an app that already ships uses the KMS key alone")
	}

	pkg, err := signingcmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}

	opts := appsigning.EnrollOpts{KmsKeyResource: in.KmsKey, NewApp: in.NewApp}
	if in.KmsCert != "" {
		if opts.PemCertificate, err = signingcmd.ReadPEM("kms-cert", in.KmsCert); err != nil {
			return nil, err
		}
	}
	if in.UploadCert != "" {
		if opts.PemUploadCertificate, err = signingcmd.ReadPEM("upload-cert", in.UploadCert); err != nil {
			return nil, err
		}
	}

	if in.DryRun {
		return signingcmd.Payload{Verb: "enroll", Package: pkg, Requires: []string{"confirm"}, DryRun: true}, nil
	}
	if err := signingcmd.RequireConfirm(in.Confirm, "enrolling "+pkg+" into Play App Signing changes the key its releases are signed with and cannot be undone; pass --confirm to proceed (rehearse first with --dry-run)"); err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	resp, raw, err := appsigning.Enroll(rc.Ctx, httpClient, pkg, opts)
	if err != nil {
		return nil, signingcmd.Classify(pkg, err)
	}

	rows := make([]signingcmd.CertRow, 0, 2)
	if r, ok := signingcmd.NewCertRow("signing", resp.SigningCertificate); ok {
		rows = append(rows, r)
	}
	if r, ok := signingcmd.NewCertRow("upload", resp.UploadCertificate); ok {
		rows = append(rows, r)
	}
	rc.Confirmf("enrolled %q into Play App Signing with the Cloud KMS key", pkg)
	return signingcmd.Payload{Verb: "enroll", Package: pkg, Rows: rows, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay signing enroll`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Enroll an app into Play App Signing with a self-hosted Cloud KMS key (irreversible)",
		Long: `Enroll an app into Play App Signing using a private key you host in your own
Google Cloud KMS instance (appsigning.enrollApp).

WARNING: this is NOT standard Play App Signing enrollment. Enrolling with a
Google-generated or Google-managed key CANNOT be done through the API: do it in
the Play Console. This command is strictly for organizations with a compliance,
regulatory or policy requirement to keep key custody in an external Cloud KMS
instance.

Prerequisite: an active Cloud KMS key whose IAM policy grants Google Play the
Decrypt and Sign permissions. See
` + appsigning.HelpCenterURL + `

Two shapes, one per kind of app:

  # an app that has already published to Open testing or Production
  gplay signing enroll --kms-key projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1 --confirm

  # a brand-new app (never published to Open testing or Production)
  gplay signing enroll --new-app --kms-key <resource> --kms-cert cert.pem --confirm

Pass --upload-cert <file.pem> to register your CI upload certificate in the same
call. Certificates are read from files: never paste base64 on a command line.

Prints the returned certificate hashes (SHA256/SHA1/MD5); --output json mirrors
the API response verbatim. Requires --confirm (missing → exit 3); rehearse first
with --dry-run. GPLAY_READONLY refuses it (exit 4).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name or app ID (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.KmsKey, "kms-key", "", "Cloud KMS crypto key version resource of the signing key (required)")
	cmd.Flags().BoolVar(&in.NewApp, "new-app", false, "the app has never published to Open testing or Production (requires --kms-cert)")
	cmd.Flags().StringVar(&in.KmsCert, "kms-cert", "", "PEM file holding the certificate of the Cloud KMS key (only with --new-app)")
	cmd.Flags().StringVar(&in.UploadCert, "upload-cert", "", "PEM file holding the upload certificate to register alongside")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the irreversible enrollment")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "rehearse without any HTTP call (reports the --confirm requirement)")
	return cmd
}
