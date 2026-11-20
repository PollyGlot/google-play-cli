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

const VersionCodeFlag = "version-code"

var completionTimeout = 3 * time.Second

func RegisterVersionCodeCompletion(cmd *cobra.Command, boot kernel.Boot) {
	if err := cmd.RegisterFlagCompletionFunc(VersionCodeFlag, CompleteVersionCodes(boot)); err != nil {
		panic("vitalscmd: " + err.Error())
	}
}

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
