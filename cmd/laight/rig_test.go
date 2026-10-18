package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/berni23/laight/internal/rig"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if binPath != "" {
		os.RemoveAll(filepath.Dir(binPath))
	}
	os.Exit(code)
}

// laightBin builds the binary once: the engine runs in tmux, so it has to be a real executable.
func laightBin(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "laight-bin-")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "laight")
		if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
			buildErr = &exec.ExitError{Stderr: out}
		}
	})
	if buildErr != nil {
		t.Fatalf("go build: %v", buildErr)
	}
	return binPath
}

// rigEnv is a git repo with one commit on main, a laight home of its own and therefore a
// throwaway tmux server, which the test kills at the end.
type rigEnv struct {
	t    *testing.T
	bin  string
	repo string
	home string
	srv  tmux.Server
}

func newRigEnv(t *testing.T, workflow string) *rigEnv {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	e := &rigEnv{t: t, bin: laightBin(t), repo: t.TempDir(), home: t.TempDir()}
	e.srv = tmux.Server{Socket: state.Socket(e.home)}
	t.Cleanup(func() { e.srv.KillServer() })
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", e.repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	wf := filepath.Join(e.repo, ".laight", "workflows", "demo.yaml")
	if err := os.MkdirAll(filepath.Dir(wf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wf, []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *rigEnv) laight(args ...string) (string, error) {
	c := exec.Command(e.bin, args...)
	c.Dir = e.repo
	c.Env = append(os.Environ(), "LAIGHT_HOME="+e.home)
	out, err := c.CombinedOutput()
	return string(out), err
}

func (e *rigEnv) mustLaight(args ...string) string {
	e.t.Helper()
	out, err := e.laight(args...)
	if err != nil {
		e.t.Fatalf("laight %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// onlyRun returns the run the test started.
func (e *rigEnv) onlyRun() *state.Run {
	e.t.Helper()
	runs, err := state.List(e.home)
	if err != nil || len(runs) != 1 {
		e.t.Fatalf("runs = %v, %v", runs, err)
	}
	return runs[0]
}

// waitFor polls the run's events until one of type typ shows up.
func (e *rigEnv) waitFor(id, typ string, n int) {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		evs, _ := state.Events(e.home, id)
		count := 0
		for _, ev := range evs {
			if ev.Type == typ {
				count++
			}
		}
		if count >= n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	r, _ := state.Load(e.home, id)
	screen, _ := e.srv.Capture(r.Session, "engine")
	evs, _ := state.Events(e.home, id)
	e.t.Fatalf("no %d× %s event; status %s %q; events %+v\nengine:\n%s", n, typ, r.Status, r.Error, evs, screen)
}

// waitLog waits up to 5s for want in the seat's pane log.
func waitLog(t *testing.T, home, run, seat, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(state.Dir(home, run), "seats", seat+".log"))
		if strings.Contains(string(b), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s.log has no %q:\n%s", seat, want, b)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestRigUpAndAbort(t *testing.T) {
	e := newRigEnv(t, `version: 1
id: demo
inputs:
  who: { default: world }
seats:
  a:
    runtime: shell
    command: sh
    setup: touch setup-ran
    env: { GREETING: "hi {{ .inputs.who }}" }
  b: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: one, seat: a, objective: 'echo one-$((40+2))', next_hop: { on: { handoff: two } } }
  - { id: two, seat: b, objective: y }
`)
	out := e.mustLaight("run", "demo", "who=tests")
	r := e.onlyRun()
	if !strings.Contains(out, "run "+r.Short()+": demo started") {
		t.Errorf("run output: %s", out)
	}
	e.waitFor(r.ID, "seat.up", 2)

	r, _ = state.Load(e.home, r.ID)
	if r.Status != state.Running || r.Inputs["who"] != "tests" || r.Base != "main" {
		t.Errorf("run.json: status %s, inputs %v, base %q", r.Status, r.Inputs, r.Base)
	}
	ws, err := e.srv.Windows(r.Session)
	if err != nil || strings.Join(ws, " ") != "engine a b" {
		t.Errorf("windows = %v, %v", ws, err)
	}
	wt := r.Seats["a"].Dir
	if _, err := os.Stat(filepath.Join(wt, "setup-ran")); err != nil {
		t.Errorf("setup didn't run in the worktree: %v", err)
	}
	if r.Seats["b"].Dir != r.Repo || r.Seats["b"].Worktree {
		t.Errorf("seat b: %+v", r.Seats["b"])
	}

	// The seat has the run's env, rendered, and its pane is logged. The engine delivers step
	// one to the same pane: wait until sh has run it, or the two pastes share a line. Its output
	// (42), unlike the pasted line, proves it ran, whatever the shell or locale.
	waitLog(t, e.home, r.ID, "a", "one-42")
	if err := e.srv.Paste(r.Session, "a", `echo "$GREETING/$LAIGHT_SEAT/$LAIGHT_RUN"`); err != nil {
		t.Fatal(err)
	}
	waitLog(t, e.home, r.ID, "a", "hi tests/a/"+r.ID)

	// The seat's env file, which can hold secrets, is gone once the seat has read it.
	if _, err := os.Stat(filepath.Join(state.Dir(e.home, r.ID), "seats", "a.env")); !os.IsNotExist(err) {
		t.Errorf("a.env still there: %v", err)
	}

	// No terminal and no --yes: abort refuses and changes nothing.
	if out, err := e.laight("abort"); err == nil || !strings.Contains(out, "add --yes") || !e.srv.HasSession(r.Session) {
		t.Fatalf("abort without --yes: %v\n%s", err, out)
	}
	// Live abort: the session goes, the worktree stays (on_finish.failure defaults to keep).
	out = e.mustLaight("abort", "--yes")
	if !strings.Contains(out, "keep 1 worktree") || e.srv.HasSession(r.Session) {
		t.Errorf("abort: session still there or wrong plan:\n%s", out)
	}
	r, _ = state.Load(e.home, r.ID)
	if r.Status != state.Aborted || r.Finished == nil {
		t.Errorf("after abort: status %s", r.Status)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Errorf("worktree removed on abort: %v", err)
	}
	// Abort on a finished run deletes its worktrees, and git forgets them.
	e.mustLaight("abort", r.Short()[:3], "--yes")
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Errorf("worktree still there: %v", err)
	}
	if b, _ := exec.Command("git", "-C", e.repo, "worktree", "list").Output(); strings.Count(string(b), "\n") != 1 {
		t.Errorf("git still lists worktrees:\n%s", b)
	}
	if out := e.mustLaight("abort", r.Short(), "--yes"); !strings.Contains(out, "nothing to do") {
		t.Errorf("third abort: %s", out)
	}
}

func TestRigUpFails(t *testing.T) {
	e := newRigEnv(t, `version: 1
id: demo
seats:
  a: { runtime: shell, command: sh, setup: "echo boom; exit 3" }
steps:
  - { id: one, seat: a, objective: x }
on_finish: { failure: destroy }
`)
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	e.waitFor(r.ID, "run.finished", 1)
	r, _ = state.Load(e.home, r.ID)
	if r.Status != state.Failed || !strings.Contains(r.Error, "setup") {
		t.Errorf("status %s, error %q", r.Status, r.Error)
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.srv.HasSession(r.Session) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if e.srv.HasSession(r.Session) {
		t.Error("the session outlived the run")
	}
	if _, err := os.Stat(filepath.Join(state.Dir(e.home, r.ID), "worktrees", "a")); !os.IsNotExist(err) {
		t.Errorf("on_finish.failure destroy kept the worktree: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(state.Dir(e.home, r.ID), "seats", "a.setup.log"))
	if !strings.Contains(string(b), "boom") {
		t.Errorf("setup log: %q", b)
	}
}

func TestRunInputs(t *testing.T) {
	e := newRigEnv(t, `version: 1
id: demo
inputs:
  issue: { required: true }
seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: one, seat: a, objective: "{{ .inputs.issue }}" }
`)
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"run", "demo"}, "missing required input: laight run demo issue=…"},
		{[]string{"run", "demo", "nope=1"}, `unknown input "nope" (inputs: issue)`},
		{[]string{"run", "demo", "issue"}, `inputs are key=value, got "issue"`},
		{[]string{"run", "nope"}, `no workflow "nope"`},
		{[]string{"attach"}, "no runs in this project yet"},
	} {
		if out, err := e.laight(tt.args...); err == nil || !strings.Contains(out, tt.want) {
			t.Errorf("laight %v = %v\n%s\nwant %q", tt.args, err, out, tt.want)
		}
	}
	if runs, _ := state.List(e.home); len(runs) != 0 {
		t.Errorf("a failed `run` left %d runs behind", len(runs))
	}
}

// waitStatus polls run.json until the run reaches status.
func (e *rigEnv) waitStatus(id, status string) *state.Run {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		r, err := state.Load(e.home, id)
		if err == nil && r.Status == status {
			return r
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("run status %v, want %s (%v)", r, status, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

const twoShellSeats = `version: 1
id: demo
seats:
  a: { runtime: shell, command: sh }
  b: { runtime: shell, command: sh, cwd: repo }
steps:
  - { id: one, seat: a, objective: x, next_hop: { on: { handoff: two } } }
  - { id: two, seat: b, objective: y }
on_finish: { failure: destroy }
`

// An abort typed in one of the rig's own panes must not kill itself before it's done.
func TestAbortFromInsideTheRig(t *testing.T) {
	e := newRigEnv(t, twoShellSeats)
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	e.waitFor(r.ID, "seat.up", 2)
	if err := e.srv.Paste(r.Session, "b", tmux.Join([]string{e.bin, "abort", r.Short(), "--yes"})); err != nil {
		t.Fatal(err)
	}
	e.waitFor(r.ID, "run.aborted", 1)
	r = e.waitStatus(r.ID, state.Aborted)
	if _, err := os.Stat(filepath.Join(state.Dir(e.home, r.ID), "worktrees", "a")); !os.IsNotExist(err) {
		t.Errorf("on_finish.failure destroy kept the worktree: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.srv.HasSession(r.Session) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if e.srv.HasSession(r.Session) {
		t.Error("the session outlived the abort")
	}
}

// Ctrl-C in the engine window stops the whole rig instead of orphaning its seats.
func TestEngineInterrupt(t *testing.T) {
	e := newRigEnv(t, twoShellSeats)
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	e.waitFor(r.ID, "seat.up", 2)
	if err := e.srv.SendKeys(r.Session, "engine", "C-c"); err != nil {
		t.Fatal(err)
	}
	r = e.waitStatus(r.ID, state.Aborted)
	if r.Error != "engine interrupted" {
		t.Errorf("error %q", r.Error)
	}
	deadline := time.Now().Add(5 * time.Second)
	for e.srv.HasSession(r.Session) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if e.srv.HasSession(r.Session) {
		t.Error("the session outlived the run")
	}
}

// A run whose engine died (killed, then the whole tmux server gone, as in a reboot) is reported
// as such instead of looking live for ever, and abort still cleans it up.
func TestEngineDied(t *testing.T) {
	e := newRigEnv(t, twoShellSeats)
	e.mustLaight("run", "demo")
	r := e.onlyRun()
	e.waitFor(r.ID, "seat.up", 2)
	e.killEngine(r)
	if out, _ := e.laight("attach", r.Short()); !strings.Contains(out, "its engine died, so it will never finish; clean it up with: laight abort "+r.Short()) {
		t.Errorf("attach with a dead engine:\n%s", out)
	}
	e.srv.KillServer()
	if out, _ := e.laight("attach", r.Short()); !strings.Contains(out, "its engine died") || strings.Contains(out, "tmux session is gone") {
		t.Errorf("attach with no tmux server:\n%s", out)
	}
	if out := e.mustLaight("abort", r.Short(), "--yes"); !strings.Contains(out, "(demo, its engine died)") {
		t.Errorf("abort:\n%s", out)
	}
	if r := e.waitStatus(r.ID, state.Aborted); len(e.rigWorktrees(r)) != 0 {
		t.Errorf("on_finish.failure destroy kept %v", e.rigWorktrees(r))
	}
}

// When tmux can't say whether the engine lives (a socket it can't open), the run isn't called dead.
func TestEngineDiedCantSay(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if os.Getuid() == 0 {
		t.Skip("root connects to any socket")
	}
	dir, err := os.MkdirTemp("/tmp", "lt") // short: a socket path is capped near 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	sockets := filepath.Join(dir, fmt.Sprintf("tmux-%d", os.Getuid()))
	if err := os.Mkdir(sockets, 0o700); err != nil {
		t.Fatal(err)
	}
	// A socket nobody may connect to: "Permission denied" on Linux and macOS alike. (A plain
	// file reads as a stale socket on Linux, "no server running".)
	l, err := net.Listen("unix", filepath.Join(sockets, "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Chmod(filepath.Join(sockets, "file"), 0); err != nil {
		t.Fatal(err)
	}
	r := &state.Run{ID: "01AAAAAAAAAAAAAAAAAAAAAAAA", Status: state.Running, Session: "laight-demo-aaaaaa"}
	if engineDied(tmux.Server{Socket: "file"}, r) {
		t.Error("a socket tmux can't open reads as a dead engine")
	}
	if !engineDied(tmux.Server{Socket: "missing"}, r) {
		t.Error("no tmux server reads as a live engine")
	}
}

// killEngine kills the run's engine as a crash would, leaving its pane dead.
func (e *rigEnv) killEngine(r *state.Run) {
	e.t.Helper()
	out, err := exec.Command("tmux", "-L", e.srv.Socket, "display-message", "-p", "-t", "="+r.Session+":engine", "#{pane_pid}").Output()
	pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid == 0 {
		e.t.Fatalf("engine pid %q: %v", out, err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		e.t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); !e.srv.PaneDead(r.Session, "engine"); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			e.t.Fatal("the engine pane never died")
		}
	}
}

func (e *rigEnv) rigWorktrees(r *state.Run) []string {
	return rig.New(e.home, e.bin, nil).Worktrees(r)
}

func TestFormatDefault(t *testing.T) {
	for in, want := range map[any]string{1234567.0: "1234567", 1.5: "1.5", "x": "x", true: "true"} {
		if got := formatDefault(in); got != want {
			t.Errorf("formatDefault(%v) = %q, want %q", in, got, want)
		}
	}
}
