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

type Input struct {
	Package           string
	Name              string
	FormFactor        string
	DryRun            bool
	KeepEditOnFailure bool
	Commit            commitflags.Flags
	IfExists          ifexists.Policy
}

var policies = []ifexists.Policy{ifexists.Skip, ifexists.Fail}

type Payload struct {
	Name       string          `json:"-"`
	Type       string          `json:"-"`
	FormFactor string          `json:"-"`
	Kind       string          `json:"-"`
	DryRun     bool            `json:"-"`
	Raw        json.RawMessage `json:"-"`
	IfExists   ifexists.Policy `json:"-"`
	Skipped    bool            `json:"-"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return renderTable(w, p) },
		JSON:     func(w io.Writer) error { return renderJSON(w, p) },
		Markdown: func(w io.Writer) error { return renderMarkdown(w, p) },
	}
}

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

func (p Payload) previewPolicy() string {
	if !p.DryRun || p.IfExists == "" || p.IfExists == ifexists.Fail {
		return ""
	}
	return string(p.IfExists)
}

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

var formFactors = map[string]string{
	"":           tracks.FormFactorDefault,
	"default":    tracks.FormFactorDefault,
	"wear":       tracks.FormFactorWear,
	"automotive": tracks.FormFactorAutomotive,
}

func Run(rc *kernel.RunContext, in Input) (output.Renderable, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return nil, &exit.UsageError{Msg: "missing track name: gplay tracks create <name>"}
	}
	formFactor, ok := formFactors[strings.ToLower(strings.TrimSpace(in.FormFactor))]
	if !ok {
		return nil, exit.Usagef("invalid --form-factor %q: pass default, wear or automotive", in.FormFactor)
	}
	qualified, err := tracks.QualifiedName(in.Name, formFactor)
	if err != nil {
		return nil, exit.Usagef("%v: pass the bare name, or the --form-factor its prefix names", err)
	}
	in.Name = qualified
	policy, err := ifexists.Check(in.IfExists, policies...)
	if err != nil {
		return nil, err
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return nil, err
	}

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

	explicitEditID, err := rc.ExplicitEditID(pkg)
	if err != nil {
		return nil, err
	}

	var (
		created *tracks.Track
		raw     json.RawMessage
		skipped bool
	)
	if err := edits.WithEdit(rc.Ctx, httpClient, pkg, edits.Options{KeepOnFailure: in.KeepEditOnFailure, ExplicitEditID: explicitEditID, Commit: in.Commit.Options()}, func(editID string) error {
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
		in.Commit.WarnStaged(rc, explicitEditID)
		t, r, e := tracks.Create(rc.Ctx, httpClient, pkg, editID, in.Name, formFactor)
		if e != nil {
			return e
		}
		created, raw = t, r
		return nil
	}); err != nil {
		return nil, apihint.ForPackage(pkg, err)
	}

	if skipped {
		rc.Logf("track %q already exists, skipped (--if-exists skip): nothing created", in.Name)
		return Payload{Name: in.Name, Skipped: true, Raw: raw}, nil
	}

	rc.ConfirmMutation(explicitEditID, "track %q created", created.Track)
	return Payload{
		Name:       created.Track,
		Type:       tracks.TrackTypeClosedTesting,
		FormFactor: formFactor,
		Kind:       "custom",
		Raw:        raw,
	}, nil
}

func isNotFound(err error) bool {
	var apiErr *api.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

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
targets: default (phone, the default), wear or automotive. A wear or
automotive track is named behind its form factor prefix, as Play
requires: beta-wear is created as wear:beta-wear, a name that already
carries the prefix is kept, and another form factor's prefix exits 2.

Creating a track that already exists surfaces the API error (exit 30)
by default.
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

  # Create a Closed track for Wear OS testers (created as wear:beta-wear)
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
