package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// stuckItem waits for the run's next pending item and checks it is a stuck one.
func (e *rigEnv) stuckItem(id string, choices ...string) *inbox.Item {
	e.t.Helper()
	it := e.waitItem(id)
	if it.Kind != inbox.Stuck || !slices.Equal(it.Choices, choices) {
		e.t.Fatalf("item %+v", it)
	}
	return it
}

// An agent that stops a second time without `laight done` is never left alone: the Stop hook
// marks it, and the engine asks a human at once, with no timeout set. It is at its prompt, so
// the choices are nudge or fail: a wait would wait for ever. A nudge pastes the reminder.
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
    objective: '"$L" done handoff'
`))
	e.mustLaight("run", "demo")
	id := e.onlyRun().ID
	it := e.stuckItem(id, inbox.Nudge, inbox.Fail)
	if it.Title != "a stopped without finishing step work" || it.Seat != "a" || it.Step != "work" ||
		!strings.Contains(it.Detail, "laight attach "+state.Short(id)+" a") {
		t.Errorf("item %+v", it)
	}
	e.mustLaight("inbox", it.Short(), inbox.Nudge)
	waitLog(t, e.home, id, "a", "laight: Step work is not finished. When it is, run: ")
	e.seatDone(id, "a", "handoff")
	// The reminder, pasted into sh, must leave no quote open: the next objective still runs.
	if r := e.waitEnded(id); r.Status != state.Succeeded || strings.Join(e.stepsDone(id), " ") != "work:handoff more:handoff" {
		t.Fatalf("status %s %q, steps %v", r.Status, r.Error, e.stepsDone(id))
	}
	evs, _ := state.Events(e.home, id)
	if countEvents(evs, "step.timeout", "") != 0 || countEvents(evs, "inbox.raised", "a") != 1 {
		t.Errorf("events %+v", evs)
	}
}

// A step quiet past its timeout raises a stuck item, not a kill, with no nudge: a paste could
// answer a dialog nobody saw. Wait starts the clock again; fail ends the step as failed, through
// routing, and interrupts the turn of a seat without hooks, which nothing else would end.
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
	first := e.stuckItem(id, inbox.Wait, inbox.Fail)
	if !strings.HasPrefix(first.Title, "a has been quiet for ") || !strings.Contains(first.Detail, "sleep 60") {
		t.Errorf("item %+v", first)
	}
	e.mustLaight("inbox", first.Short(), inbox.Wait)
	waited := time.Now()
	second := e.stuckItem(id, inbox.Wait, inbox.Fail)
	if second.ID == first.ID || second.Created.Sub(waited) < 1500*time.Millisecond { // a full timeout again, not a tick
		t.Errorf("asked again %s after the wait", second.Created.Sub(waited))
	}
	e.mustLaight("inbox", second.Short(), inbox.Fail)
	r := e.waitEnded(id)
	// The seat has no hooks, so its turn was interrupted: the next step on it ran at once.
	if r.Status != state.Succeeded || strings.Join(e.stepsDone(id), " ") != "work:failed after:handoff" ||
		r.Steps["work"].Out["error"] != "stuck; failed from the inbox" {
		t.Fatalf("status %s %q, steps %v %+v", r.Status, r.Error, e.stepsDone(id), r.Steps["work"])
	}
	evs, _ := state.Events(e.home, id)
	if countEvents(evs, "step.timeout", "a") != 2 {
		t.Errorf("events %+v", evs)
	}
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
    next_hop: { on: { failed: end } }
`))
	e.mustLaight("run", "demo")
	id := e.onlyRun().ID
	it := e.stuckItem(id, inbox.Wait, inbox.Fail)
	r, _ := state.Load(e.home, id)
	started := r.Active[0].Started
	if quiet := it.Created.Sub(started); quiet < 5*time.Second { // 4s of writes, then 2s quiet
		t.Errorf("stuck after %s, while it was still writing", quiet)
	}
	if _, err := os.Stat(filepath.Join(r.Seats["a"].Dir, "w7")); err != nil {
		t.Errorf("the writes didn't happen: %v", err)
	}
	e.mustLaight("inbox", it.Short(), inbox.Fail)
	e.waitEnded(id)
}

// A shell step quiet past its timeout can wait or fail; fail stops its command while the run
// goes on (here, to a gate).
func TestEngineShellStuck(t *testing.T) {
	e := newRigEnv(t, "")
	pids := filepath.Join(t.TempDir(), "pids")
	writeWorkflow(t, e, strings.ReplaceAll(`version: 1
id: demo
steps:
  - id: wait
    run: 'echo $$ > PIDS; sleep 300'
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
	it := e.stuckItem(id, inbox.Wait, inbox.Fail)
	if !strings.HasPrefix(it.Title, "shell step wait has been quiet for ") || it.Seat != "" {
		t.Errorf("item %+v", it)
	}
	e.mustLaight("inbox", it.Short(), inbox.Wait)
	waited := time.Now()
	it = e.stuckItem(id, inbox.Wait, inbox.Fail)
	if d := it.Created.Sub(waited); d < 1500*time.Millisecond {
		t.Errorf("asked again %s after the wait", d)
	}
	e.mustLaight("inbox", it.Short(), inbox.Fail)
	gate := e.waitItem(id)
	if gate.Kind != inbox.Gate {
		t.Fatalf("item %+v", gate)
	}
	waitGone(t, pid) // stopped by the fail, not by the run ending
	e.mustLaight("inbox", gate.Short(), "yes")
	if r := e.waitEnded(id); r.Status != state.Succeeded || strings.Join(e.stepsDone(id), " ") != "wait:failed hold:yes" {
		t.Fatalf("status %s %q, steps %v", r.Status, r.Error, e.stepsDone(id))
	}
}

// The pane quoted in a stuck item is stripped of the seat's secret env values (§12.4), even
// where they don't look like a token.
func TestEngineStuckRedactsPane(t *testing.T) {
	t.Setenv("LAIGHT_SECRET_TOK", "plain-words-secret")
	e := newRigEnv(t, "")
	writeWorkflow(t, e, fakeWorkflow(e.bin, `seats:
  a: { runtime: shell, command: sh, cwd: repo, env: { TOK: '{{ secret "tok" }}' } }
steps:
  - id: work
    seat: a
    objective: 'echo "tok=$TOK"; echo "{\"stop_hook_active\": true}" | "$L" hook stop'
    next_hop: { on: { failed: end } }
`))
	e.mustLaight("run", "demo")
	id := e.onlyRun().ID
	waitLog(t, e.home, id, "a", "tok=plain-words-secret")
	it := e.stuckItem(id, inbox.Nudge, inbox.Fail)
	if strings.Contains(it.Detail, "plain-words-secret") || !strings.Contains(it.Detail, "tok=***") {
		t.Errorf("detail:\n%s", it.Detail)
	}
	e.mustLaight("inbox", it.Short(), inbox.Fail)
	e.waitEnded(id)
}
