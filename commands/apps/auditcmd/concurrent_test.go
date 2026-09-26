package auditcmd_test

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PollyGlot/google-play-cli/commands/apps/auditcmd"
	"github.com/PollyGlot/google-play-cli/internal/fanout"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// TestRun_concurrentSweepKeepsPackageOrder: apps are read fanout.Limit at a
// time and the first-named apps answer LAST, yet ran, errors and findings
// follow the named order exactly as a serial sweep would, a failed app never
// stops the others, and every Edit opened is discarded.
func TestRun_concurrentSweepKeepsPackageOrder(t *testing.T) {
	// Named out of alphabetical order on purpose: a named sweep keeps the
	// operator's order, it does not sort.
	pkgs := []string{"com.z", "com.a", "com.m", "com.b", "com.y", "com.c", "com.x", "com.d", "com.w", "com.e"}
	failing := map[string]int{"com.m": http.StatusForbidden, "com.x": http.StatusInternalServerError}
	delay := map[string]time.Duration{}
	for i, p := range pkgs {
		delay[p] = time.Duration(len(pkgs)-i) * time.Millisecond
	}

	fake := testkit.NewFake(func(c testkit.Call) (int, string, bool) {
		pkg := pkgOf(c.Path)
		switch {
		case c.Method == http.MethodPost && strings.HasSuffix(c.Path, "/edits"):
			return 0, fmt.Sprintf(`{"id":"edit-%s"}`, pkg), true
		case c.Method == http.MethodDelete && strings.Contains(c.Path, "/edits/"):
			return http.StatusNoContent, "", true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/tracks"):
			if s := failing[pkg]; s != 0 {
				return s, `{"error":{"message":"nope"}}`, true
			}
			return 0, driftedTracks, true
		case c.Method == http.MethodGet && strings.HasSuffix(c.Path, "/listings"):
			return 0, oneLocale, true
		}
		return 0, "", false
	})
	rt := testkit.NewDelayed(fake, func(r *http.Request) time.Duration { return delay[pkgOf(r.URL.Path)] })
	rc, _ := newRC(t, rt)

	report, err := auditcmd.Run(rc, auditcmd.Input{Packages: pkgs})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if p := rt.Peak(); p != fanout.Limit {
		t.Errorf("peak in-flight requests = %d, want exactly fanout.Limit (%d)", p, fanout.Limit)
	}

	var wantRan, wantFailed []string
	for _, p := range pkgs {
		if failing[p] != 0 {
			wantFailed = append(wantFailed, p)
		} else {
			wantRan = append(wantRan, p)
		}
	}
	if !slices.Equal(report.Ran.Apps, wantRan) {
		t.Errorf("ran.apps = %v, want %v (named order, failures removed)", report.Ran.Apps, wantRan)
	}
	var gotFailed []string
	for _, e := range report.Errors {
		gotFailed = append(gotFailed, e.Package)
	}
	if !slices.Equal(gotFailed, wantFailed) {
		t.Errorf("errors = %v, want %v in named order", gotFailed, wantFailed)
	}
	if len(report.Errors) == 2 && (report.Errors[0].ExitCode != 11 || report.Errors[1].ExitCode != 40) {
		t.Errorf("exit codes = %d, %d, want 11 (403) then 40 (5xx)", report.Errors[0].ExitCode, report.Errors[1].ExitCode)
	}
	// Findings are grouped per app in the same order.
	var seen []string
	for _, f := range report.Findings {
		if len(seen) == 0 || seen[len(seen)-1] != f.Package {
			seen = append(seen, f.Package)
		}
	}
	if !slices.Equal(seen, wantRan) {
		t.Errorf("findings grouped by %v, want %v", seen, wantRan)
	}

	var opened, discarded int
	for _, c := range fake.Calls() {
		switch c.Method {
		case http.MethodPost:
			opened++
		case http.MethodDelete:
			discarded++
		}
	}
	if opened != len(pkgs) || discarded != len(pkgs) {
		t.Errorf("opened %d / discarded %d Edits, want %d of each", opened, discarded, len(pkgs))
	}
}
