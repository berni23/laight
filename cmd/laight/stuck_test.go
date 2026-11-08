package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/state"
)

// seatDone runs `laight done` as seat would, from outside its pane.
func (e *rigEnv) seatDone(id, seat string, args ...string) {
	e.t.Helper()
	c := exec.Command(e.bin, append([]string{"done"}, args...)...)
	c.Env = append(os.Environ(), "LAIGHT_HOME="+e.home, "LAIGHT_RUN="+id, "LAIGHT_SEAT="+seat)
	if out, err := c.CombinedOutput(); err != nil {
		e.t.Fatalf("laight done %v: %v %s", args, err, out)
	}
}

// seatStops runs the seat's Stop hook from outside its pane, as Claude would after its own
// reminder: the second stop without `laight done`.
func (e *rigEnv) seatStops(id, seat string) {
	e.t.Helper()
	c := exec.Command(e.bin, "hook", "stop")
	c.Env = append(os.Environ(), "LAIGHT_HOME="+e.home, "LAIGHT_RUN="+id, "LAIGHT_SEAT="+seat)
	c.Stdin = strings.NewReader(`{"stop_hook_active": true}`)
	if out, err := c.CombinedOutput(); err != nil {
		e.t.Fatalf("laight hook stop: %v %s", err, out)
	}
}

// noItems checks that the run asked nobody anything.
func (e *rigEnv) noItems(id string) {
	e.t.Helper()
	evs, _ := state.Events(e.home, id)
	if n := countEvents(evs, "inbox.raised", ""); n != 0 {
		e.t.Errorf("%d inbox items raised: %+v", n, evs)
	}
}

// An agent that stops a second time without `laight done` is reminded once by the engine, with
// no timeout set; the reminder, pasted into sh, leaves no quote open. If it then finishes, the
// run goes on; if it stops again, its step fails as stuck and the run ends red. Nobody is asked.
func TestEngineStuckStopped(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - id: work
    seat: a
    objective: 'echo "{\"stop_hook_active\": true}" | "$L" hook stop'
    next_hop: { on: { handoff: more } }
  - id: more
    seat: a
    objective: 'echo "{\"stop_hook_active\": true}" | "$L" hook stop'
`))
	e.mustLaight("run", "demo")
	id := e.onlyRun().ID
	waitLog(t, e.home, id, "a", "laight: Step work is not finished. When it is, run: ")
	e.seatDone(id, "a", "handoff")
	e.waitFor(id, "step.nudged", 2) // more stopped too, and was reminded
	e.seatStops(id, "a")
	r := e.waitFailed(id, state.KindStuck)
	if r.Failure.Step != "more" || r.Failure.Seat != "a" || !strings.Contains(r.Failure.Detail, "laight: Step more is not finished") {
		t.Errorf("failure %+v", r.Failure)
	}
	if got := strings.Join(e.stepsDone(id), " "); got != "work:handoff more:failed" {
		t.Errorf("steps %s", got)
	}
	e.noItems(id)
}

// A step quiet past its timeout fails as a timeout, with no nudge (a paste could answer a dialog
// nobody saw), through routing; a seat without hooks has its turn interrupted, since nothing else
// would end it, so the next step on it runs at once.
func TestEngineStuckTimeout(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo }
steps:
  - id: work
    seat: a
    objective: 'sleep 60'
    timeout: 2s
    next_hop: { on: { failed: after } }
  - id: after
    seat: a
    objective: '"$L" done handoff'
`))
	e.mustLaight("run", "demo")
	id := e.onlyRun().ID
	r := e.waitEnded(id)
	if r.Status != state.Succeeded || strings.Join(e.stepsDone(id), " ") != "work:failed after:handoff" ||
		!strings.HasPrefix(fmt.Sprint(r.Steps["work"].Out["error"]), "a was quiet for ") {
		t.Fatalf("status %s %q, steps %v %+v", r.Status, r.Error, e.stepsDone(id), r.Steps["work"])
	}
	evs, _ := state.Events(e.home, id)
	if countEvents(evs, "step.timeout", "a") != 1 {
		t.Errorf("events %+v", evs)
	}
	e.noItems(id)
}

// A seat quiet in its pane but still writing to its worktree (a long build, a test run) is not
// stuck until the writes stop (#15).
func TestEngineStuckWorktreeWrites(t *testing.T) {
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh }
steps:
  - id: work
    seat: a
    objective: 'i=0; while [ $i -lt 8 ]; do touch w$i; i=$((i+1)); sleep 0.5; done; sleep 60'
    timeout: 2s
`))
	e.mustLaight("run", "demo")
	id := e.onlyRun().ID
	e.waitFor(id, "step.started", 1)
	started := time.Now()
	r := e.waitFailed(id, state.KindTimeout)
	if quiet := r.Failure.At.Sub(started); quiet < 5*time.Second { // 4s of writes, then 2s quiet
		t.Errorf("timed out after %s, while it was still writing", quiet)
	}
	if !strings.Contains(r.Failure.Detail, "sleep 60") {
		t.Errorf("detail:\n%s", r.Failure.Detail)
	}
}

// A shell step quiet past its timeout fails, which stops its command, while the run goes on
// (here, to a gate).
func TestEngineShellStuck(t *testing.T) {
	e := newRigEnv(t, "")
	pids := filepath.Join(t.TempDir(), "pids")
	writeWorkflow(t, e, strings.ReplaceAll(`version: 1
id: demo
steps:
  - id: wait
    run: 'echo $$ > PIDS; echo still-going; sleep 300'
    timeout: 2s
    next_hop: { on: { failed: hold } }
  - id: hold
    type: gate
    ask: Go on?
    allowed_exits: [yes]
`, "PIDS", pids))
	e.mustLaight("run", "demo")
	id := e.onlyRun().ID
	pid := readPid(t, pids)
	gate := e.waitItem(id)
	if gate.Kind != inbox.Gate {
		t.Fatalf("item %+v", gate)
	}
	waitGone(t, pid) // stopped by the timeout, not by the run ending
	e.mustLaight("inbox", gate.Short(), "yes")
	r := e.waitEnded(id)
	if r.Status != state.Succeeded || strings.Join(e.stepsDone(id), " ") != "wait:failed hold:yes" ||
		!strings.HasPrefix(fmt.Sprint(r.Steps["wait"].Out["error"]), "shell step wait was quiet for ") {
		t.Fatalf("status %s %q, steps %v %+v", r.Status, r.Error, e.stepsDone(id), r.Steps["wait"])
	}
}

// The pane quoted in a failure report is stripped of the seat's secret env values (§12.4), even
// where they don't look like a token.
func TestEngineStuckRedactsPane(t *testing.T) {
	t.Setenv("LAIGHT_SECRET_TOK", "plain-words-secret")
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo, env: { TOK: '{{ secret "tok" }}' } }
steps:
  - id: work
    seat: a
    objective: 'echo "tok=$TOK"; sleep 60'
    timeout: 2s
`))
	e.mustLaight("run", "demo")
	id := e.onlyRun().ID
	r := e.waitFailed(id, state.KindTimeout)
	if d := r.Failure.Detail; strings.Contains(d, "plain-words-secret") || !strings.Contains(d, "tok=***") {
		t.Errorf("detail:\n%s", d)
	}
	evs, _ := state.Events(e.home, id)
	for _, ev := range evs {
		if ev.Type == "run.failed" && strings.Contains(fmt.Sprint(ev.Data), "plain-words-secret") {
			t.Errorf("secret in run.failed: %+v", ev)
		}
	}
}
