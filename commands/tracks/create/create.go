// Package create implements `gplay tracks create <name>`: the CLI glue
// that creates a custom closed-testing track. It is thin glue: resolve
// --package, open an Edit (open → tracks.create → commit), and render.
// The create endpoint supports exactly one type (CLOSED_TESTING) and the
// DEFAULT form factor, so there is no --type / --form-factor flag; and a
// closed test track is low-stakes and reversible, so there is no
// --confirm (unlike a production rollout). See docs/DESIGN.md §10.
package create

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/edits/commitflags"
	"github.com/PollyGlot/google-play-cli/internal/apihint"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/edits"
	"github.com/PollyGlot/google-play-cli/internal/play/tracks"
)

// Input is the request-shaped struct cobra builds from the positional
// <name> and the flags.
type Input struct {
	Package           string
	Name              string
	DryRun            bool
	KeepEditOnFailure bool
	Commit            commitflags.Flags
}

// Payload satisfies output.Renderable. Raw carries the tracks.create
// body for the ADR-0003 JSON pass-through; Name/Type/FormFactor/Kind are
// the gplay-shaped context shown in the human views. A created track is
// always a Closed track (closed testing is the only creatable type), so
// Kind is the derived "custom" label per CONTEXT.md. DryRun marks the
// preview path, where Raw is empty (no API body) and the JSON view emits
// a small gplay-shaped preview instead.
type Payload struct {
	Name       string          `json:"-"`
	Type       string          `json:"-"`
	FormFactor string          `json:"-"`
	Kind       string          `json:"-"`
	DryRun     bool            `json:"-"`
	Raw        json.RawMessage `json:"-"`
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
// sees the create did not actually run.
func renderTable(w io.Writer, p Payload) error {
	if p.DryRun {
		if _, err := fmt.Fprintln(w, "DRY-RUN: would create closed track"); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w,
		"track:       %s\ntype:        %s\nformFactor:  %s\nkind:        %s\n",
		p.Name, p.Type, p.FormFactor, p.Kind,
	)
	return err
}

// renderJSON emits the raw tracks.create body verbatim (ADR-0003
// pass-through) on the live path. The dry-run path has no API body, so it
// emits a small gplay-shaped preview of the TrackConfig instead: empty
// bytes would silently break the contract (every Payload field is
// json:"-").
func renderJSON(w io.Writer, p Payload) error {
	if len(p.Raw) > 0 {
		_, err := w.Write(p.Raw)
		return err
	}
	return output.WriteJSON(w, struct {
		Track      string `json:"track"`
		Type       string `json:"type"`
		FormFactor string `json:"formFactor"`
	}{Track: p.Name, Type: p.Type, FormFactor: p.FormFactor})
}

// renderMarkdown writes the same fields as a Markdown bullet list, with
// the dry-run marker as a leading line so a pasted report stands on its
// own.
func renderMarkdown(w io.Writer, p Payload) error {
	if p.DryRun {
		if _, err := fmt.Fprintln(w, "DRY-RUN: would create closed track"); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w,
		"- **track**: %s\n- **type**: %s\n- **formFactor**: %s\n- **kind**: %s\n",
		p.Name, p.Type, p.FormFactor, p.Kind,
	)
	return err
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
			FormFactor: tracks.FormFactorDefault,
			Kind:       "custom",
			DryRun:     true,
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
	)
	if err := edits.WithEdit(rc.Ctx, httpClient, pkg, edits.Options{KeepOnFailure: in.KeepEditOnFailure, ExplicitEditID: explicitEditID, Commit: in.Commit.For(rc, explicitEditID)}, func(editID string) error {
		t, r, e := tracks.Create(rc.Ctx, httpClient, pkg, editID, in.Name, tracks.FormFactorDefault)
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

	// DESIGN §8: a committed mutation prints one ✓ line on stderr. The
	// --dry-run path returned above, so this only runs after a real create.
	rc.ConfirmMutation(explicitEditID, "track %q created", created.Track)
	return Payload{
		Name:       created.Track,
		Type:       tracks.TrackTypeClosedTesting,
		FormFactor: tracks.FormFactorDefault,
		Kind:       "custom",
		Raw:        raw,
	}, nil
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

The create endpoint supports exactly one type (CLOSED_TESTING) and the
DEFAULT (phone) form factor, so there is no --type / --form-factor flag:
every created track is closed. Open / internal track creation has no API
path. Creating a track that already exists surfaces the API error (exit
30); gplay does not fake idempotency.

Runs inside an implicit Edit (open → tracks.create → commit). --dry-run
previews the TrackConfig without any HTTP; --keep-edit-on-failure skips
the auto-discard cleanup on failure (debug). No --confirm: a closed test
track is low-stakes and reversible.`,
		Example: `  # Create a Closed track for an internal QA group
  gplay tracks create qa-team

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
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "validate inputs and preview the TrackConfig without any HTTP call")
	cmd.Flags().BoolVar(&in.KeepEditOnFailure, "keep-edit-on-failure", false, "skip the auto-discard cleanup on failure (debug)")
	commitflags.Register(cmd, &in.Commit)
	return cmd
}
