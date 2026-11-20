package kernel

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/PollyGlot/google-play-cli/internal/exit"
)

const (
	RemovedCommand = "command"
	RemovedFlag    = "flag"
)

type Removal struct {
	Kind      string
	Path      string
	Token     string
	Use       string
	UseArgs   string
	RemovedIn string
	Note      string
}

var removedSurface = []Removal{
	{Kind: RemovedCommand, Path: "apps", Token: "info", Use: "apps view", RemovedIn: "0.4.1"},
	{Kind: RemovedCommand, Path: "tracks", Token: "status", Use: "tracks view", RemovedIn: "0.4.1"},
	{Kind: RemovedCommand, Path: "team grants", Token: "revoke", Use: "team grants remove", RemovedIn: "0.4.1"},

	{Kind: RemovedCommand, Path: "games achievements", Token: "update", Use: "games achievements set", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "games achievements", Token: "delete", Use: "games achievements remove", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "games leaderboards", Token: "update", Use: "games leaderboards set", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "games leaderboards", Token: "delete", Use: "games leaderboards remove", RemovedIn: "2.0.0"},
	{
		Kind: RemovedCommand, Path: "appstore", Token: "update", Use: "appstore submit", RemovedIn: "2.0.0",
		Note: "submit is the domain verb for sending the app to review, not a renamed update",
	},
	{
		Kind: RemovedCommand, Path: "appstore publish-status", Token: "published", Use: "appstore publish-status set", UseArgs: "published", RemovedIn: "2.0.0",
		Note: "the state is now the argument of set",
	},
	{
		Kind: RemovedCommand, Path: "appstore publish-status", Token: "unpublished", Use: "appstore publish-status set", UseArgs: "unpublished", RemovedIn: "2.0.0",
		Note: "the state is now the argument of set",
	},
	{Kind: RemovedCommand, Path: "appstore", Token: "upload apk", Use: "appstore apk upload", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "appstore", Token: "upload image", Use: "appstore image upload", RemovedIn: "2.0.0"},
	{Kind: RemovedCommand, Path: "appstore", Token: "upload policy", Use: "appstore policy upload", RemovedIn: "2.0.0"},

	{Kind: RemovedFlag, Path: "releases rollout", Token: "--to", Use: "--staged", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "vitals anr", Token: "--version", Use: "--version-code", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "vitals crashes", Token: "--version", Use: "--version-code", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "vitals errors counts", Token: "--version", Use: "--version-code", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "vitals excessivewakeup", Token: "--version", Use: "--version-code", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "vitals lmk", Token: "--version", Use: "--version-code", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "vitals slowrendering", Token: "--version", Use: "--version-code", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "vitals slowstart", Token: "--version", Use: "--version-code", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "vitals stuckbgwakelock", Token: "--version", Use: "--version-code", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "metadata images apply", Token: "--no-validate", Use: "--skip-preflight", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "games achievements list", Token: "--max-results", Use: "--page-size", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "games achievements create", Token: "--from-json", Use: "--file", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "games achievements set", Token: "--from-json", Use: "--file", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "games leaderboards list", Token: "--max-results", Use: "--page-size", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "games leaderboards create", Token: "--from-json", Use: "--file", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "games leaderboards set", Token: "--from-json", Use: "--file", RemovedIn: "2.0.0"},
	{
		Kind: RemovedFlag, Path: "subscriptions prices migrate", Token: "--region", Use: "--regions", RemovedIn: "2.0.0",
		Note: "--regions takes a comma-separated list or a repeated flag: `--regions US,CA`",
	},
	{Kind: RemovedFlag, Path: "releases artifacts list", Token: "--kind", Use: "--format", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "appstore catalog events list", Token: "--start-time", Use: "--since", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "appstore catalog events list", Token: "--end-time", Use: "--until", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "reviews history", Token: "--from", Use: "--since", RemovedIn: "2.0.0"},
	{Kind: RemovedFlag, Path: "reviews history", Token: "--to", Use: "--until", RemovedIn: "2.0.0"},
}

func RemovedSurface() []Removal {
	return append([]Removal(nil), removedSurface...)
}

