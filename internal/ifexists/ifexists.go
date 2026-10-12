// Package ifexists binds the --if-exists flag of the creates that can tell
// "already exists" apart reliably: those whose identity the caller supplies
// and whose resource has a dependable read (ADR-0050). One registration keeps
// the name, the enum, the validation and the help text identical across every
// adopter; each command only chooses which policies it offers.
//
// The flag is Experimental on otherwise frozen leaves (ADR-0010/0042): its
// value set can still grow without a major.
package ifexists

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
)

// Name is the flag name, exported so tests and hints spell it once.
const Name = "if-exists"

// Policy is what a create does when its resource already exists.
type Policy string

const (
	// Fail sends the create as before the flag existed and surfaces the API
	// error: the default, and the only policy that adds no read.
	Fail Policy = "fail"
	// Skip reads the resource first and, when it exists, writes nothing and
	// prints the existing resource as the API returned it.
	Skip Policy = "skip"
	// Update reads the resource first and, when it exists, runs the command's
	// own update path. Offered only where such a path already exists.
	Update Policy = "update"
)

// usage describes each policy in the help text. %s is the resource noun.
var usage = map[Policy]string{
	Skip:   "skip (read it first; if present, create nothing and print the existing %s)",
	Update: "update (read it first; if present, apply this command's settings to the existing %s)",
	Fail:   "fail (send the create and surface the API error, the default)",
}

// Register declares --if-exists on cmd, bound to p, offering only allowed (in
// help order), and classifies the flag Experimental. resource is the GLOSSARY
// noun the help text names ("track", "user"). p starts at Fail.
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

// Check validates a policy for a direct caller of a command's Run, which
// bypasses flag parsing: the zero value means Fail, and a policy the command
// does not offer is CLI misuse (exit 2), refused before any HTTP.
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

// mustBe names the allowed values: "must be skip or fail".
func mustBe(allowed []Policy) string {
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return "must be " + joinOr(names)
}

// joinOr renders "a", "a or b", "a, b or c".
func joinOr(items []string) string {
	if len(items) <= 1 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// value validates --if-exists while flags are parsed, so a bad or
// unoffered value is exit 2 before auth, before any HTTP and on a --dry-run
// too, without a check in each command.
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

// Type names the command's own allowed values, so help renders
// `--if-exists skip|fail`. It is deliberately not "string": the flag-type
// registry test in cmd/gplay samples every "string" flag with an arbitrary
// value, which this one rightly refuses.
func (v *value) Type() string {
	names := make([]string, len(v.allowed))
	for i, a := range v.allowed {
		names[i] = string(a)
	}
	return strings.Join(names, "|")
}
