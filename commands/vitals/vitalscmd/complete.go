package vitalscmd

import (
	"context"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/play/vitals"
)

// VersionCodeFlag is the flag CompleteVersionCodes is registered on.
const VersionCodeFlag = "version-code"

// completionTimeout bounds the whole completion (token exchange included): a
// TAB press that hangs is worse than one that suggests nothing. A var only so
// the test can shorten it.
var completionTimeout = 3 * time.Second

// RegisterVersionCodeCompletion wires CompleteVersionCodes on cmd's
// --version-code flag. Every vitals leaf carrying that flag calls it, so the
// suggestion source stays the one `vitals releases` lists (#348).
func RegisterVersionCodeCompletion(cmd *cobra.Command, boot kernel.Boot) {
	// The only error is "no such flag", a programmer error that the test
	// building the command tree turns into a failure.
	if err := cmd.RegisterFlagCompletionFunc(VersionCodeFlag, CompleteVersionCodes(boot)); err != nil {
		panic("vitalscmd: " + err.Error())
	}
}

// CompleteVersionCodes suggests the version codes Play holds vitals data for
// (apps.fetchReleaseFilterOptions on the --package, or the Project pin),
// newest first, each described by the release and track serving it.
//
// It is best effort by contract: the user did not ask for a network call, they
// pressed TAB. Any failure (no package, no credential, an API refusal, a
// malformed answer, the timeout) yields no suggestion, never an error
// directive, and nothing reaches the terminal: stdout and stderr of the run
// are discarded, since cobra's completion protocol owns stdout.
func CompleteVersionCodes(boot kernel.Boot) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		const directive = cobra.ShellCompDirectiveNoFileComp
		parent := cmd.Context()
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, completionTimeout)
		defer cancel()

		pkgFlag, _ := cmd.Flags().GetString("package")
		in := kernel.FromCobra(cmd, string(output.FormatJSON))
		in.Ctx = ctx
		in.Timeout = completionTimeout
		in.Retry = 0
		in.Verbose = false
		boot.Stdout, boot.Stderr, boot.Stdin = io.Discard, io.Discard, nil

		var rows []vitals.Release
		err := kernel.Run(boot, in, func(rc *kernel.RunContext) (output.Renderable, error) {
			pkg, err := rc.Package(pkgFlag)
			if err != nil {
				return nil, err
			}
			hc, err := rc.AuthedClient()
			if err != nil {
				return nil, err
			}
			raw, err := vitals.FetchReleaseFilterOptions(rc.Ctx, hc, pkg)
			if err != nil {
				return nil, err
			}
			rows, err = vitals.ParseReleases(raw)
			return nil, err
		})
		if err != nil {
			return nil, directive
		}
		return versionCodeCompletions(rows, toComplete), directive
	}
}

// versionCodeCompletions turns release rows into completions: one per distinct
// version code starting with toComplete, newest (highest) first, described by
// the first release serving it in API order.
func versionCodeCompletions(rows []vitals.Release, toComplete string) []cobra.Completion {
	desc := map[string]string{}
	var codes []string
	for _, r := range rows {
		for _, code := range r.VersionCodes {
			if _, seen := desc[code]; seen || !strings.HasPrefix(code, toComplete) {
				continue
			}
			desc[code] = r.Release + " (" + r.Track + ")"
			codes = append(codes, code)
		}
	}
	sort.SliceStable(codes, func(i, j int) bool {
		a, errA := strconv.ParseInt(codes[i], 10, 64)
		b, errB := strconv.ParseInt(codes[j], 10, 64)
		if errA != nil || errB != nil {
			return codes[i] > codes[j]
		}
		return a > b
	})
	out := make([]cobra.Completion, 0, len(codes))
	for _, code := range codes {
		out = append(out, cobra.CompletionWithDesc(code, desc[code]))
	}
	return out
}
