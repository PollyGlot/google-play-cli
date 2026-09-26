package apply_test

import (
	"encoding/json"
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/metadata/apply"
	"github.com/PollyGlot/google-play-cli/internal/metadata/diff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/listing"
	"github.com/PollyGlot/google-play-cli/internal/metadata/orchestrator"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
)

// listingOf builds one Listing; a "" value is a managed-empty file (clear).
func listingOf(locale string, fields map[listing.Field]string) listing.Listing {
	l := listing.NewListing(locale)
	for f, v := range fields {
		l.Set(f, v)
	}
	return l
}

// trees returns a local/online pair that yields every diff op: an update and
// a clear on en-US, a create on a new fr-FR, an unchanged title, and a de-DE
// only on Play. Texts carry < > & because store copy does.
func trees() (local, online listing.Tree) {
	local = listing.Tree{
		"en-US": listingOf("en-US", map[listing.Field]string{
			listing.Title:            "Example App",
			listing.ShortDescription: "Track habits <fast> & free",
			listing.Video:            "",
		}),
		"fr-FR": listingOf("fr-FR", map[listing.Field]string{listing.Title: "Mon Appli"}),
	}
	online = listing.Tree{
		"en-US": listingOf("en-US", map[listing.Field]string{
			listing.Title:            "Example App",
			listing.ShortDescription: "Track habits",
			listing.Video:            "https://www.youtube.com/watch?v=example",
		}),
		"de-DE": listingOf("de-DE", map[listing.Field]string{listing.Title: "Beispiel-App"}),
	}
	return local, online
}

// TestRenderJSON_dryRun_golden freezes the ADR-0011 diff schema computed by
// the real diff engine: field ops with char counts, and the Play-only locale
// reported as untouchedLocale under the additive default.
func TestRenderJSON_dryRun_golden(t *testing.T) {
	local, online := trees()
	r := &orchestrator.Result{Package: "com.example.app", DryRun: true, Diff: diff.Compute("com.example.app", local, online, false)}
	outputtest.GoldenJSON(t, "dry_run.json.golden", apply.Payload{Result: r})
}

// TestRenderJSON_dryRunPrune_golden freezes the --prune variant: the same
// Play-only locale now becomes a delete record with its reason.
func TestRenderJSON_dryRunPrune_golden(t *testing.T) {
	local, online := trees()
	r := &orchestrator.Result{Package: "com.example.app", DryRun: true, Diff: diff.Compute("com.example.app", local, online, true)}
	outputtest.GoldenJSON(t, "dry_run_prune.json.golden", apply.Payload{Result: r})
}

// TestRenderJSON_applied_golden freezes the real-apply envelope gplay builds:
// a locale-keyed object of the write bodies it sent, plus the {"pruned":true}
// marker it authors for a deleted locale.
func TestRenderJSON_applied_golden(t *testing.T) {
	r := &orchestrator.Result{
		Package: "com.example.app",
		Patched: map[string]json.RawMessage{
			"en-US": json.RawMessage(`{"language":"en-US","shortDescription":"Track habits <fast> & free","video":""}`),
			"fr-FR": json.RawMessage(`{"language":"fr-FR","title":"Mon Appli"}`),
		},
		Created: []string{"fr-FR"},
		Pruned:  []string{"de-DE"},
	}
	outputtest.GoldenJSON(t, "applied.json.golden", apply.Payload{Result: r})
}
