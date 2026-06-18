// Package expansionfilescmd holds the wiring shared by the `releases
// expansion-files` leaves (upload/set/view): package resolution, --type
// validation, and a shared ExpansionFile renderer. The leaves orchestrate the
// Edit lifecycle (edits.WithEdit / WithReadOnlyEdit) themselves.
package expansionfilescmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
)

type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }
func (e *usageError) ExitCode() int { return 2 }

// Usagef builds a usage error (exit 2).
func Usagef(format string, a ...any) error { return &usageError{msg: fmt.Sprintf(format, a...)} }

// ResolvePackage resolves the target package: --package wins, else the project pin.
func ResolvePackage(rc *kernel.RunContext, flag string) (string, error) {
	pkg := strings.TrimSpace(flag)
	if pkg == "" && rc.Resolved != nil {
		pkg = strings.TrimSpace(rc.Resolved.Pin)
	}
	if pkg == "" {
		return "", &usageError{msg: "no package — pass --package <pkg> or run gplay init in your repo"}
	}
	return pkg, nil
}

// NormalizeType validates --type and returns the canonical segment. Empty
// defaults to main; anything but main|patch is CLI misuse (exit 2). The
// expansion 'patch' type is distinct from the HTTP PATCH method.
func NormalizeType(t string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "", expansionfiles.TypeMain:
		return expansionfiles.TypeMain, nil
	case expansionfiles.TypePatch:
		return expansionfiles.TypePatch, nil
	default:
		return "", &usageError{msg: "--type must be main or patch"}
	}
}

// RenderExpansionFile writes the human view: fileSize XOR referencesVersion
// (whichever the schema set), never a misleading "fileSize: 0".
func RenderExpansionFile(w io.Writer, versionCode int, fileType string, ef expansionfiles.ExpansionFile) error {
	if _, err := fmt.Fprintf(w, "versionCode:  %d\ntype:         %s\n", versionCode, fileType); err != nil {
		return err
	}
	if ef.HasFile() {
		_, err := fmt.Fprintf(w, "fileSize:     %s\n", ef.FileSize)
		return err
	}
	if ef.ReferencesVersion > 0 {
		_, err := fmt.Fprintf(w, "referencesVersion: %d\n", ef.ReferencesVersion)
		return err
	}
	return nil
}
