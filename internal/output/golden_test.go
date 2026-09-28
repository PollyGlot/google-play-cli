package output_test

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/output"
	"github.com/PollyGlot/google-play-cli/internal/output/outputtest"
	"github.com/PollyGlot/google-play-cli/internal/play/api"
)

// jsonOnly adapts a WriteJSON call to output.Renderable so GoldenJSON can
// drive it like a command payload.
type jsonOnly func(io.Writer) error

func (f jsonOnly) Renderers() output.Renderers { return output.Renderers{JSON: f} }

// TestWriteJSON_keepsHTMLCharactersLiteral pins COH-15: <, > and & reach
// stdout as themselves, in gplay-authored strings AND inside a passthrough
// json.RawMessage (which the encoder re-indents, and used to re-escape).
func TestWriteJSON_keepsHTMLCharactersLiteral(t *testing.T) {
	v := struct {
		Permission string          `json:"permission"`
		Hint       string          `json:"hint"`
		Raw        json.RawMessage `json:"raw"`
	}{
		Permission: "View app quality (Android vitals, crashes & ANRs)",
		Hint:       "pass --package <pkg>",
		Raw:        json.RawMessage(`{"text":"5 < 6 && 7 > 6"}`),
	}
	outputtest.GoldenJSON(t, "writejson_html.golden", jsonOnly(func(w io.Writer) error {
		return output.WriteJSON(w, v)
	}))
}

// TestWriteErrorEnvelope_golden freezes the envelope bytes (ADR-0023/0044): a
// usage error whose message quotes a placeholder, and an API failure carrying
// the fields a consumer branches on.
func TestWriteErrorEnvelope_golden(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"envelope_usage.golden", exit.Usagef("no package: pass --package <pkg> or run `gplay init`")},
		{"envelope_api.golden", &api.Error{
			Operation:  "edits.commit",
			Package:    "com.example.app",
			StatusCode: 409,
			Message:    "Edit already exists & is open",
			Reasons:    []string{"editAlreadyExists"},
		}},
		{"envelope_safety.golden", exit.SafetyFlag("confirm", "refusing to remove <user>: pass --confirm")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outputtest.GoldenJSON(t, tc.name, jsonOnly(func(w io.Writer) error {
				return output.WriteErrorEnvelope(w, tc.err)
			}))
		})
	}
}

// TestMarshal_keepsRawMessageVerbatim covers the envelope builders (merged
// vitals pages, team users, the apps view composite): json.Marshal would bake
// an escaped ampersand into the bytes before WriteJSON ever sees them.
func TestMarshal_keepsRawMessageVerbatim(t *testing.T) {
	got, err := output.Marshal(struct {
		Items []json.RawMessage `json:"items"`
	}{Items: []json.RawMessage{json.RawMessage(`{"cause":"NPE at <init> & onCreate"}`)}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"items":[{"cause":"NPE at <init> & onCreate"}]}`
	if string(got) != want {
		t.Errorf("Marshal = %s, want %s (compact, unescaped, no trailing newline)", got, want)
	}
}
