package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/artifacttest"
	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/compliance/datasafety"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// examplePackage is the app every example targets: the Project pin each run
// starts from, so an example without --package resolves as in a repo after
// gplay init.
const examplePackage = "com.example.app"

// TestExamples_writeCommandsRunAgainstTheConstraints runs every Example line
// of every write command (kernel.MarkMutating) against a fake Play, in a
// fresh directory holding the files examples name (testdata/examples). The
// fake checks each request body against the constraints table
// (internal/testkit), so an example whose body Play refuses fails with the
// rule's name (#776, #809).
//
// A non-preview example (no --dry-run, no --plan) must also send a write of
// its own, or the table judged nothing. testdata/examples.allow names the
// lines that do not run, each for a known defect: a listed line that runs, or
// no longer exists, fails too. A write command must offer an example.
func TestExamples_writeCommandsRunAgainstTheConstraints(t *testing.T) {
	allowed := loadExampleAllowlist(t)
	seen := map[string]bool{}
	for _, c := range runnableLeaves(newExampleRoot()) {
		if !kernel.IsMutating(c) {
			continue
		}
		runs := exampleRuns(c.Example)
		if len(runs) == 0 {
			t.Errorf("%q writes to Play but its Example has no gplay line: give it one, it is what users copy", leafKey(c))
		}
		for _, r := range runs {
			key := strings.Join(r.args, " ")
			if seen[key] {
				continue
			}
			seen[key] = true
			t.Run(key, func(t *testing.T) {
				why := runExample(t, leafKey(c), r)
				switch {
				case why == "" && allowed[key]:
					t.Errorf("runs now: delete %q from testdata/examples.allow so the list keeps its gain", key)
				case why != "" && !allowed[key]:
					t.Errorf("Example %q: %s", key, why)
				}
			})
		}
	}
	for key := range allowed {
		if !seen[key] {
			t.Errorf("testdata/examples.allow: %q is no write Example any more: delete the line", key)
		}
	}
}

// examplePlaceholders are the values a user sets before copying an Example
// that reads a $NAME none of its lines sets.
var examplePlaceholders = map[string]string{
	"NEW_KMS_KEY": "projects/example/locations/global/keyRings/play/cryptoKeys/app-signing/cryptoKeyVersions/2",
}

// runExample runs r, an Example line of leaf, in a fresh directory against
// exampleFake and returns why it failed, "" when it exited 0 with every body
// inside the rules.
func runExample(t *testing.T, leaf string, r exampleRun) string {
	t.Helper()
	args := r.args
	for _, name := range r.unset {
		v, ok := examplePlaceholders[name]
		if !ok {
			return fmt.Sprintf("cannot run as written: no line of the Example sets $%s, and examplePlaceholders has no value for it", name)
		}
		args = slices.Clone(args)
		for i := range args {
			args[i] = strings.NewReplacer("${"+name+"}", v, "$"+name, v).Replace(args[i])
		}
	}
	world, ok := exampleWorlds[leaf]
	if !ok {
		world = defaultWorld
	}
	dir := t.TempDir()
	copyFixtures(t, dir)
	buildFixtures(t, dir)
	if world.pin {
		pin := fmt.Sprintf(`{"editId":"example-edit","package":%q}`, examplePackage)
		if err := os.WriteFile(filepath.Join(dir, ".gplay", "edit-"+examplePackage+".json"), []byte(pin), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	// HOME apart from dir: the Project walk-up never takes ~/.gplay, the
	// macOS config directory, for a Project.
	home := t.TempDir()
	for k, v := range map[string]string{
		"HOME":                     home,
		"XDG_CONFIG_HOME":          filepath.Join(home, ".config"),
		resolver.EnvAccount:        "",
		resolver.EnvServiceAccount: string(testkit.ServiceAccountJSON(t)),
		"GPLAY_READONLY":           "",
		"GPLAY_DEFAULT_OUTPUT":     "",
		"GPLAY_APP_STORE_PACKAGE":  "",
		"GPLAY_DEVELOPER_ID":       "1234567890",
	} {
		t.Setenv(k, v)
	}
	for k, v := range r.env {
		t.Setenv(k, v)
	}

	var stdin io.Reader = strings.NewReader("")
	if r.stdin != "" {
		f, err := os.Open(r.stdin)
		if err != nil {
			return fmt.Sprintf("no fixture for the redirected file: %v", err)
		}
		defer func() { _ = f.Close() }()
		stdin = f
	}
	fake := exampleFake(world).CollectRefusals()
	var stdout, stderr bytes.Buffer
	root := newRootCmd(kernel.Boot{
		Stdout:       &stdout,
		Stderr:       &stderr,
		Stdin:        stdin,
		ConfigPath:   filepath.Join(home, ".gplay", "config.json"),
		KeystoreRoot: filepath.Join(home, ".gplay", "accounts"),
		Keyring:      noKeyring{},
	})
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(args)
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: fake})
	code := execute(ctx, root, &stderr, nil)
	if refused := fake.Refusals(); len(refused) > 0 {
		return refused[0]
	}
	if code != 0 {
		return fmt.Sprintf("exit %d against the example fake; stderr:\n%s", code, stderr.String())
	}
	if !slices.Contains(args, "--dry-run") && !slices.Contains(args, "--plan") && !changedPlay(leaf, fake.Calls()) {
		return "exited 0 but sent no write: the constraints table judged nothing"
	}
	return ""
}

