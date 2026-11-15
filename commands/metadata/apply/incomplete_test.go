package apply_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/metadata/apply"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
)

// TestRun_createdLocaleWithoutTitle_warns: apply is additive, so a required
// field missing from the tree is not a refusal, but a locale the apply CREATES
// has no online value to keep: it goes out without a title, and stderr says
// so, on a real apply and on its preview. A complete new locale, and a live
// locale missing a field (its online value stays), are not flagged.
func TestRun_createdLocaleWithoutTitle_warns(t *testing.T) {
	dir := writeTree(t, listing.Tree{
		"en-US": ml("en-US", "short", "S"),
		"de-DE": ml("de-DE", "short", "Kurz", "full", "Lang"),
		"fr-FR": ml("fr-FR", "title", "Mon app", "full", "Long"),
	})
	for _, dryRun := range []bool{true, false} {
		rt := newFake(t, applyRT{editID: "e",
			listingsBody: `{"listings":[{"language":"en-US","title":"T","fullDescription":"F"}]}`})
		rc := newRC(t, rt)
		var stderr bytes.Buffer
		rc.Stderr = &stderr

		if _, err := apply.Run(rc, apply.Input{Package: "com.x", Dir: dir, DryRun: dryRun, Confirm: !dryRun}); err != nil {
			t.Fatalf("dryRun=%v: Run: %v", dryRun, err)
		}
		var warns []string
		for _, line := range strings.Split(stderr.String(), "\n") {
			if strings.Contains(line, "without") {
				warns = append(warns, line)
			}
		}
		if len(warns) != 1 {
			t.Fatalf("dryRun=%v: want one warning, for de-DE only; stderr:\n%s", dryRun, stderr.String())
		}
		for _, want := range []string{`"de-DE"`, "without title", "title.txt", filepath.Join(dir, "de-DE")} {
			if !strings.Contains(warns[0], want) {
				t.Errorf("dryRun=%v: warning %q does not contain %q", dryRun, warns[0], want)
			}
		}
		if strings.Contains(warns[0], "fullDescription") {
			t.Errorf("dryRun=%v: de-DE has its full description, warning %q says otherwise", dryRun, warns[0])
		}
	}
}
