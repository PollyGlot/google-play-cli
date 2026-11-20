package editscmd

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

type NoOpenEditError struct{ Package string }

func (e *NoOpenEditError) Error() string {
	return fmt.Sprintf("no open explicit edit for %s: run `gplay edits begin --package %s` first (nothing to commit or discard)", e.Package, e.Package)
}
func (e *NoOpenEditError) ExitCode() int { return 60 }

type AlreadyOpenError struct {
	Package string
	EditID  string
}

func (e *AlreadyOpenError) Error() string {
	return fmt.Sprintf("an explicit edit is already open for %s (%s): run `gplay edits commit` or `gplay edits discard` first", e.Package, e.EditID)
}
func (e *AlreadyOpenError) ExitCode() int { return 60 }

func RequireGplayDir(rc *kernel.RunContext) (string, error) {
	dir, ok := rc.GplayDir()
	if !ok {
		return "", exit.Usagef("no project found: run `gplay init --package <pkg>` first (explicit edits are stored in the project's .gplay/)")
	}
	return dir, nil
}

type Payload struct {
	Package           string `json:"package"`
	EditID            string `json:"editId,omitempty"`
	Open              bool   `json:"open"`
	Action            string `json:"action,omitempty"`
	Live              bool   `json:"live,omitempty"`
	ExpiryTimeSeconds string `json:"expiryTimeSeconds,omitempty"`
	Gone              bool   `json:"-"`
}

const ActionClearedUnreadable = "cleared-unreadable-pin"

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderText(w, "") },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return p.renderText(w, "- ") },
	}
}

func (p Payload) renderText(w io.Writer, prefix string) error {
	var line string
	switch {
	case p.Action == ActionClearedUnreadable:
		line = fmt.Sprintf("removed unreadable pin for %s (no edit discarded on Play)", p.Package)
	case p.Action != "":
		line = fmt.Sprintf("%s explicit edit %s for %s", p.Action, p.EditID, p.Package)
	case p.Gone:
		line = fmt.Sprintf("pinned explicit edit %s for %s no longer exists server-side (expired or discarded elsewhere): run `gplay edits discard --package %s` to clear the pin", p.EditID, p.Package, p.Package)
	case p.Open && p.Live:
		line = fmt.Sprintf("open explicit edit %s for %s (live, expires %s)", p.EditID, p.Package, expiryLabel(p.ExpiryTimeSeconds))
	case p.Open:
		line = fmt.Sprintf("open explicit edit %s for %s", p.EditID, p.Package)
	default:
		line = fmt.Sprintf("no open explicit edit for %s", p.Package)
	}
	_, err := fmt.Fprintf(w, "%s%s\n", prefix, line)
	return err
}

func expiryLabel(seconds string) string {
	secs, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil || secs <= 0 {
		if seconds == "" {
			return "(unknown)"
		}
		return seconds
	}
	return time.Unix(secs, 0).UTC().Format(time.RFC3339)
}
