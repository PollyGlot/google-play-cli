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

func listingOf(locale string, fields map[listing.Field]string) listing.Listing {
	l := listing.NewListing(locale)
	for f, v := range fields {
		l.Set(f, v)
	}
	return l
}

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

func TestRenderJSON_dryRun_golden(t *testing.T) {
	local, online := trees()
	r := &orchestrator.Result{Package: "com.example.app", DryRun: true, Diff: diff.Compute("com.example.app", local, online, false)}
	outputtest.GoldenJSON(t, "dry_run.json.golden", apply.Payload{Result: r})
}

func TestRenderJSON_dryRunPrune_golden(t *testing.T) {
	local, online := trees()
	r := &orchestrator.Result{Package: "com.example.app", DryRun: true, Diff: diff.Compute("com.example.app", local, online, true)}
	outputtest.GoldenJSON(t, "dry_run_prune.json.golden", apply.Payload{Result: r})
}

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
