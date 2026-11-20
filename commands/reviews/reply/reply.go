package reply

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/commands/reviews/reviewerr"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/reviews"
	"github.com/PollyGlot/google-play-cli/internal/reviews/batch"
)

type Input struct {
	Package  string
	ReviewID string
	Reply    string
	Batch    string
	BatchSet bool
	DryRun   bool
}

func Run(rc *kernel.RunContext, in Input) error {
	if in.BatchSet && (in.ReviewID != "" || in.Reply != "") {
		return &exit.UsageError{Msg: "--batch is mutually exclusive with --review-id / --reply"}
	}
	if !in.BatchSet {
		if in.ReviewID == "" {
			return &exit.UsageError{Msg: "nothing to reply to: pass --review-id <id> --reply <text>, or --batch <file>"}
		}
		if in.Reply == "" {
			return &exit.UsageError{Msg: "--review-id requires --reply <text>"}
		}
	}

	pkg, err := rc.Package(in.Package)
	if err != nil {
		return err
	}

	var httpClient *http.Client
	if !in.DryRun {
		var err error
		httpClient, err = rc.AuthedClient()
		if err != nil {
			return err
		}
	}

	if in.BatchSet {
		return runBatch(rc, pkg, in, httpClient)
	}
	return runSingle(rc, pkg, in.ReviewID, in.Reply, in.DryRun, httpClient)
}

func runSingle(rc *kernel.RunContext, pkg, reviewID, text string, dryRun bool, hc *http.Client) error {
	warnIfLong(rc, reviewID, text)
	if dryRun {
		rc.Logf("DRY-RUN would reply on %s", reviewID)
		return nil
	}
	raw, err := reviews.Reply(rc.Ctx, hc, pkg, reviewID, text)
	if err != nil {
		return reviewerr.ClassifyReply(pkg, reviewID, err)
	}
	rc.Logf("Reply posted on %s", reviewID)
	if rc.Format == output.FormatJSON {
		return writeRaw(rc.Stdout, raw)
	}
	return nil
}

func warnIfLong(rc *kernel.RunContext, reviewID, text string) {
	if n := utf8.RuneCountInString(text); n > reviews.MaxReplyChars {
		rc.Warnf("reply on %s is %d characters, more than the %d Play documents: Play may reject it",
			reviewID, n, reviews.MaxReplyChars)
	}
}

type rowResult struct {
	ReviewID string `json:"reviewId"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type batchError struct {
	code   int
	failed int
	total  int
}

func (e *batchError) Error() string {
	return fmt.Sprintf("%d of %d batch replies failed", e.failed, e.total)
}

func (e *batchError) ExitCode() int { return e.code }

func runBatch(rc *kernel.RunContext, pkg string, in Input, hc *http.Client) error {
	src, closeFn, err := batchSource(rc, in.Batch)
	if err != nil {
		return err
	}
	defer closeFn()

	lines := batch.Parse(src)
	if len(lines) == 0 {
		return &exit.UsageError{Msg: "batch is empty: no <review-id>\\t<reply> rows found"}
	}

	worst := 0
	failed := 0
	results := make([]rowResult, 0, len(lines))

	for _, ln := range lines {
		if ln.Err != nil {
			rc.Failf("ERR line %d: %v", ln.Num, ln.Err)
			results = append(results, rowResult{Status: "error", Error: ln.Err.Error()})
			worst = max(worst, 2)
			failed++
			continue
		}
		id := ln.Record.ReviewID
		warnIfLong(rc, id, ln.Record.Reply)
		if in.DryRun {
			rc.Logf("DRY-RUN would reply on %s", id)
			results = append(results, rowResult{ReviewID: id, Status: "planned"})
			continue
		}
		if _, err := reviews.Reply(rc.Ctx, hc, pkg, id, ln.Record.Reply); err != nil {
			cerr := reviewerr.ClassifyReply(pkg, id, err)
			rc.Failf("ERR %s %s", id, cerr.Error())
			results = append(results, rowResult{ReviewID: id, Status: "error", Error: cerr.Error()})
			worst = max(worst, exit.For(cerr))
			failed++
			continue
		}
		rc.Logf("OK %s", id)
		results = append(results, rowResult{ReviewID: id, Status: "ok"})
	}

	if rc.Format == output.FormatJSON {
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

func batchSource(rc *kernel.RunContext, path string) (io.Reader, func(), error) {
	if path == "-" {
		if rc.Stdin == nil {
			return nil, func() {}, &exit.UsageError{Msg: "--batch - reads the TSV from stdin, but stdin is not available"}
		}
		return rc.Stdin, func() {}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, func() {}, &exit.UsageError{Msg: "cannot read --batch file: " + err.Error()}
	}
	return f, func() { _ = f.Close() }, nil
}

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

func NewCommand(boot kernel.Boot) *cobra.Command {
	var (
		outputFlag string
		in         Input
	)
	cmd := &cobra.Command{
		Use:   "reply",
		Short: "Post a developer reply to a review (single or --batch)",
		Long: `Post developer responses to user reviews, one at a time (--review-id
with --reply) or in a batch (--batch with a TSV file, or - for stdin).

The batch stream is TSV: one <review-id>\t<reply text> line per reply. Blank
lines and lines starting with # are skipped; a reply containing tabs or
newlines must be double-quoted (RFC 4180 quoting). Each line is posted
sequentially; a per-line failure is reported on stderr and does not abort the
rest. The process exits non-zero with the highest exit code seen across rows.

--review-id/--reply and --batch are mutually exclusive. --dry-run parses the
input and prints the planned actions without calling the API. --output json
echoes the API response (single) or a {"results":[...]} envelope (batch).`,
		Example: `  # Reply to one review
  gplay reviews reply --review-id gp:AOqpTOGx1bY2kLm --reply "Thanks! Fixed in 2.4."

  # Rehearse a batch: one <review-id><TAB><reply> line per reply
  gplay reviews reply --batch replies.tsv --dry-run

  # Post the batch from stdin
  gplay reviews reply --batch - < replies.tsv`,
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
