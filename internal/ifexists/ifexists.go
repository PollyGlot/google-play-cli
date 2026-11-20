package ifexists

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

const Name = "if-exists"

type Policy string

const (
	Fail   Policy = "fail"
	Skip   Policy = "skip"
	Update Policy = "update"
)

var usage = map[Policy]string{
	Skip:   "skip (read it first; if present, create nothing and print the existing %s)",
	Update: "update (read it first; if present, apply this command's settings to the existing %s)",
	Fail:   "fail (send the create and surface the API error, the default)",
}

func Register(cmd *cobra.Command, p *Policy, resource string, allowed ...Policy) {
	*p = Fail
	parts := make([]string, 0, len(allowed))
	for _, a := range allowed {
		parts = append(parts, strings.ReplaceAll(usage[a], "%s", resource))
	}
	cmd.Flags().Var(&value{p: p, allowed: allowed}, Name,
		"what to do when the "+resource+" already exists: "+joinOr(parts))
	kernel.ExperimentalFlag(cmd, Name)
}

func Check(p Policy, allowed ...Policy) (Policy, error) {
	if p == "" {
		return Fail, nil
	}
	if !offered(p, allowed) {
		return "", exit.Usagef("invalid --%s %q: %s", Name, string(p), mustBe(allowed))
	}
	return p, nil
}

func offered(p Policy, allowed []Policy) bool {
	for _, a := range allowed {
		if a == p {
			return true
		}
	}
	return false
}

func mustBe(allowed []Policy) string {
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return "must be " + joinOr(names)
}

func joinOr(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

type value struct {
	p       *Policy
	allowed []Policy
}

func (v *value) String() string {
	if v.p == nil {
		return string(Fail)
	}
	return string(*v.p)
}

func (v *value) Set(s string) error {
	p := Policy(s)
	if !offered(p, v.allowed) {
		return exit.Usagef("%s", mustBe(v.allowed))
	}
	*v.p = p
	return nil
}

func (v *value) Type() string {
	names := make([]string, len(v.allowed))
	for i, a := range v.allowed {
		names[i] = string(a)
	}
	return strings.Join(names, "|")
}
