package generatedcmd_test

import (
	"testing"

	"github.com/PollyGlot/google-play-cli/commands/releases/generated/generatedcmd"
	"github.com/PollyGlot/google-play-cli/internal/play/generatedapks"
)

// TestBuildRows_flattensEveryArtifact asserts the grouped-by-signing-key envelope
// flattens to one row per downloadable artifact, with the right type, the natural
// secondary id, the downloadId, and a short cert hash: including the unprotected
// split/standalone lists (they carry downloadIds too).
func TestBuildRows_flattensEveryArtifact(t *testing.T) {
	lr := generatedapks.ListResponse{
		GeneratedApks: []generatedapks.PerSigningKey{{
			CertificateSha256Hash:    "0123456789abcdefDEADBEEF",
			GeneratedSplitApks:       []generatedapks.SplitApk{{DownloadID: "d-split", ModuleName: "base", SplitID: "config.xxhdpi"}},
			GeneratedStandaloneApks:  []generatedapks.StandaloneApk{{DownloadID: "d-stand", VariantID: 5}},
			GeneratedUniversalApk:    &generatedapks.UniversalApk{DownloadID: "d-univ"},
			GeneratedAssetPackSlices: []generatedapks.AssetPackSlice{{DownloadID: "d-asset", ModuleName: "assets", SliceID: "slice-1"}},
			GeneratedRecoveryModules: []generatedapks.RecoveryApk{{DownloadID: "d-rec", ModuleName: "base", RecoveryID: "99"}},
			UnprotectedGeneratedSplitApks: []generatedapks.SplitApk{
				{DownloadID: "d-usplit", ModuleName: "base", SplitID: "config.arm64"},
			},
			UnprotectedGeneratedStandaloneApks: []generatedapks.StandaloneApk{{DownloadID: "d-ustand", VariantID: 6}},
		}},
	}

	rows := generatedcmd.BuildRows(lr)
	if len(rows) != 7 {
		t.Fatalf("rows = %d, want 7 (2 split + 2 standalone + 1 universal + 1 asset + 1 recovery)", len(rows))
	}

	byDownload := map[string]generatedcmd.Row{}
	for _, r := range rows {
		byDownload[r.DownloadID] = r
		if r.Cert != "0123456789ab…" {
			t.Errorf("row %q cert = %q, want short hash", r.DownloadID, r.Cert)
		}
	}

	cases := []struct {
		dl, typ, module, id string
	}{
		{"d-split", "split", "base", "config.xxhdpi"},
		{"d-usplit", "split", "base", "config.arm64"},
		{"d-stand", "standalone", "", "5"},
		{"d-ustand", "standalone", "", "6"},
		{"d-univ", "universal", "", ""},
		{"d-asset", "asset-slice", "assets", "slice-1"},
		{"d-rec", "recovery", "base", "99"},
	}
	for _, c := range cases {
		r, ok := byDownload[c.dl]
		if !ok {
			t.Errorf("missing row for downloadId %q", c.dl)
			continue
		}
		if r.Type != c.typ || r.Module != c.module || r.ID != c.id {
			t.Errorf("row %q = {type:%q module:%q id:%q}, want {type:%q module:%q id:%q}", c.dl, r.Type, r.Module, r.ID, c.typ, c.module, c.id)
		}
	}
}

// TestBuildRows_empty returns no rows for an empty response (no panic).
func TestBuildRows_empty(t *testing.T) {
	if rows := generatedcmd.BuildRows(generatedapks.ListResponse{}); len(rows) != 0 {
		t.Errorf("rows = %d, want 0", len(rows))
	}
}
