package installskills

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

// pinJSON is the reviewed supply-chain pin, baked into the binary at build time
// (ADR-0045). Bumping the skills pack is therefore a normal reviewed PR that
// edits this file: `commands/` counts as code, so it goes through the full gate.
//
//go:embed skills-pin.json
var pinJSON []byte

// Pin describes exactly what `install-skills` is allowed to install.
//
// Commit is the integrity anchor: git guarantees the fetched tree hashes to it,
// so no separate per-file digest table is needed (and none is kept: it would
// have to be regenerated on every pin bump, adding a second source of truth for
// the same fact). Skills is the completeness anchor: the checkout must contain
// exactly this set, no more and no less, or nothing is installed at all.
type Pin struct {
	// Repo is the `owner/name` slug, used in messages and browse URLs.
	Repo string `json:"repo"`
	// URL is the clone URL git fetches from.
	URL string `json:"url"`
	// Commit is the full 40-hex commit the checkout is pinned to. Never a
	// branch or a tag: both are mutable by whoever controls the remote.
	Commit string `json:"commit"`
	// Subdir is the directory inside the repo holding one directory per skill.
	Subdir string `json:"subdir"`
	// Skills is the expected pack, sorted, as directory names under Subdir.
	Skills []string `json:"skills"`
}

// fullCommit matches a complete, unabbreviated git object name. An abbreviated
// hash is rejected: it is a prefix match, so it does not pin a unique tree.
var fullCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

// embeddedPin is the parsed pinJSON. It is validated at first use rather than
// in an init() so a malformed pin surfaces as a normal command error.
func embeddedPin() (Pin, error) {
	var p Pin
	if err := json.Unmarshal(pinJSON, &p); err != nil {
		return Pin{}, fmt.Errorf("parse embedded skills pin: %w", err)
	}
	if err := p.validate(); err != nil {
		return Pin{}, fmt.Errorf("embedded skills pin: %w", err)
	}
	return p, nil
}

func (p Pin) validate() error {
	if p.Repo == "" || p.URL == "" || p.Subdir == "" {
		return fmt.Errorf("repo, url and subdir are required")
	}
	if !fullCommit.MatchString(p.Commit) {
		return fmt.Errorf("commit %q is not a full 40-character hash", p.Commit)
	}
	if len(p.Skills) == 0 {
		return fmt.Errorf("no skills listed")
	}
	seen := make(map[string]bool, len(p.Skills))
	for _, s := range p.Skills {
		// A skill name is a single path element: anything else would let the
		// pin (or a tampered checkout matched against it) escape the target
		// directory during the copy.
		if s == "" || s == "." || s == ".." || containsSeparator(s) {
			return fmt.Errorf("invalid skill name %q", s)
		}
		if seen[s] {
			return fmt.Errorf("duplicate skill name %q", s)
		}
		seen[s] = true
	}
	if !sort.StringsAreSorted(p.Skills) {
		return fmt.Errorf("skills must be sorted")
	}
	return nil
}

func containsSeparator(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' || s[i] == '\\' {
			return true
		}
	}
	return false
}
