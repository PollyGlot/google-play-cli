// Package expansionfilescmd holds the wiring shared by the `releases
// expansion-files` leaves (upload/set/view): --type validation and a shared
// ExpansionFile renderer. The leaves orchestrate the
// Edit lifecycle (edits.WithEdit / WithReadOnlyEdit) themselves.
package expansionfilescmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/play/expansionfiles"
)

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
		return "", &exit.UsageError{Msg: "--type must be main or patch"}
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
