// Package rotate implements `gplay signing rotate`: rotate an app's signing key
// to a new self-hosted Google Cloud KMS key (appsigning.rotateAppSigningKey).
//
// Rotation through the API applies ONLY to apps enrolled with a self-hosted
// Cloud KMS key. For an app on standard Google-managed Play App Signing, a key
// rotation request goes through the Play Console UI.
//
// DESTRUCTIVE tier (ADR-0017/0043): it swaps the live signing key of a real app,
// irreversibly and visibly to every device, so it requires --confirm (missing →
// exit 3) and is MarkMutating (GPLAY_READONLY → exit 4). Ships [experimental]
// (ADR-0010/0042).
package rotate

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/signing/signingcmd"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/appsigning"
)

// Input is the request-shaped struct cobra builds from the flags.
type Input struct {
	Package string
	KmsKey  string
	KmsCert string
	Lineage string
	Reason  string
	Confirm bool
	DryRun  bool
}

// Run is the business function the kernel invokes.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	if in.KmsKey == "" {
		return nil, signingcmd.Usagef("missing --kms-key: pass the Cloud KMS crypto key VERSION resource of the NEW signing key")
	}
	if in.KmsCert == "" {
		return nil, signingcmd.Usagef("missing --kms-cert <file.pem>: the new key's certificate travels with the key")
	}
	if in.Lineage == "" {
		return nil, signingcmd.Usagef("missing --lineage <file>: the API requires the apksigner proof-of-rotation lineage (apksigner rotate)")
	}
	reason, ok := appsigning.RotationReason(in.Reason)
	if !ok {
		return nil, signingcmd.Usagef("invalid --reason %q (valid: %s)", in.Reason, strings.Join(appsigning.RotationReasons(), ", "))
	}

	pkg, err := signingcmd.ResolvePackage(rc, in.Package)
	if err != nil {
		return nil, err
	}

	opts := appsigning.RotateOpts{KmsKeyResource: in.KmsKey, Reason: reason}
	if opts.PemCertificate, err = signingcmd.ReadPEM("kms-cert", in.KmsCert); err != nil {
		return nil, err
	}
	// The lineage is an apksigner binary artifact, not PEM: read it as-is.
	if opts.Lineage, err = signingcmd.ReadFile("lineage", in.Lineage); err != nil {
		return nil, err
	}

	if in.DryRun {
		return signingcmd.Payload{Verb: "rotate", Package: pkg, Requires: []string{"confirm"}, DryRun: true}, nil
	}
	if err := signingcmd.RequireConfirm(in.Confirm, "rotating the signing key of "+pkg+" cannot be undone and every future release is signed with the new key; pass --confirm to proceed (rehearse first with --dry-run)"); err != nil {
		return nil, err
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}
	resp, raw, err := appsigning.Rotate(rc.Ctx, httpClient, pkg, opts)
	if err != nil {
		return nil, signingcmd.Classify(pkg, err)
	}

	rows := make([]signingcmd.CertRow, 0, 1)
	if r, ok := signingcmd.NewCertRow("rotatedKey", resp.RotatedKeyCertificate); ok {
		rows = append(rows, r)
	}
	rc.Confirmf("rotated the app signing key of %q (reason %s)", pkg, reason)
	return signingcmd.Payload{Verb: "rotate", Package: pkg, Rows: rows, Raw: raw}, nil
}

// NewCommand returns the cobra command for `gplay signing rotate`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	reasons := strings.Join(appsigning.RotationReasons(), "|")
	cmd := &cobra.Command{
		Use:   "rotate",
		Short: "Rotate an app's signing key to a new self-hosted Cloud KMS key (irreversible)",
		Long: `Rotate the app signing key to a new private key hosted in your own Google Cloud
KMS instance (appsigning.rotateAppSigningKey).

WARNING: this applies ONLY to apps enrolled with a self-hosted Cloud KMS key
(see ` + "`gplay signing enroll`" + `). For an app on standard, Google-managed Play App
Signing, a key rotation request must be initiated through the Google Play
Console UI. See
` + appsigning.HelpCenterURL + `

The proof-of-rotation lineage is produced by apksigner, not by gplay:

  apksigner rotate --out lineage.bin --old-signer ... --new-signer ...
  gplay signing rotate --kms-key <resource> --kms-cert new-cert.pem \
    --lineage lineage.bin --reason routine-key-upgrade --confirm

--reason is required and takes one of: ` + reasons + `.

Prints the rotated key's certificate hashes (SHA256/SHA1/MD5); --output json
mirrors the API response verbatim. Requires --confirm (missing → exit 3);
rehearse first with --dry-run. GPLAY_READONLY refuses it (exit 4).`,
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
	cmd.Flags().StringVar(&in.KmsKey, "kms-key", "", "Cloud KMS crypto key version resource of the NEW signing key (required)")
	cmd.Flags().StringVar(&in.KmsCert, "kms-cert", "", "PEM file holding the certificate of the new Cloud KMS key (required)")
	cmd.Flags().StringVar(&in.Lineage, "lineage", "", "apksigner proof-of-rotation lineage file (required)")
	cmd.Flags().StringVar(&in.Reason, "reason", "", "why the key is rotated: "+reasons+" (required)")
	cmd.Flags().BoolVar(&in.Confirm, "confirm", false, "authorize the irreversible rotation")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "rehearse without any HTTP call (reports the --confirm requirement)")
	return cmd
}