// editLifecycle matches edits.insert and the discard of an implicit Edit:
// every Edit-based command sends them, so they prove no write of its own.
var editLifecycle = regexp.MustCompile(`^(POST /androidpublisher/v3/applications/[^/]+/edits|DELETE /androidpublisher/v3/applications/[^/]+/edits/[^/]+)$`)

// changedPlay reports whether calls hold a write of leaf's own. The Edit
// lifecycle counts only for the edits leaves, whose write it is.
func changedPlay(leaf string, calls []testkit.Call) bool {
	for _, c := range calls {
		if c.Method == http.MethodGet {
			continue
		}
		if strings.HasPrefix(leaf, "edits ") || !editLifecycle.MatchString(c.Method+" "+c.Path) {
			return true
		}
	}
	return false
}

// exampleWorld is the Play state an example runs against: what a write
// command reads before it writes. The default is one app whose every track
// serves one staged rollout (two releases would make every release verb ask
// which one), and whose developer account holds the members the team
// examples name.
type exampleWorld struct {
	track string // tracks.get body, %q the track name; "" answers 404
	pin   bool   // an explicit Edit is open (gplay edits begin ran)
}

var defaultWorld = exampleWorld{track: `{"track":%q,"releases":[
	{"name":"2.4.0","versionCodes":["1042"],"status":"inProgress","userFraction":0.1,"releaseNotes":[{"language":"en-US","text":"Faster sync."}]}
]}`}

// exampleWorlds holds the leaves whose Example states another starting
// point than defaultWorld ("Resume a halted production rollout", "Publish
// everything batched since gplay edits begin", a track not created yet).
var exampleWorlds = map[string]exampleWorld{
	"tracks create": {},
	"releases resume": {track: `{"track":%q,"releases":[
	{"name":"2.4.0","versionCodes":["1042"],"status":"halted","userFraction":0.1}
]}`},
	"edits commit":  {track: defaultWorld.track, pin: true},
	"edits discard": {track: defaultWorld.track, pin: true},
}

// exampleMembers answers users.list: the members the team examples act on.
const exampleMembers = `{"users":[
	{"name":"developers/1234567890/users/qa@example.com","email":"qa@example.com","accessState":"ACCESS_GRANTED"},
	{"name":"developers/1234567890/users/dev@example.com","email":"dev@example.com","accessState":"ACCESS_GRANTED"},
	{"name":"developers/1234567890/users/contractor@example.com","email":"contractor@example.com","accessState":"ACCESS_GRANTED",
	 "grants":[{"name":"developers/1234567890/users/contractor@example.com/grants/com.example.app","packageName":"com.example.app","appLevelPermissions":["CAN_VIEW_FINANCIAL_DATA"]}]},
	{"name":"developers/1234567890/users/former@example.com","email":"former@example.com","accessState":"ACCESS_GRANTED"}
]}`

// exampleMedia answers a media upload, with the fields any uploader reads
// from it.
const exampleMedia = `{"versionCode":1042,"sha1":"da39a3ee5e6b4b0d3255bfef95601890afd80709","sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",` +
	`"downloadUrl":"https://play.google.com/apps/test/com.example.app/1042","certificateFingerprint":"00:11","apkId":"apk-1","imageId":"img-1","fileId":"file-1"}`

