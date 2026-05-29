// Package reply implements `gplay reviews reply`: post developer responses
// to user reviews, one at a time (--review-id + --reply) or in bulk from a
// TSV stream (--batch <file>|-). It is thin glue — resolve --package, build
// an authenticated client, and drive internal/play/reviews.Reply, with the
// TSV grammar owned by internal/reviews/batch and the 403/404 hints by
// commands/reviews/reviewerr (shared with `reviews list`).
//
// Unlike the renderable-returning commands, Run writes its own output: the
// stdout/stderr split here (single → a stderr confirmation plus the verbatim
// API body on stdout under --output json; batch → per-line OK/ERR on stderr
// plus a {"results":[...]} envelope on stdout under --output json) does not
// fit the three-Renderer payload model. Run therefore returns only an error
// — nil on full success, or one carrying the aggregate exit code.
package reply

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/commands/reviews/reviewerr"
	"github.com/PollyGlot/google-play-cli/internal/auth/token"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/reviews"
	"github.com/PollyGlot/google-play-cli/internal/reviews/batch"
)

// Input is the request-shaped struct cobra builds from flags.
type Input struct {
	Package  string
	ReviewID string
	Reply    string
	Batch    string // --batch path; "-" = stdin
	BatchSet bool   // whether --batch was supplied (distinguishes an empty path)
	DryRun   bool
}

// usageError is a CLI-misuse error (no mode, both modes, missing reply);
// ExitCode()=2 per docs/DESIGN.md §9.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// authError signals no credential resolved for a call that needs one;
// ExitCode()=10.
type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
func (e *authError) ExitCode() int { return 10 }

// Run validates the flag combination, resolves the package, and dispatches
// to single- or batch-reply. It writes all user-facing output itself and
// returns only an error (nil on full success).
func Run(rc *kernel.RunContext, in Input) error {
	// Mode selection is pure CLI misuse — validate before any resolution or
	// network so a bad invocation fails fast with exit 2.
	if in.BatchSet && (in.ReviewID != "" || in.Reply != "") {
		return &usageError{msg: "--batch is mutually exclusive with --review-id / --reply"}
	}
	if !in.BatchSet {
		if in.ReviewID == "" {
			return &usageError{msg: "nothing to reply to — pass --review-id <id> --reply <text>, or --batch <file>"}
		}
		if in.Reply == "" {
			return &usageError{msg: "--review-id requires --reply <text>"}
		}
	}

	pkg := in.Package
	if pkg == "" && rc.Resolved != nil {
		pkg = rc.Resolved.Pin
	}
	if pkg == "" {
		return &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}

	// --dry-run never touches the network, so a missing Account is fine.
	var httpClient *http.Client
	if !in.DryRun {
		if rc.Account == nil {
			return &authError{msg: "no Account resolved; run gplay auth login or set GPLAY_SERVICE_ACCOUNT"}
		}
		ts, err := token.Source(rc.Ctx, rc.Account)
		if err != nil {
			return &authError{msg: "could not build token source: " + err.Error()}
		}
		ctx := context.WithValue(rc.Ctx, oauth2.HTTPClient, baseHTTP(rc))
		httpClient = oauth2.NewClient(ctx, ts)
	}

	if in.BatchSet {
		return runBatch(rc, pkg, in, httpClient)
	}
	return runSingle(rc, pkg, in.ReviewID, in.Reply, in.DryRun, httpClient)
}

// runSingle posts one developer response. On --output json the verbatim API
// body is echoed to stdout; the human-facing confirmation always goes to
// stderr.
func runSingle(rc *kernel.RunContext, pkg, reviewID, text string, dryRun bool, hc *http.Client) error {
	if dryRun {
		_, _ = fmt.Fprintf(rc.Stderr, "DRY-RUN would reply on %s\n", reviewID)
		return nil
	}
	raw, err := reviews.Reply(rc.Ctx, hc, pkg, reviewID, text)
	if err != nil {
		return reviewerr.ClassifyReply(pkg, reviewID, err)
	}
	_, _ = fmt.Fprintf(rc.Stderr, "Reply posted on %s\n", reviewID)
	if rc.Format == output.FormatJSON {
		return writeRaw(rc.Stdout, raw)
	}
	return nil
}

// rowResult is one entry of the --output json batch envelope.
type rowResult struct {
	ReviewID string `json:"reviewId"`
	Status   string `json:"status"` // "ok" | "error" | "planned" (dry-run)
	Error    string `json:"error,omitempty"`
}

// batchError carries the aggregate exit code of a batch run: the highest
// exit code seen across rows (per issue #62 — a 404 row, exit 30, outranks a
// malformed line, exit 2). It is returned only when at least one row failed.
type batchError struct {
	code   int
	failed int
	total  int
}

func (e *batchError) Error() string {
	return fmt.Sprintf("%d of %d batch replies failed", e.failed, e.total)
}

func (e *batchError) ExitCode() int { return e.code }

