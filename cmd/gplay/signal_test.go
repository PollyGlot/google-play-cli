package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/PollyGlot/google-play-cli/internal/auth/resolver"
	"github.com/PollyGlot/google-play-cli/internal/kernel"
	"github.com/PollyGlot/google-play-cli/internal/testkit"
)

// Environment of the interrupt helper process (TestInterruptHelperProcess).
const (
	envInterruptChild = "GPLAY_TEST_INTERRUPT_CHILD"    // non-empty: act as the child
	envInterruptDir   = "GPLAY_TEST_INTERRUPT_DIR"      // scratch dir: config, artifact, call log
	envInterruptHang  = "GPLAY_TEST_INTERRUPT_HANG"     // "delete": the discard hangs too
	readyLine         = "gplay-test: upload in flight"  // child → parent: send the signal now
	discardLine       = "gplay-test: discard in flight" // child → parent: the cleanup started
	editsPath         = "/androidpublisher/v3/applications/com.example.app/edits"
)

// hangingPlay answers the Play API from a testkit.Fake, except that the
// artifact upload (and, with hangDelete, the Edit discard) blocks until its
// request is canceled, so the process sits mid-command when the signal lands.
type hangingPlay struct {
	fake       *testkit.Fake
	hangDelete bool
	ready      sync.Once
}

func (h *hangingPlay) serve(req *http.Request) (*http.Response, error) {
	upload := strings.HasPrefix(req.URL.Path, "/upload/")
	if upload || (h.hangDelete && req.Method == http.MethodDelete) {
		if upload {
			h.ready.Do(func() { fmt.Fprintln(os.Stderr, readyLine) })
		} else {
			fmt.Fprintln(os.Stderr, discardLine)
		}
		// Record the call through the Fake first, so the log shows it.
		_, _ = h.fake.RoundTrip(req)
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	return h.fake.RoundTrip(req)
}

// TestInterruptHelperProcess is not a test: it is the gplay process the
// interrupt tests signal. It runs `releases upload` against a fake Play API
// through the same execute path main uses, then logs every API call it made.
func TestInterruptHelperProcess(t *testing.T) {
	if os.Getenv(envInterruptChild) == "" {
		t.Skip("helper process for the interrupt tests")
	}
	dir := os.Getenv(envInterruptDir)
	fake := testkit.NewFake(
		func(c testkit.Call) (int, string, bool) {
			return 200, `{"id":"e1"}`, c.Method == http.MethodPost && c.Path == editsPath
		},
		func(c testkit.Call) (int, string, bool) {
			return 204, "", c.Method == http.MethodDelete && c.Path == editsPath+"/e1"
		},
	)
	rt := &hangingPlay{fake: fake, hangDelete: os.Getenv(envInterruptHang) == "delete"}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Transport: testkit.RoundTripFunc(rt.serve)})

	root := newRootCmd(kernel.Boot{
		ConfigPath:   filepath.Join(dir, "config.json"),
		KeystoreRoot: filepath.Join(dir, "accounts"),
	})
	root.SetArgs([]string{
		"releases", "upload", filepath.Join(dir, "app.aab"),
		"--package", "com.example.app", "--track", "internal", "--skip-preflight",
	})
	code := execute(ctx, root, os.Stderr, nil)

	var log strings.Builder
	for _, c := range fake.Calls() {
		fmt.Fprintf(&log, "%s %s\n", c.Method, c.Path)
	}
	if err := os.WriteFile(filepath.Join(dir, "calls.log"), []byte(log.String()), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "write call log:", err)
	}
	os.Exit(code)
}

// startInterruptChild launches the helper process and returns once its upload
// is in flight. The returned channel yields the child's exit error.
func startInterruptChild(t *testing.T, hang string) (cmd *exec.Cmd, dir string, exited <-chan error, stderr func() string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals")
	}
	dir = t.TempDir()
	sa := filepath.Join(dir, "sa.json")
	if err := os.WriteFile(sa, testkit.ServiceAccountJSON(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.aab"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestInterruptHelperProcess$")
	cmd.Env = append(os.Environ(),
		envInterruptChild+"=1",
		envInterruptDir+"="+dir,
		envInterruptHang+"="+hang,
		resolver.EnvServiceAccount+"="+sa,
		resolver.EnvAccount+"=",
		"GPLAY_READONLY=",
	)
	pipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	var (
		mu  sync.Mutex
		buf strings.Builder
	)
	ready := make(chan struct{})
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		sc := bufio.NewScanner(pipe)
		once := sync.Once{}
		for sc.Scan() {
			mu.Lock()
			buf.WriteString(sc.Text() + "\n")
			mu.Unlock()
			if sc.Text() == readyLine {
				once.Do(func() { close(ready) })
			}
		}
	}()
	done := make(chan error, 1)
	go func() {
		<-scanned // Wait must not run before the pipe is drained
		done <- cmd.Wait()
	}()
	stderr = func() string { mu.Lock(); defer mu.Unlock(); return buf.String() }

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("helper exited before its upload started (%v); stderr:\n%s", err, stderr())
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("helper never reached the upload; stderr:\n%s", stderr())
	}
	return cmd, dir, done, stderr
}

// SIGTERM mid-upload (a CI job canceled or timed out): gplay discards the
// implicit Edit it opened, commits nothing, and exits 50 within the 5s the
// runner's kill margin leaves.
func TestInterrupt_SIGTERM_discardsImplicitEditAndExits50(t *testing.T) {
	cmd, dir, exited, stderr := startInterruptChild(t, "")

	sent := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	var err error
	select {
	case err = <-exited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("gplay still running 10s after SIGTERM; stderr:\n%s", stderr())
	}
	if took := time.Since(sent); took > 5*time.Second {
		t.Errorf("exit took %v after SIGTERM, want <= 5s", took)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 50 {
		t.Fatalf("exit = %v, want code 50; stderr:\n%s", err, stderr())
	}
	if !strings.Contains(stderr(), "gplay: interrupted by SIGTERM") {
		t.Errorf("stderr does not name the signal:\n%s", stderr())
	}

	raw, rerr := os.ReadFile(filepath.Join(dir, "calls.log"))
	if rerr != nil {
		t.Fatalf("read call log: %v", rerr)
	}
	calls := string(raw)
	if n := strings.Count(calls, "DELETE "+editsPath+"/e1\n"); n != 1 {
		t.Errorf("DELETE of the implicit Edit = %d, want 1; calls:\n%s", n, calls)
	}
	if strings.Contains(calls, ":commit") {
		t.Errorf("an interrupted upload committed its Edit; calls:\n%s", calls)
	}
}

// A second signal is not caught: when the discard itself hangs, SIGINT then
// SIGTERM (the Actions sequence) kills gplay at once instead of waiting out
// the cleanup bound.
func TestInterrupt_secondSignalForcesExit(t *testing.T) {
	cmd, _, exited, stderr := startInterruptChild(t, "delete")

	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	// Wait for the first signal to have started the (hanging) discard.
	for deadline := time.Now().Add(10 * time.Second); !strings.Contains(stderr(), discardLine); {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("the first signal never started the discard; stderr:\n%s", stderr())
		}
		time.Sleep(10 * time.Millisecond)
	}
	sent := time.Now()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	var err error
	select {
	case err = <-exited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("gplay survived a second signal; stderr:\n%s", stderr())
	}
	if took := time.Since(sent); took > 2*time.Second {
		t.Errorf("exit took %v after the second signal, want immediate", took)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("exit = %v, want death by signal", err)
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		t.Errorf("exit status = %v, want killed by SIGTERM", ee)
	}
}