// exampleFake serves w and accepts every write, echoing its body.
func exampleFake(w exampleWorld) *testkit.Fake {
	return testkit.NewFake(testkit.ReplyHeader(func(c testkit.Call) (int, string, bool) {
		switch {
		case strings.Contains(c.Query, "uploadType=resumable"):
			return http.StatusOK, "", true
		case strings.Contains(c.Query, "upload_id="), strings.HasPrefix(c.Path, "/upload/"):
			return http.StatusOK, exampleMedia, true
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return http.StatusOK, `{"id":"example-edit","expiryTimeSeconds":"1700000000"}`, true
		case strings.HasSuffix(c.Path, ":commit") || strings.HasSuffix(c.Path, ":validate"):
			return http.StatusOK, `{"id":"example-edit"}`, true
		case c.Method == http.MethodDelete:
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && trackPath.MatchString(c.Path) && w.track == "":
			return http.StatusNotFound, `{"error":{"code":404,"message":"Track not found.","status":"NOT_FOUND"}}`, true
		case c.Method == http.MethodGet && trackPath.MatchString(c.Path):
			return http.StatusOK, fmt.Sprintf(w.track, path.Base(c.Path)), true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/users"):
			return http.StatusOK, exampleMembers, true
		case c.Method == http.MethodGet:
			return http.StatusOK, `{}`, true
		case len(bytes.TrimSpace(c.Body)) > 0 && bytes.TrimSpace(c.Body)[0] == '{':
			return http.StatusOK, string(c.Body), true
		}
		return http.StatusOK, `{}`, true
	}, "Location", func(c testkit.Call, _ int) string {
		if strings.Contains(c.Query, "uploadType=resumable") {
			return "https://" + c.Host + c.Path + "?upload_id=example-session"
		}
		return ""
	}))
}

var trackPath = regexp.MustCompile(`/edits/[^/]+/tracks/[^/]+$`)

// buildFixtures writes the files Examples name that are built rather than
// committed: artifacts carrying a real manifest, images of the dimensions
// Play requires, a certificate, a Data Safety CSV with today's header.
func buildFixtures(t *testing.T, dir string) {
	t.Helper()
	for name, pkg := range map[string]string{
		"app-release.aab": examplePackage,
		"phone.aab":       examplePackage,
		"wear.aab":        examplePackage,
		"build/app.bin":   examplePackage,
	} {
		mkdirFor(t, dir, name)
		artifacttest.AAB(t, dir, name, pkg)
	}
	for name, pkg := range map[string]string{
		"app-debug.apk": examplePackage,
		"game-base.apk": "com.example.game",
	} {
		artifacttest.APK(t, dir, name, pkg)
	}
	artifacttest.Zip(t, dir, "native-debug-symbols.zip", map[string][]byte{"arm64-v8a/libexample.so.sym": []byte("symbols")})
	cert := testkit.CertificatePEM(t)
	csv := strings.Join(datasafety.ReferenceHeader(), ",") + "\n"
	for name, b := range map[string][]byte{
		"icon-512.png":                                 testkit.PNG(512, 512),
		"screenshot-1.png":                             testkit.PNG(1080, 1920),
		"metadata/en-US/images/icon.png":               testkit.PNG(512, 512),
		"metadata/en-US/images/phoneScreenshots/1.png": testkit.PNG(1080, 1920),
		"kms-cert.pem":                                 cert,
		"upload-cert.pem":                              cert,
		"new-cert.pem":                                 cert,
		"compliance/data-safety.csv":                   []byte(csv),
	} {
		mkdirFor(t, dir, name)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func mkdirFor(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
}

// noKeyring keeps the OS keychain out of the run: the service account comes
// from the environment.
type noKeyring struct{}

var errNoKeyring = errors.New("examples test: no keyring")

func (noKeyring) Set(string, string, string) error   { return errNoKeyring }
func (noKeyring) Get(string, string) (string, error) { return "", errNoKeyring }
func (noKeyring) Delete(string, string) error        { return errNoKeyring }

// copyFixtures copies testdata/examples, the files Examples name, into dir
// and pins examplePackage there.
func copyFixtures(t *testing.T, dir string) {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("testdata", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	pin := filepath.Join(dir, ".gplay", "config.json")
	if err := os.MkdirAll(filepath.Dir(pin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pin, []byte(fmt.Sprintf(`{"package":%q}`, examplePackage)), 0o600); err != nil {
		t.Fatal(err)
	}
}

// loadExampleAllowlist reads testdata/examples.allow: one Example line (its
// argv after gplay) per line, under `#` comments that give the reason.
func loadExampleAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "examples.allow"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	allowed := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if allowed[line] {
			t.Errorf("testdata/examples.allow: %q is listed twice", line)
		}
		allowed[line] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return allowed
}
