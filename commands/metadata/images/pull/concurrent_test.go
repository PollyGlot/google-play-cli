package imagespull_test

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	imagespull "github.com/PollyGlot/google-play-cli/commands/metadata/images/pull"
	"github.com/PollyGlot/google-play-cli/internal/exit"
	"github.com/PollyGlot/google-play-cli/internal/fanout"
	"github.com/PollyGlot/google-play-cli/internal/metadata/imagetree"
	"github.com/PollyGlot/google-play-cli/internal/play/images"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

const shots = 8

var concurrentLocales = []string{"de-DE", "en-US", "fr-FR"}

// shotURL is where screenshot j of loc is served; shotBytes is its content.
func shotURL(loc string, j int) string { return fmt.Sprintf("https://play-lh.test/%s/%d", loc, j) }
func shotBytes(loc string, j int) []byte {
	return png(fmt.Sprintf("%s-%d", loc, j))
}

// galleryFake serves a pull where every locale holds an 8-screenshot phone
// gallery and nothing else. A download whose "<locale>/<j>" is in fail answers
// 500; override responders run first.
func galleryFake(fail map[string]bool, override ...testkit.Responder) *testkit.Fake {
	var ls []string
	for _, l := range concurrentLocales {
		ls = append(ls, fmt.Sprintf(`{"language":%q,"title":"t"}`, l))
	}
	return testkit.NewFake(append(override, func(c testkit.Call) (int, string, bool) {
		switch {
		case c.Host == "play-lh.test":
			key := strings.TrimPrefix(c.Path, "/")
			if fail[key] {
				return http.StatusInternalServerError, "", true
			}
			parts := strings.Split(key, "/")
			var j int
			_, _ = fmt.Sscan(parts[1], &j)
			return 0, string(shotBytes(parts[0], j)), true
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 0, `{"id":"e1"}`, true
		case c.Method == http.MethodDelete && strings.HasSuffix(c.Path, "/edits/e1"):
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/listings"):
			return 0, `{"listings":[` + strings.Join(ls, ",") + `]}`, true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/"+string(images.PhoneScreenshots)):
			loc := strings.Split(strings.Split(c.Path, "/listings/")[1], "/")[0]
			var imgs []string
			for j := range shots {
				imgs = append(imgs, fmt.Sprintf(`{"id":"%s%d","url":%q}`, loc, j, shotURL(loc, j)))
			}
			return 0, `{"images":[` + strings.Join(imgs, ",") + `]}`, true
		case c.Method == http.MethodGet && strings.Contains(c.Path, "/listings/"):
			return 0, `{"images":[]}`, true
		}
		return 0, "", false
	})...)
}

// reverseLatency makes later screenshots of a gallery answer FIRST, so a pull
// that wrote in completion order would scramble the gallery.
func reverseLatency(slow map[string]time.Duration) func(*http.Request) time.Duration {
	return func(r *http.Request) time.Duration {
		if r.URL.Host != "play-lh.test" {
			return 2 * time.Millisecond
		}
		key := strings.TrimPrefix(r.URL.Path, "/")
		if d, ok := slow[key]; ok {
			return d
		}
		var j int
		_, _ = fmt.Sscan(strings.Split(key, "/")[1], &j)
		return time.Duration(shots-j) * time.Millisecond
	}
}

