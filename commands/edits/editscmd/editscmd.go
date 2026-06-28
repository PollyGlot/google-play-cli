// Package editscmd holds the wiring shared by the four `gplay edits` leaves
// (begin/commit/discard/status): package resolution, locating the project's
// .gplay/ pin directory, the operator-facing error shapes (CLI misuse → exit 2,
// no-open-Edit / already-open state → exit 60), and the small renderable the
// leaves return. Keeping it here mirrors commands/device-tiers/devicetierscmd
// and keeps the leaves thin glue over internal/play/edits + internal/editpin.
package editscmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/output"
)

// usageError is CLI misuse (exit 2): a missing package or an uninitialised
// project.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Usagef builds a CLI-misuse error (exit 2) for the leaves to share.
func Usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// NoOpenEditError is returned by `edits commit`/`discard` when there is no
// pinned Edit to act on. It maps to exit 60 (state conflict, recoverable): the
// fix is `gplay edits begin`.
type NoOpenEditError struct{ Package string }

func (e *NoOpenEditError) Error() string {
	return fmt.Sprintf("no open explicit edit for %s — run `gplay edits begin --package %s` first (nothing to commit or discard)", e.Package, e.Package)
}
func (e *NoOpenEditError) ExitCode() int { return 60 }

// AlreadyOpenError is returned by `edits begin` when a pin already exists: a
// second begin would orphan the first Edit. Exit 60 (state conflict); the fix
// is to commit or discard the open one first.
type AlreadyOpenError struct {
	Package string
	EditID  string
}

func (e *AlreadyOpenError) Error() string {
	return fmt.Sprintf("an explicit edit is already open for %s (%s) — run `gplay edits commit` or `gplay edits discard` first", e.Package, e.EditID)
}
func (e *AlreadyOpenError) ExitCode() int { return 60 }

// ResolvePackage resolves the target package: --package wins, else the project
// pin. An empty result is a usage error (exit 2).
func ResolvePackage(rc *kernel.RunContext, flag string) (string, error) {
	pkg := strings.TrimSpace(flag)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", Usagef("no package — pass --package <pkg> or run gplay init in your repo")
	}
	return pkg, nil
}

// RequireGplayDir returns the project's .gplay/ directory, or a usage error
// (exit 2) when no project was found. Explicit edits live in the project's
// .gplay/ (gitignored for edit-*.json), so they require an initialised
// project — the error names `gplay init`.
func RequireGplayDir(rc *kernel.RunContext) (string, error) {
	dir, ok := rc.GplayDir()
	if !ok {
		return "", Usagef("no project found — run `gplay init --package <pkg>` first (explicit edits are stored in the project's .gplay/)")
	}
	return dir, nil
}

// Payload is the renderable the edits leaves return: the package, the Edit ID
// acted on (or the open one, for status), whether an Edit is open, and the past
// action (began/committed/discarded; empty for status). --output json emits it
// as a small gplay envelope — these are local-pin operations, not an API
// pass-through, so there is no upstream body to mirror.
type Payload struct {
	Package string `json:"package"`
	EditID  string `json:"editId,omitempty"`
	Open    bool   `json:"open"`
	Action  string `json:"action,omitempty"`
}

func (p Payload) Renderers() output.Renderers {
	return output.Renderers{
		Table:    func(w io.Writer) error { return p.renderText(w, "") },
		JSON:     func(w io.Writer) error { return output.WriteJSON(w, p) },
		Markdown: func(w io.Writer) error { return p.renderText(w, "- ") },
	}
}

// renderText writes the one-line human/markdown view. prefix is "" for the
// plain table form and "- " for the markdown bullet.
func (p Payload) renderText(w io.Writer, prefix string) error {
	var line string
	switch {
	case p.Action != "":
		line = fmt.Sprintf("%s explicit edit %s for %s", p.Action, p.EditID, p.Package)
	case p.Open:
		line = fmt.Sprintf("open explicit edit %s for %s", p.EditID, p.Package)
	default:
		line = fmt.Sprintf("no open explicit edit for %s", p.Package)
	}
	_, err := fmt.Fprintf(w, "%s%s\n", prefix, line)
	return err
}
