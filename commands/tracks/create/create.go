// Package create implements `gplay tracks create <name>`: the CLI glue
// that creates a custom closed-testing track. It is thin glue: resolve
// --package, open an Edit (open → tracks.create → commit), and render.
// The create endpoint supports exactly one type (CLOSED_TESTING), so there
// is no --type flag; --form-factor picks the TrackConfig form factor
// (default, wear or automotive) and defaults to the phone one. A closed
// test track is low-stakes and reversible, so there is no --confirm
// (unlike a production rollout). --if-exists skip makes a re-run converge:
// the track is read in the same Edit first and, when present, nothing is
// created (ADR-0050). See docs/DESIGN.md §10.
package create

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/ifexists"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// Input is the request-shaped struct cobra builds from the positional
// <name> and the flags.
type Input struct {
	Package           string
	Name              string
	FormFactor        string
	DryRun            bool
	KeepEditOnFailure bool
	Commit            commitflags.Flags
	// IfExists is the --if-exists policy; the zero value is ifexists.Fail.
	IfExists ifexists.Policy
}

// policies are the --if-exists values tracks create offers. A track has no
// update path here (its settings are its releases, owned by the release
// verbs), so update is refused rather than faked.
var policies = []ifexists.Policy{ifexists.Skip, ifexists.Fail}

// Payload satisfies output.Renderable. Raw carries the tracks.create
// body for the ADR-0003 JSON pass-through; Name/Type/FormFactor/Kind are
// the gplay-shaped context shown in the human views. A created track is
// always a Closed track (closed testing is the only creatable type), so
// Kind is the derived "custom" label per GLOSSARY.md. DryRun marks the
// preview path, where Raw is empty (no API body) and the JSON view emits
// a small gplay-shaped preview instead.
type Payload struct {
	Name       string          `json:"-"`
	Type       string          `json:"-"`
	FormFactor string          `json:"-"`
	Kind       string          `json:"-"`
	DryRun     bool            `json:"-"`
	Raw        json.RawMessage `json:"-"`
	// IfExists is the policy shown in the dry-run preview when it is not the
	// default, so a default preview keeps its exact bytes.
	IfExists ifexists.Policy `json:"-"`
	// Skipped marks an existing track left as is under --if-exists skip: Raw
	// is then the tracks.get body, and only the name is known for the human
	// views (tracks.get carries no type or form factor).
	Skipped bool `json:"-"`
}

// Renderers satisfies output.Renderable with one renderer per Format.
// The JSON form is the ADR-0003 tracks.create pass-through (or, on the
// dry-run path, a small gplay-shaped preview); table and markdown are
// human-shaped views over the same fields.
func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

// renderTable writes the created track's fields as aligned human lines.
// On the dry-run path it prepends a DRY-RUN context line so the operator
// sees the create did not actually run. A skipped create (--if-exists skip on
// an existing track) shows a SKIPPED line and the name instead.
func renderTable(w io.Writer, p Payload) error {
	if p.Skipped {
		_, err := fmt.Fprintf(w, "SKIPPED: track already exists, nothing created\ntrack:       %s\n", p.Name)
		return err
	}
	if p.DryRun {
		if _, err := fmt.Fprintln(w, "DRY-RUN: would create closed track"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w,
		"track:       %s\ntype:        %s\nformFactor:  %s\nkind:        %s\n",
		p.Name, p.Type, p.FormFactor, p.Kind,
	); err != nil {
		return err
	}
	if policy := p.previewPolicy(); policy != "" {
		_, err := fmt.Fprintf(w, "ifExists:    %s\n", policy)
		return err
	}
	return nil
}

// previewPolicy is the --if-exists policy a dry-run preview reports, or ""
// for the default, so a default preview keeps the bytes it had before the
// flag.
func (p Payload) previewPolicy() string {
	if !p.DryRun || p.IfExists == "" || p.IfExists == ifexists.Fail {
		return ""
	}
	return string(p.IfExists)
}

// renderJSON emits the raw tracks.create body verbatim (ADR-0003
// pass-through) on the live path. The dry-run path has no API body, so it
// emits a small gplay-shaped preview of the TrackConfig instead: empty
// bytes would silently break the contract (every Payload field is
// json:"-"). The preview leads with the dryRun marker every other mutating
// command's preview carries, so an agent tells it from a created track.
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, struct {
		DryRun     bool   `json:"dryRun,omitempty"`
		Track      string `json:"track"`
		Type       string `json:"type"`
		FormFactor string `json:"formFactor"`
		IfExists   string `json:"ifExists,omitempty"`
	}{DryRun: p.DryRun, Track: p.Name, Type: p.Type, FormFactor: p.FormFactor, IfExists: p.previewPolicy()})
}