// TestRun_concurrentPullKeepsGalleryOrder: the downloads run fanout.Limit at a
// time and complete out of order, yet every gallery lands on disk in display
// order and the summary is in (locale, type) order.
func TestRun_concurrentPullKeepsGalleryOrder(t *testing.T) {
	rt := testkit.NewDelayed(galleryFake(nil), reverseLatency(nil))
	rc := newRC(t, rt)
	dir := t.TempDir()

	out, err := imagespull.Run(rc, imagespull.Input{Package: "com.example.app", Dir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p := rt.Peak(); p != fanout.Limit {
		t.Errorf("peak in-flight requests = %d, want exactly fanout.Limit (%d)", p, fanout.Limit)
	}
	tr, err := imagetree.Read(dir)
	if err != nil {
		t.Fatalf("imagetree.Read: %v", err)
	}
	for _, loc := range concurrentLocales {
		got := tr[loc][images.PhoneScreenshots]
		if len(got) != shots {
			t.Fatalf("%s: %d screenshots on disk, want %d", loc, len(got), shots)
		}
		for j, b := range got {
			if !bytes.Equal(b, shotBytes(loc, j)) {
				t.Errorf("%s screenshot %d = %q, want %q (display order lost)", loc, j+1, b, shotBytes(loc, j))
			}
		}
	}
	pulled := out.(imagespull.Payload).Pulled
	if len(pulled) != len(concurrentLocales) {
		t.Fatalf("pulled = %+v, want one slot per locale", pulled)
	}
	for i, loc := range concurrentLocales {
		if pulled[i].Locale != loc {
			t.Errorf("pulled[%d].Locale = %s, want %s (sorted)", i, pulled[i].Locale, loc)
		}
	}
}

// TestRun_concurrentPull429FollowsRetryFlag pins what the burst does to the
// rate limit, unchanged by concurrency: --retry stays opt-in, so without it a
// 429 on one images.list fails the whole pull (exit 60, nothing written); with
// --retry the same 429 is replayed and the pull completes.
func TestRun_concurrentPull429FollowsRetryFlag(t *testing.T) {
	for _, retry := range []int{0, 1} {
		t.Run(fmt.Sprintf("retry=%d", retry), func(t *testing.T) {
			var limited atomic.Bool
			fake := galleryFake(nil, func(c testkit.Call) (int, string, bool) {
				if strings.HasSuffix(c.Path, "/en-US/"+string(images.PhoneScreenshots)) && limited.CompareAndSwap(false, true) {
					return http.StatusTooManyRequests, `{"error":{"code":429,"message":"slow down"}}`, true
				}
				return 0, "", false
			})
			rc := newRC(t, testkit.NewDelayed(fake, reverseLatency(nil)))
			rc.Retry = retry
			dir := t.TempDir()

			_, err := imagespull.Run(rc, imagespull.Input{Package: "com.example.app", Dir: dir})
			tr, readErr := imagetree.Read(dir)
			if readErr != nil {
				t.Fatalf("imagetree.Read: %v", readErr)
			}
			if retry == 0 {
				if code := exit.For(err); code != 60 {
					t.Fatalf("exit = %d (err %v), want 60 for an unretried 429", code, err)
				}
				if len(tr) != 0 {
					t.Errorf("a pull failed on a 429 still wrote %d locale(s)", len(tr))
				}
				return
			}
			if err != nil {
				t.Fatalf("Run with --retry: %v", err)
			}
			if got := len(tr["en-US"][images.PhoneScreenshots]); got != shots {
				t.Errorf("en-US screenshots = %d, want %d after the replayed 429", got, shots)
			}
		})
	}
}

// TestRun_concurrentPullIsAllOrNothing: one failed download leaves the target
// tree exactly as it was (no partial gallery a later `images apply --prune`
// would push as deletions), and the error is the serial one: the failure with
// the lowest (locale, type, position), even when a later one fails first.
func TestRun_concurrentPullIsAllOrNothing(t *testing.T) {
	fail := map[string]bool{"de-DE/5": true, "fr-FR/0": true}
	// de-DE/5 answers last, fr-FR/0 fails well before it.
	rt := testkit.NewDelayed(galleryFake(fail), reverseLatency(map[string]time.Duration{"de-DE/5": 40 * time.Millisecond}))
	rc := newRC(t, rt)
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "en-US", "title.txt")
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sentinel, []byte("My App"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := imagespull.Run(rc, imagespull.Input{Package: "com.example.app", Dir: dir})
	if err == nil {
		t.Fatal("Run succeeded with two failed downloads")
	}
	if !strings.Contains(err.Error(), shotURL("de-DE", 5)) {
		t.Errorf("err = %v, want the lowest-index failure (%s)", err, shotURL("de-DE", 5))
	}
	var files []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, _ error) error {
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 1 || files[0] != sentinel {
		t.Errorf("files after a failed pull = %v, want only the untouched %s", files, sentinel)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "en-US", "images")); !os.IsNotExist(statErr) {
		t.Errorf("a failed pull created images/: %v", statErr)
	}
}
