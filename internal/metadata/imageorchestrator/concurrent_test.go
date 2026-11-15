package imageorchestrator_test

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/internal/fanout"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagediff"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imageorchestrator"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

var concurrentLocales = []string{"de-DE", "en-US", "fr-FR"}

// slotBytes is the local image of a slot; an even slot index is also live on
// Play (unchanged), an odd one is not (upload), so the plan mixes both.
func slotBytes(loc string, ty images.Type) []byte { return []byte(loc + "/" + string(ty)) }

func liveFor(i int, loc string, ty images.Type) []images.Image {
	if i%2 == 1 {
		return nil
	}
	return []images.Image{{ID: loc + string(ty), Sha256: sha(slotBytes(loc, ty))}}
}

// TestApply_planningReadsInParallel_planUnchanged: the 8 x L images.list reads
// run fanout.Limit at a time and answer in a scrambled order, yet the diff is
// the one a serial walk of (sorted locale, canonical type) gives, and the
// uploads of the real apply still go out one at a time in that order.
func TestApply_planningReadsInParallel_planUnchanged(t *testing.T) {
	tr := imagetree.Tree{}
	var serial []imagediff.SlotInput
	live := map[string]string{}
	i := 0
	for _, loc := range concurrentLocales {
		tr[loc] = map[images.Type][][]byte{}
		for _, ty := range images.Types() {
			tr[loc][ty] = [][]byte{slotBytes(loc, ty)}
			l := liveFor(i, loc, ty)
			body := `{"images":[]}`
			if l != nil {
				body = fmt.Sprintf(`{"images":[{"id":%q,"sha256":%q}]}`, l[0].ID, l[0].Sha256)
			}
			live["/listings/"+loc+"/"+string(ty)] = body
			serial = append(serial, imagediff.SlotInput{Locale: loc, Type: ty, Local: []string{sha(slotBytes(loc, ty))}, Live: l})
			i++
		}
	}
	want := imagediff.Aggregate("com.example.app", imagediff.Plans(serial, false))

	for _, dryRun := range []bool{true, false} {
		fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
			switch {
			case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
				return 200, `{"id":"e"}`, true
			case strings.HasSuffix(c.Path, ":commit"):
				return 200, `{}`, true
			case c.Method == http.MethodPost && strings.HasPrefix(c.Path, "/upload/"):
				return 200, `{"image":{"id":"up","sha256":"s"}}`, true
			case c.Method == http.MethodDelete && strings.Contains(c.Path, "/listings/"):
				return 200, `{"deleted":[]}`, true
			case c.Method == http.MethodDelete && strings.HasSuffix(c.Path, "/edits/e"):
				return 204, "", true
			case c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/"):
				return 200, live[c.Path[strings.Index(c.Path, "/listings/"):]], true
			}
			t.Errorf("unexpected: %s %s", c.Method, c.Path)
			return 0, "", false
		})
		// A per-slot pseudo-random latency scrambles the completion order.
		rt := testkit.NewDelayed(fake, func(r *http.Request) time.Duration {
			h := fnv.New32a()
			_, _ = h.Write([]byte(r.URL.Path))
			return time.Duration(1+h.Sum32()%6) * time.Millisecond
		})

		dir, local := stage(t, tr)
		res, err := imageorchestrator.Apply(context.Background(), &http.Client{Transport: rt}, local, imageorchestrator.Opts{
			Dir:     dir,
			Package: "com.example.app", DryRun: dryRun, Confirm: !dryRun, NoValidate: true,
		})
		if err != nil {
			t.Fatalf("dryRun=%v: %v", dryRun, err)
		}
		if p := rt.Peak(); p != fanout.Limit {
			t.Errorf("dryRun=%v: peak in-flight requests = %d, want exactly fanout.Limit (%d)", dryRun, p, fanout.Limit)
		}
		if !reflect.DeepEqual(res.Diff, want) {
			t.Errorf("dryRun=%v: diff = %+v, want the serial plan %+v", dryRun, res.Diff, want)
		}
		if dryRun {
			continue
		}
		var got, wantWrites []string
		for _, c := range fake.Calls() {
			if c.Method == http.MethodPost && strings.HasPrefix(c.Path, "/upload/") {
				got = append(got, c.Path[strings.Index(c.Path, "/listings/"):]+" "+string(c.Body))
			}
		}
		for i, in := range serial {
			if i%2 == 1 {
				wantWrites = append(wantWrites, "/listings/"+in.Locale+"/"+string(in.Type)+" "+string(slotBytes(in.Locale, in.Type)))
			}
		}
		if !reflect.DeepEqual(got, wantWrites) {
			t.Errorf("uploads = %q, want them in serial (locale, type) order %q", got, wantWrites)
		}
	}
}