func tablePath(cmd *cobra.Command) string {
	return strings.TrimPrefix(strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()), " ")
}

func removedVerb(group *cobra.Command, args []string) (Removal, bool) {
	path := tablePath(group)
	for _, r := range removedSurface {
		if r.Kind != RemovedCommand || r.Path != path {
			continue
		}
		words := strings.Fields(r.Token)
		if len(args) >= len(words) && strings.Join(args[:len(words)], " ") == r.Token {
			return r, true
		}
	}
	return Removal{}, false
}

func removedVerbError(group *cobra.Command, r Removal) *exit.UsageError {
	root := group.Root().Name()
	use := strings.Join(append([]string{root, r.Use}, strings.Fields(r.UseArgs)...), " ")
	msg := fmt.Sprintf("unknown command %q for %q: removed in %s, use `%s`",
		strings.Fields(r.Token)[0], group.CommandPath(), r.RemovedIn, use)
	if r.Note != "" {
		msg += " (" + r.Note + ")"
	}
	return &exit.UsageError{
		Msg: msg,
		Replacement: &exit.Replacement{
			Kind:      RemovedCommand,
			Removed:   group.CommandPath() + " " + r.Token,
			Use:       use,
			RemovedIn: r.RemovedIn,
			Note:      r.Note,
		},
	}
}

func FlagErrorFunc(cmd *cobra.Command, err error) error {
	return flagError(cmd, err, nil)
}

func SetArgs(root *cobra.Command, argv []string) {
	root.SetArgs(argv)
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return flagError(cmd, err, argv)
	})
}

func flagError(cmd *cobra.Command, err error, argv []string) error {
	if cmd.HasSubCommands() {
		args := cmd.Flags().Args()
		if argv != nil {
			args = positionalsAfter(cmd, argv)
		}
		if r, ok := removedVerb(cmd, args); ok {
			return removedVerbError(cmd, r)
		}
	}
	if r, ok := removedFlag(cmd, err); ok {
		return removedFlagError(cmd, err, r)
	}
	return exit.Usagef("%s", err)
}

func positionalsAfter(cmd *cobra.Command, argv []string) []string {
	flags := cmd.Flags()
	var pos []string
	for i := 0; i < len(argv); i++ {
		s := argv[i]
		switch {
		case s == "--":
			pos = append(pos, argv[i+1:]...)
			i = len(argv)
		case strings.HasPrefix(s, "--") && !strings.Contains(s, "="):
			if f := flags.Lookup(s[2:]); f == nil || f.NoOptDefVal == "" {
				i++
			}
		case len(s) == 2 && s[0] == '-':
			if f := flags.ShorthandLookup(s[1:]); f == nil || f.NoOptDefVal == "" {
				i++
			}
		case s == "-":
			pos = append(pos, s)
		case s == "" || strings.HasPrefix(s, "-"):
		default:
			pos = append(pos, s)
		}
	}
	depth := len(strings.Fields(tablePath(cmd)))
	if len(pos) < depth {
		return nil
	}
	return pos[depth:]
}

func removedFlag(cmd *cobra.Command, err error) (Removal, bool) {
	var unknown *pflag.NotExistError
	if !errors.As(err, &unknown) || unknown.GetSpecifiedShortnames() != "" {
		return Removal{}, false
	}
	path := tablePath(cmd)
	token := "--" + unknown.GetSpecifiedName()
	for _, r := range removedSurface {
		if r.Kind == RemovedFlag && r.Path == path && r.Token == token {
			return r, true
		}
	}
	return Removal{}, false
}

func removedFlagError(cmd *cobra.Command, err error, r Removal) *exit.UsageError {
	msg := fmt.Sprintf("%s: removed in %s, use `%s %s`", err, r.RemovedIn, cmd.CommandPath(), r.Use)
	if r.Note != "" {
		msg += " (" + r.Note + ")"
	}
	return &exit.UsageError{
		Msg: msg,
		Replacement: &exit.Replacement{
			Kind:      RemovedFlag,
			Removed:   r.Token,
			Use:       r.Use,
			RemovedIn: r.RemovedIn,
			Note:      r.Note,
		},
	}
}