// runBatch reads the TSV source, posts each row sequentially, and reports a
// per-line OK/ERR on stderr. A per-line failure (a malformed line, or an API
// error) is recorded and the batch continues. Under --output json the
// {"results":[...]} envelope is written to stdout. The aggregate exit code is
// the highest seen across rows (0 when every row succeeds).
func runBatch(rc *kernel.RunContext, pkg string, in Input, hc *http.Client) error {
	src, closeFn, err := batchSource(rc, in.Batch)
	if err != nil {
		return err
	}
	defer closeFn()

	lines := batch.Parse(src)
	if len(lines) == 0 {
		return &usageError{msg: "batch is empty — no <review-id>\\t<reply> rows found"}
	}

	worst := 0
	failed := 0
	results := make([]rowResult, 0, len(lines))

	for _, ln := range lines {
		if ln.Err != nil {
			_, _ = fmt.Fprintf(rc.Stderr, "ERR line %d: %v\n", ln.Num, ln.Err)
			results = append(results, rowResult{Status: "error", Error: ln.Err.Error()})
			worst = max(worst, 2) // malformed input is CLI misuse (exit 2)
			failed++
			continue
		}
		id := ln.Record.ReviewID
		if in.DryRun {
			_, _ = fmt.Fprintf(rc.Stderr, "DRY-RUN would reply on %s\n", id)
			results = append(results, rowResult{ReviewID: id, Status: "planned"})
			continue
		}
		if _, err := reviews.Reply(rc.Ctx, hc, pkg, id, ln.Record.Reply); err != nil {
			cerr := reviewerr.ClassifyReply(pkg, id, err)
			_, _ = fmt.Fprintf(rc.Stderr, "ERR %s %s\n", id, cerr.Error())
			results = append(results, rowResult{ReviewID: id, Status: "error", Error: cerr.Error()})
			worst = max(worst, exit.For(cerr))
			failed++
			continue
		}
		_, _ = fmt.Fprintf(rc.Stderr, "OK %s\n", id)
		results = append(results, rowResult{ReviewID: id, Status: "ok"})
	}

	if rc.Format == output.FormatJSON {
		// A broken/closed stdout is a hard failure the consumer cannot see
		// past, so it supersedes the per-row aggregate exit below.
		if err := output.WriteJSON(rc.Stdout, struct {
			Results []rowResult `json:"results"`
		}{Results: results}); err != nil {
			return err
		}
	}

	if worst != 0 {
		return &batchError{code: worst, failed: failed, total: len(lines)}
	}
	return nil
}

// batchSource opens the --batch input: stdin for "-", otherwise the file at
// path. The returned closeFn is always safe to call.
func batchSource(rc *kernel.RunContext, path string) (io.Reader, func(), error) {
	if path == "-" {
		if rc.Stdin == nil {
			return nil, func() {}, &usageError{msg: "--batch - reads the TSV from stdin, but stdin is not available"}
		}
		return rc.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, &usageError{msg: "cannot read --batch file: " + err.Error()}
	}
	return f, func() { _ = f.Close() }, nil
}

// writeRaw passes an API JSON body through to w verbatim, ensuring a single
// trailing newline so piped output is line-friendly. A write failure (broken
// or closed stdout) is returned so the caller can surface it rather than
// reporting a success the consumer never received.
func writeRaw(w io.Writer, raw []byte) error {
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if n := len(raw); n == 0 || raw[n-1] != '\n' {
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}

// baseHTTP exposes the test seam (ctx's oauth2.HTTPClient) so one injected
// RoundTripper covers both the /token exchange and the androidpublisher
// calls. Mirrors reviews list / tracks.
func baseHTTP(rc *kernel.RunContext) *http.Client {
	if v := rc.Ctx.Value(oauth2.HTTPClient); v != nil {
		if c, ok := v.(*http.Client); ok && c != nil {
			return c
		}
	}
	return http.DefaultClient
}

// NewCommand returns the cobra command for `gplay reviews reply`.
func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "reply",
		Short: "Post a developer reply to a review (single or --batch)",
		Long: `Post developer responses to user reviews.

Single:  gplay reviews reply --package P --review-id ID --reply "Thanks!"
Batch:   gplay reviews reply --package P --batch replies.tsv
         gplay reviews reply --package P --batch -   (read TSV from stdin)

The batch stream is TSV: one <review-id>\t<reply text> line per reply. Blank
lines and lines starting with # are skipped; a reply containing tabs or
newlines must be double-quoted (RFC 4180 quoting). Each line is posted
sequentially; a per-line failure is reported on stderr and does not abort the
rest. The process exits non-zero with the highest exit code seen across rows.

--review-id/--reply and --batch are mutually exclusive. --dry-run parses the
input and prints the planned actions without calling the API. --output json
echoes the API response (single) or a {"results":[...]} envelope (batch).`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			in.BatchSet = cmd.Flags().Changed("batch")
			return kernel.RunCobra(cmd, boot, outputFlag, func(rc *kernel.RunContext) (output.Renderable, error) {
				return nil, Run(rc, in)
			})
		},
	}
	output.RegisterFlag(cmd, &outputFlag)
	cmd.Flags().StringVar(&in.Package, "package", "", "Android package name (overrides .gplay/config.json pin)")
	cmd.Flags().StringVar(&in.ReviewID, "review-id", "", "the id of the review to reply to (single mode)")
	cmd.Flags().StringVar(&in.Reply, "reply", "", "the developer reply text (single mode)")
	cmd.Flags().StringVar(&in.Batch, "batch", "", "post replies from a TSV file (<review-id>\\t<reply>), or - for stdin")
	cmd.Flags().BoolVar(&in.DryRun, "dry-run", false, "parse and print the planned replies without calling the API")
	return cmd
}
