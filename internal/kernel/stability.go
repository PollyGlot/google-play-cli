package kernel

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const stabilityAnnotation = "gplay.stability"

const StabilityExperimental = "experimental"

const experimentalTag = "[experimental]"

const experimentalNotice = experimentalTag + " Outside the Public contract: flags, output and behavior\n" +
	"may change in any release, without a major version bump. Pin an exact\n" +
	"release if you depend on this in CI.\n" +
	"See https://gplay.sh/docs/concepts/stability/"

func Experimental(cmd *cobra.Command) *cobra.Command {
	if cmd == nil {
		return cmd
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[stabilityAnnotation] = StabilityExperimental
	tagExperimental(cmd)
	for _, sub := range cmd.Commands() {
		Experimental(sub)
	}
	return cmd
}

func tagExperimental(cmd *cobra.Command) {
	if !strings.HasPrefix(cmd.Short, experimentalTag) {
		cmd.Short = strings.TrimSpace(experimentalTag + " " + cmd.Short)
	}
	if strings.HasPrefix(cmd.Long, experimentalNotice) {
		return
	}
	body := cmd.Long
	if body == "" {
		body = strings.TrimSpace(strings.TrimPrefix(cmd.Short, experimentalTag))
	}
	cmd.Long = experimentalNotice + "\n\n" + body
}

func ExperimentalFlag(cmd *cobra.Command, name string) *cobra.Command {
	f := cmd.Flags().Lookup(name)
	if f == nil {
		panic("kernel.ExperimentalFlag: " + cmd.CommandPath() + " has no --" + name + " flag")
	}
	if f.Annotations == nil {
		f.Annotations = map[string][]string{}
	}
	f.Annotations[stabilityAnnotation] = []string{StabilityExperimental}
	if !strings.HasPrefix(f.Usage, experimentalTag) {
		f.Usage = experimentalTag + " " + f.Usage
	}
	return cmd
}

func IsExperimentalFlag(cmd *cobra.Command, f *pflag.Flag) bool {
	if IsExperimental(cmd) {
		return true
	}
	v := f.Annotations[stabilityAnnotation]
	return len(v) == 1 && v[0] == StabilityExperimental
}

func IsExperimental(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Annotations[stabilityAnnotation] == StabilityExperimental {
			return true
		}
	}
	return false
}
