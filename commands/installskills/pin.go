package installskills

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

//go:embed skills-pin.json
var pinJSON []byte

type Pin struct {
	Repo   string   `json:"repo"`
	URL    string   `json:"url"`
	Commit string   `json:"commit"`
	Subdir string   `json:"subdir"`
	Skills []string `json:"skills"`
}

var fullCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

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