// renderMarkdown writes the same fields as a Markdown bullet list, with
// the dry-run marker as a leading line so a pasted report stands on its
// own.
func renderMarkdown(w io.Writer, p Payload) error {
	if p.Skipped {
		_, err := fmt.Fprintf(w, "SKIPPED: track already exists, nothing created\n\n- **track**: %s\n", p.Name)
		return err
	}
	if p.DryRun {
		if _, err := fmt.Fprintln(w, "DRY-RUN: would create closed track"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w,
		"- **track**: %s\n- **type**: %s\n- **formFactor**: %s\n- **kind**: %s\n",
		p.Name, p.Type, p.FormFactor, p.Kind,
	); err != nil {
		return err
	}
	if policy := p.previewPolicy(); policy != "" {
		_, err := fmt.Fprintf(w, "- **ifExists**: %s\n", policy)
		return err
	}
	return nil
}

// formFactors maps the --form-factor values, lowercased, to the
// TrackConfig enum. "" stands for a direct caller that never set the field
// and gets the phone form factor, exactly the body sent before the flag.
var formFactors = map[string]string{
	"":           tracks.FormFactorDefault,
	"default":    tracks.FormFactorDefault,
	"wear":       tracks.FormFactorWear,
	"automotive": tracks.FormFactorAutomotive,
}

// Run is the business function the kernel invokes. It validates inputs,
// resolves the package, then (live path) opens an Edit and creates the
// closed track inside it (open → tracks.create → commit). The dry-run
// path previews the TrackConfig without building an HTTP client or
// touching the network.
func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, &exit.UsageError{Msg: "missing track name: gplay tracks create <name>"}
	}
	formFactor, ok := formFactors[strings.ToLower(strings.TrimSpace(in.FormFactor))]
	if !ok {
		return nil, exit.Usagef("invalid --form-factor %q: pass default, wear or automotive", in.FormFactor)
	}
	policy, err := ifexists.Check(in.IfExists, policies...)
	if err != nil {
		return nil, err
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

	// Dry-run skips auth entirely: nothing hits the network, so a missing
	// Account is not a problem here. Mirror promote: short-circuit before
	// AuthedClient and return the previewed TrackConfig.
	if in.DryRun {
		return Payload{
			Name:       in.Name,
			Type:       tracks.TrackTypeClosedTesting,
			FormFactor: formFactor,
			Kind:       "custom",
			DryRun:     true,
			IfExists:   policy,
		}, nil
	}

	httpClient, err := rc.AuthedClient()
	if err != nil {
		return nil, err
	}

	// Reuse an open explicit Edit when one is pinned (`gplay edits begin`); ""
	// keeps the implicit per-create Edit.
	explicitEditID, err := rc.ExplicitEditID(pkg)
	if err != nil {
		return nil, err
	}

	var (
		created *tracks.Track
		raw     json.RawMessage
		skipped bool
	)
	if err := edits.WithEdit(rc.Ctx, httpClient, pkg, edits.Options{KeepOnFailure: in.KeepEditOnFailure, ExplicitEditID: explicitEditID, Commit: in.Commit.For(rc, explicitEditID)}, func(editID string) error {
		// ADR-0050: existence is read in this same Edit (so a track created
		// earlier in an explicit Edit counts), never inferred from the
		// create's 400/409, whose meaning the API does not document. Only a
		// 404 means absent; any other read failure stops before the create.
		if policy == ifexists.Skip {
			_, r, e := tracks.Get(rc.Ctx, httpClient, pkg, editID, in.Name)
			switch {
			case e == nil:
				raw, skipped = r, true
				return edits.ErrNothingToCommit
			case !isNotFound(e):
				return e
			}
		}
		t, r, e := tracks.Create(rc.Ctx, httpClient, pkg, editID, in.Name, formFactor)
		if e != nil {
			return e
		}
		created, raw = t, r
		return nil
	}); err != nil {
		// A tracks.create 400/409 "track already exists" is not a 403/404,
		// so it surfaces verbatim (exit 30/60) rather than faking idempotency.
		return nil, apihint.ForPackage(pkg, err)
	}

	if skipped {
		// Not a ✓: nothing was created, committed or staged.
		rc.Logf("track %q already exists, skipped (--if-exists skip): nothing created", in.Name)
		return Payload{Name: in.Name, Skipped: true, Raw: raw}, nil
	}

	// DESIGN §8: a committed mutation prints one ✓ line on stderr. The
	// --dry-run path returned above, so this only runs after a real create.
	rc.ConfirmMutation(explicitEditID, "track %q created", created.Track)
	return Payload{
		Name:       created.Track,
		Type:       tracks.TrackTypeClosedTesting,
		FormFactor: formFactor,
		Kind:       "custom",
		Raw:        raw,
	}, nil
}

// isNotFound reports whether a tracks.get failed with HTTP 404: the one
// answer that means the track does not exist.
func isNotFound(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// NewCommand returns the cobra command for `gplay tracks create`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a custom closed-testing track",
		Long: `Create a custom closed-testing track named <name>.

The create endpoint supports exactly one type (CLOSED_TESTING), so there
is no --type flag: every created track is closed. Open / internal track
creation has no API path. --form-factor picks the devices the track
targets: default (phone, the default), wear or automotive. Creating a
track that already exists surfaces the API error (exit 30) by default.
--if-exists skip reads the track first in the same Edit: when it exists,
nothing is created, the existing track is printed (tracks.get, verbatim
under --output json) and the implicit Edit is discarded, not committed.
A re-run of the same create then succeeds without a change.

Runs inside an implicit Edit (open → tracks.create → commit). --dry-run
previews the TrackConfig without any HTTP; --keep-edit-on-failure skips
the auto-discard cleanup on failure (debug). No --confirm: a closed test
track is low-stakes and reversible.`,
		Example: `  # Create a Closed track for an internal QA group
  gplay tracks create qa-team

  # Create a Closed track for Wear OS testers
  gplay tracks create beta-wear --form-factor wear

  # Create the track unless it already exists (safe to re-run in CI)
  gplay tracks create qa-team --if-exists skip

  # Preview the TrackConfig without any HTTP call
  gplay tracks create qa-team --dry-run --output json`,
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			b := boot
			b.Stdout = cmd.OutOrStdout()
			b.Stderr = cmd.ErrOrStderr()
			in.Name = args[0]
			return kernel.Run(b, kernel.FromCobra(cmd, outputFlag), func(rc *kernel.RunContext) (output.Renderable, error) {
				return Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.FormFactor, "form-factor", "default", "form factor the track targets: default (phone), wear or automotive")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the TrackConfig without any HTTP call")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	commitflags.Register(cmd, &in.Commit)
	ifexists.Register(cmd, &in.IfExists, "track", policies...)
	return cmd
}
