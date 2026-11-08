package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/rig"
	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// hooked is a run whose seats are Claude Code seats (so they have a Stop hook), already up: each
// one's pane is a plain sh on a throwaway tmux server. Tests drive advance/tick directly, so no
// agent is ever launched.
func hooked(t *testing.T, steps []*spec.Step, seats ...string) (*Engine, string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	home := t.TempDir()
	g := rig.New(home, "", nil)
	t.Cleanup(func() { g.Tmux.KillServer() })
	id := state.NewID(time.Now())
	w := &spec.Workflow{ID: "wf", Seats: map[string]*spec.Seat{}, Steps: steps, LoopGuards: spec.LoopGuards{MaxHops: 50}}
	r := &state.Run{ID: id, Workflow: "wf", Session: state.SessionName("wf", id), Spec: w, Status: state.Running, Seats: map[string]*state.Seat{}}
	for i, name := range seats {
		w.Seats[name] = &spec.Seat{Runtime: "claude-code", Session: "resume"}
		r.Seats[name] = &state.Seat{Window: name, Up: true}
		var err error
		if i == 0 {
			err = g.Tmux.NewSession(r.Session, tmux.Window{Name: name, Cmd: "sh"})
		} else {
			err = g.Tmux.NewWindow(r.Session, tmux.Window{Name: name, Cmd: "sh"})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := state.Create(home, r); err != nil {
		t.Fatal(err)
	}
	return &Engine{Home: home, Rig: g}, id
}

// step advances the run once under its lock, like tick, without delivering anything.
func (e *Engine) step(t *testing.T, id string) (string, []start) {
	t.Helper()
	var status string
	var starts []start
	if _, err := state.Update(e.Home, id, func(r *state.Run) error {
		status, _, starts, _, _ = e.advance(r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return status, starts
}

func load(t *testing.T, e *Engine, id string) *state.Run {
	t.Helper()
	r, err := state.Load(e.Home, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func two(on map[string]string) []*spec.Step {
	return []*spec.Step{
		{ID: "one", Type: spec.TypeAgent, Seat: "a", Objective: "true", AllowedExits: []string{"handoff"}, NextHop: spec.NextHop{On: on}},
		{ID: "two", Type: spec.TypeAgent, Seat: "a", Objective: "true", AllowedExits: []string{"handoff"}},
	}
}

// A hooked seat gets its next step only once its Stop hook says the turn ended.
func TestHookedSeatWaitsForStop(t *testing.T) {
	e, id := hooked(t, two(map[string]string{"handoff": "two"}), "a")
	if _, starts := e.step(t, id); len(starts) != 1 || starts[0].hop.Step != "one" {
		t.Fatalf("starts %+v", starts)
	}
	if err := state.WriteDone(e.Home, id, state.Done{Step: "one", Hop: 1, Exit: "handoff"}); err != nil {
		t.Fatal(err)
	}
	if _, starts := e.step(t, id); len(starts) != 0 {
		t.Fatalf("two started before the turn ended: %+v", starts)
	}
	if r := load(t, e, id); len(r.Queue) != 1 || !r.Seats["a"].Turn {
		t.Fatalf("queue %v, seat %+v", r.Queue, r.Seats["a"])
	}
	mark := filepath.Join(state.Dir(e.Home, id), "seats", "a.idle")
	if err := os.WriteFile(mark, []byte(IdleMark("one", 1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, starts := e.step(t, id); len(starts) != 1 || starts[0].hop.Step != "two" || starts[0].restart {
		t.Fatalf("starts %+v", starts)
	}
}

// An ending the engine wrote (an objective that didn't render) has no turn behind it: the next
// step on the same hooked seat must not wait for a Stop hook that will never come.
func TestEngineFailureFreesHookedSeat(t *testing.T) {
	e, id := hooked(t, two(map[string]string{"failed": "two"}), "a")
	e.step(t, id)
	if err := state.WriteDone(e.Home, id, state.Done{Step: "one", Hop: 1, Exit: spec.ExitFailed, Engine: true}); err != nil {
		t.Fatal(err)
	}
	if _, starts := e.step(t, id); len(starts) != 1 || starts[0].hop.Step != "two" {
		t.Fatalf("two didn't start: %+v", starts)
	}
}

// An agent that runs `laight done` and then exits never runs its Stop hook: the seat is free,
// and its next step relaunches it.
func TestHookedSeatExitsAfterDone(t *testing.T) {
	e, id := hooked(t, two(map[string]string{"handoff": "two"}), "a")
	e.step(t, id)
	if err := state.WriteDone(e.Home, id, state.Done{Step: "one", Hop: 1, Exit: "handoff"}); err != nil {
		t.Fatal(err)
	}
	r := load(t, e, id)
	if err := e.Rig.Tmux.KillWindow(r.Session, "a"); err != nil {
		t.Fatal(err)
	}
	if _, starts := e.step(t, id); len(starts) != 1 || starts[0].hop.Step != "two" || !starts[0].restart {
		t.Fatalf("starts %+v", starts)
	}
}

// A crash is written to out/ like any other ending, and a `laight done` already there wins.
func TestCrashIsRecorded(t *testing.T) {
	steps := []*spec.Step{
		{ID: "one", Type: spec.TypeAgent, Seat: "a", Objective: "true", AllowedExits: []string{"handoff"}, NextHop: spec.NextHop{On: map[string]string{"failed": "end"}}},
		{ID: "two", Type: spec.TypeAgent, Seat: "b", AllowedExits: []string{"handoff"}},
	}
	e, id := hooked(t, steps, "a", "b")
	e.step(t, id)
	r := load(t, e, id)
	// Seat a crashes mid-step; seat b finished its step and then crashed.
	if err := state.WriteDone(e.Home, id, state.Done{Step: "two", Hop: 2, Exit: "handoff"}); err != nil {
		t.Fatal(err)
	}
	e.Rig.Tmux.KillWindow(r.Session, "a")
	e.Rig.Tmux.KillWindow(r.Session, "b")
	e.step(t, id)
	d, err := state.ReadDone(e.Home, id, "one", 1)
	if err != nil || d == nil || d.Exit != spec.ExitFailed || !d.Engine || d.Out["error"] != "seat a crashed" {
		t.Errorf("one: %+v %v", d, err)
	}
	if d, _ := state.ReadDone(e.Home, id, "two", 2); d == nil || d.Exit != "handoff" || d.Engine {
		t.Errorf("two: %+v", d)
	}
}

// When tmux can't answer, a live step is not declared crashed.
func TestTmuxErrorIsNotACrash(t *testing.T) {
	e, id := hooked(t, two(nil), "a")
	e.step(t, id)
	bin := t.TempDir()
	fake := "#!/bin/sh\necho 'server exited unexpectedly' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	e.step(t, id)
	if d, _ := state.ReadDone(e.Home, id, "one", 1); d != nil {
		t.Errorf("a tmux hiccup failed the step: %+v", d)
	}
	if r := load(t, e, id); len(r.Active) != 1 {
		t.Errorf("active %+v", r.Active)
	}
}

// An unrouted failure ends the run red, with its failure report saved with the state: an engine
// that runs on the same files later gets the same verdict, never an empty queue and a success.
func TestFailedRunEndsRed(t *testing.T) {
	e, id := hooked(t, two(map[string]string{"handoff": "two"}), "a")
	ctx := context.Background()
	if status, _, err := e.tick(ctx, id); status != "" || err != nil {
		t.Fatalf("tick: %s %v", status, err)
	}
	if err := state.WriteDone(e.Home, id, state.Done{Step: "one", Hop: 1, Exit: spec.ExitFailed, Out: map[string]any{"reason": "no tests to run"}}); err != nil {
		t.Fatal(err)
	}
	status, reason, err := e.tick(ctx, id)
	if status != state.Failed || reason != "step one failed: no tests to run" || err != nil {
		t.Fatalf("tick: %s %q %v", status, reason, err)
	}
	r := load(t, e, id)
	f := r.Failure
	if r.Status != state.Failed || f == nil || f.Step != "one" || f.Hop != 1 || f.Seat != "a" || f.Kind != state.KindExit ||
		f.Reason != reason || f.At.IsZero() {
		t.Fatalf("run.json: %s %+v", r.Status, f)
	}
	if status, again, _ := e.tick(ctx, id); status != state.Failed || again != reason {
		t.Errorf("second engine: %s %q", status, again)
	}
}

// Once a step fails, nothing new starts: running siblings finish and are recorded, their routes
// and the steps that need them never start, and the run ends red once nothing runs (§12.3).
func TestSiblingsFinishAfterFailure(t *testing.T) {
	steps := []*spec.Step{agent("one", "a", nil), agent("two", "b", map[string]string{"handoff": "after"}),
		agent("after", "b", nil), agent("join", "c", nil, "one", "two")}
	e, id := hooked(t, steps, "a", "b", "c")
	_, starts := e.step(t, id)
	if strings.Join(started(starts), ",") != "one,two" {
		t.Fatalf("started %v", started(starts))
	}
	finish(t, e, id, starts[0].hop, spec.ExitFailed)
	if status, s := e.step(t, id); status != "" || len(s) != 0 {
		t.Fatalf("status %q, started %v while two runs", status, started(s))
	}
	finish(t, e, id, starts[1].hop, "handoff")
	status, s := e.step(t, id)
	if status != state.Failed || len(s) != 0 {
		t.Fatalf("status %q, started %v", status, started(s))
	}
	r := load(t, e, id)
	if r.Steps["two"] == nil || r.Steps["two"].Exit != "handoff" || r.Failure == nil || r.Failure.Step != "one" {
		t.Fatalf("steps %+v, failure %+v", r.Steps, r.Failure)
	}
}

// A failing run starts no step whose needs are met by another branch, and a sibling that fails
// too doesn't replace the first failure in the report.
func TestFailingRunStartsNothing(t *testing.T) {
	steps := []*spec.Step{agent("one", "a", nil), agent("two", "b", nil), agent("three", "c", nil), agent("after", "d", nil, "two")}
	e, id := hooked(t, steps, "a", "b", "c", "d")
	_, starts := e.step(t, id)
	if strings.Join(started(starts), ",") != "one,two,three" {
		t.Fatalf("started %v", started(starts))
	}
	finish(t, e, id, starts[0].hop, spec.ExitFailed)
	finish(t, e, id, starts[1].hop, "handoff")
	if status, s := e.step(t, id); status != "" || len(s) != 0 {
		t.Fatalf("status %q, started %v: after's needs are met, but the run is failing", status, started(s))
	}
	finish(t, e, id, starts[2].hop, spec.ExitFailed)
	if status, _ := e.step(t, id); status != state.Failed {
		t.Fatalf("status %q", status)
	}
	if f := load(t, e, id).Failure; f == nil || f.Step != "one" {
		t.Fatalf("failure %+v: the first failure is the report", f)
	}
}

// An open gate doesn't keep a failed run alive: its exit could start nothing, and nobody is
// watching it (#80 review 5).
func TestFailedRunEndsDespiteOpenGate(t *testing.T) {
	e, id := hooked(t, []*spec.Step{agent("one", "a", nil), {ID: "g", Type: spec.TypeGate, Ask: "Go?", AllowedExits: []string{"yes"}}}, "a")
	_, starts := e.step(t, id)
	if r := load(t, e, id); len(starts) != 1 || len(r.Active) != 2 {
		t.Fatalf("starts %+v, active %+v", starts, r.Active)
	}
	finish(t, e, id, starts[0].hop, spec.ExitFailed)
	if status, _ := e.step(t, id); status != state.Failed {
		t.Fatalf("status %q with only a gate open", status)
	}
}

// The failure report is redacted where it is built: the seat's secrets and anything that looks
// like a token, in the reason and the detail, and control characters stripped (#80 review 6, 10).
func TestFailureRedacted(t *testing.T) {
	t.Setenv("LAIGHT_SECRET_TOK", "plain-words-secret")
	e, id := hooked(t, two(nil), "a")
	state.Update(e.Home, id, func(r *state.Run) error {
		r.Spec.Seats["a"].Env = spec.StringMap{"TOK": `{{ secret "tok" }}`}
		return nil
	})
	e.step(t, id)
	token := "ghp_" + strings.Repeat("aB3", 12)
	if err := state.WriteDone(e.Home, id, state.Done{Step: "one", Hop: 1, Exit: spec.ExitFailed, Engine: true,
		Out:    map[string]any{"reason": "auth failed: plain-words-secret " + token + " \x1b[31mred"},
		Detail: "tok=plain-words-secret\n\x1b]0;title\x07done"}); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.step(t, id); status != state.Failed {
		t.Fatalf("status %q", status)
	}
	r := load(t, e, id)
	for _, got := range []string{r.Failure.Reason, r.Failure.Detail, r.Error} {
		if strings.Contains(got, "plain-words-secret") || strings.Contains(got, token) || strings.ContainsAny(got, "\x1b\x07") {
			t.Errorf("not redacted: %q", got)
		}
	}
	if !strings.Contains(r.Failure.Reason, "auth failed: ***") || !strings.Contains(r.Failure.Detail, "tok=***\n") {
		t.Errorf("failure %+v", r.Failure)
	}
}

// An agent that gives up after a denied request fails as `permission`, so a postmortem can tell a
// tight deny list from an impossible task (#80 review 7).
func TestFailureAfterDenialIsPermission(t *testing.T) {
	e, id := hooked(t, two(nil), "a")
	_, starts := e.step(t, id)
	if err := state.Append(e.Home, state.Event{Run: id, Type: "permission.denied", Step: "one", Seat: "a", Hop: 1,
		Data: map[string]any{"tool": "Bash", "input": "git push --force", "by": "laight"}}); err != nil {
		t.Fatal(err)
	}
	finish(t, e, id, starts[0].hop, spec.ExitFailed)
	e.step(t, id)
	if f := load(t, e, id).Failure; f == nil || f.Kind != state.KindPermission {
		t.Fatalf("failure %+v", f)
	}
}

// A step max_hops refuses fails the run, saying which step it refused.
func TestMaxHopsFailsRun(t *testing.T) {
	e, id := hooked(t, two(map[string]string{"handoff": "two"}), "a")
	state.Update(e.Home, id, func(r *state.Run) error { r.Spec.LoopGuards.MaxHops = 1; return nil })
	_, starts := e.step(t, id)
	finish(t, e, id, starts[0].hop, "handoff")
	if status, s := e.step(t, id); status != state.Failed || len(s) != 0 {
		t.Fatalf("status %q, started %v", status, started(s))
	}
	if f := load(t, e, id).Failure; f == nil || f.Kind != state.KindMaxHops || f.Step != "two" || !strings.Contains(f.Reason, "max_hops (1)") {
		t.Fatalf("failure %+v", f)
	}
}

// breakAnswer leaves an item answered with a file nobody can read, as outside damage would.
func breakAnswer(t *testing.T, home, item string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(home, "inbox", "answered"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "inbox", "answered", item+".json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(home, "inbox", item+".json"))
}

// A gate whose answer can't be read fails, through routing like any other ending.
func TestUnreadableGateAnswer(t *testing.T) {
	e, id := hooked(t, []*spec.Step{{ID: "g", Type: spec.TypeGate, Ask: "Go?", AllowedExits: []string{"yes", "no"}}})
	e.step(t, id)
	r := load(t, e, id)
	if len(r.Active) != 1 || r.Active[0].Item == "" {
		t.Fatalf("active %+v", r.Active)
	}
	breakAnswer(t, e.Home, r.Active[0].Item)
	if status, _ := e.step(t, id); status != state.Failed {
		t.Fatalf("status %q", status)
	}
	r = load(t, e, id)
	if res := r.Steps["g"]; res == nil || res.Exit != spec.ExitFailed || r.Failure == nil || r.Failure.Kind != state.KindCrash ||
		!strings.Contains(r.Failure.Reason, "can't read the answer") {
		t.Fatalf("steps %+v failure %+v", r.Steps["g"], r.Failure)
	}
}

// finish ends a hop as its agent would: `laight done`, then the Stop hook's idle mark.
func finish(t *testing.T, e *Engine, id string, h state.Hop, exit string) {
	t.Helper()
	if err := state.WriteDone(e.Home, id, state.Done{Step: h.Step, Hop: h.N, Exit: exit}); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(state.Dir(e.Home, id), "seats", h.Seat+".idle")
	if err := os.WriteFile(mark, []byte(IdleMark(h.Step, h.N)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// started returns the ids of the steps a tick started, in order.
func started(starts []start) []string {
	var ids []string
	for _, s := range starts {
		ids = append(ids, s.hop.Step)
	}
	return ids
}

func agent(id, seat string, on map[string]string, needs ...string) *spec.Step {
	return &spec.Step{ID: id, Type: spec.TypeAgent, Seat: seat, Objective: "true",
		AllowedExits: []string{"handoff", "pass", "fail"}, NextHop: spec.NextHop{On: on}, Needs: needs}
}

// Rule 2: a step with needs starts once every needed step has finished, not before.
func TestNeedsWaitForEvery(t *testing.T) {
	e, id := hooked(t, []*spec.Step{agent("one", "a", nil), agent("two", "b", nil), agent("join", "c", nil, "one", "two")}, "a", "b", "c")
	_, starts := e.step(t, id)
	if got := started(starts); strings.Join(got, ",") != "one,two" {
		t.Fatalf("started %v", got)
	}
	finish(t, e, id, starts[0].hop, "handoff")
	if _, s := e.step(t, id); len(s) != 0 {
		t.Fatalf("join started with two still running: %v", started(s))
	}
	finish(t, e, id, starts[1].hop, "handoff")
	if _, s := e.step(t, id); strings.Join(started(s), ",") != "join" {
		t.Fatalf("started %v", started(s))
	}
}

// A needed step inside a loop has finished only once the loop has left it: a `fail` that routes
// back to the fixer doesn't release the step that needs the check.
func TestNeedsWaitForLoop(t *testing.T) {
	steps := []*spec.Step{
		agent("fix", "a", map[string]string{"handoff": "check"}),
		agent("check", "b", map[string]string{"fail": "fix", "pass": "end"}),
		agent("ship", "c", nil, "check"),
	}
	e, id := hooked(t, steps, "a", "b", "c")
	_, s := e.step(t, id)
	finish(t, e, id, s[0].hop, "handoff") // fix
	_, s = e.step(t, id)
	finish(t, e, id, s[0].hop, "fail") // check → fix
	if _, s = e.step(t, id); strings.Join(started(s), ",") != "fix" {
		t.Fatalf("after check failed, started %v", started(s))
	}
	finish(t, e, id, s[0].hop, "handoff")
	_, s = e.step(t, id)
	finish(t, e, id, s[0].hop, "pass")
	if _, s = e.step(t, id); strings.Join(started(s), ",") != "ship" {
		t.Fatalf("after check passed, started %v", started(s))
	}
}

// Steps whose needs are met in the same tick are released independently of their order in the
// file: one that can send the needed step round again (y) holds the other (z) until it ends.
func TestNeedsReleaseIgnoresFileOrder(t *testing.T) {
	for _, order := range []string{"x,z,y", "x,y,z"} {
		byID := map[string]*spec.Step{
			"x": agent("x", "a", nil),
			"y": agent("y", "b", map[string]string{"fail": "x"}, "x"),
			"z": agent("z", "c", nil, "x"),
		}
		var steps []*spec.Step
		for _, id := range strings.Split(order, ",") {
			steps = append(steps, byID[id])
		}
		e, id := hooked(t, steps, "a", "b", "c")
		_, s := e.step(t, id)
		finish(t, e, id, s[0].hop, "handoff") // x
		if _, s = e.step(t, id); strings.Join(started(s), ",") != "y" {
			t.Fatalf("%s: after x, started %v", order, started(s))
		}
		finish(t, e, id, s[0].hop, "handoff") // y ends its branch
		if _, s = e.step(t, id); strings.Join(started(s), ",") != "z" {
			t.Fatalf("%s: after y, started %v", order, started(s))
		}
	}
}

// Two steps in the same loop back to what they need don't hold each other: both are released.
func TestNeedsLoopMembersReleaseTogether(t *testing.T) {
	steps := []*spec.Step{
		agent("x", "a", nil),
		agent("y1", "b", map[string]string{"fail": "x"}, "x"),
		agent("y2", "c", map[string]string{"fail": "x"}, "x"),
	}
	e, id := hooked(t, steps, "a", "b", "c")
	_, s := e.step(t, id)
	finish(t, e, id, s[0].hop, "handoff")
	if _, s = e.step(t, id); strings.Join(started(s), ",") != "y1,y2" {
		t.Fatalf("started %v", started(s))
	}
}

// A step whose needs can never be met (the needed step failed and was routed elsewhere) is
// skipped when the run ends, and the run still succeeds.
func TestNeedsUnmetIsSkipped(t *testing.T) {
	steps := []*spec.Step{
		agent("one", "a", map[string]string{"failed": "cleanup"}),
		agent("cleanup", "b", nil),
		agent("join", "c", nil, "one"),
	}
	e, id := hooked(t, steps, "a", "b", "c")
	_, s := e.step(t, id)
	finish(t, e, id, s[0].hop, spec.ExitFailed)
	_, s = e.step(t, id)
	if strings.Join(started(s), ",") != "cleanup" {
		t.Fatalf("started %v", started(s))
	}
	finish(t, e, id, s[0].hop, "handoff")
	var status string
	var events []state.Event
	if _, err := state.Update(e.Home, id, func(r *state.Run) error {
		status, _, _, _, events = e.advance(r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if status != state.Succeeded {
		t.Fatalf("status %q", status)
	}
	var skipped []state.Event
	for _, ev := range events {
		if ev.Type == "step.skipped" {
			skipped = append(skipped, ev)
		}
	}
	if len(skipped) != 1 || skipped[0].Step != "join" || fmt.Sprint(skipped[0].Data["needs"]) != "[one]" {
		t.Fatalf("skipped %+v", skipped)
	}
}

func matrixStep(id, seat string, max int, values any, needs ...string) *spec.Step {
	st := agent(id, seat, nil, needs...)
	st.Matrix, st.MaxParallel = map[string]any{"task": values}, max
	return st
}

// matrixSeat declares a Claude Code seat that only matrix steps use, and gives its first n
// instances a window, as delivering their copies would.
func matrixSeat(t *testing.T, e *Engine, id, name string, n int) {
	t.Helper()
	r, err := state.Update(e.Home, id, func(r *state.Run) error {
		r.Spec.Seats[name] = &spec.Seat{Runtime: "claude-code", Session: "resume"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for k := 1; k <= n; k++ {
		w := tmux.Window{Name: spec.Instance(name, k), Cmd: "sh"}
		err := e.Rig.Tmux.NewWindow(r.Session, w)
		if !e.Rig.Tmux.HasSession(r.Session) {
			err = e.Rig.Tmux.NewSession(r.Session, w)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

// hopOf returns the started hop of copy k of step.
func hopOf(t *testing.T, starts []start, step string, k int) state.Hop {
	t.Helper()
	for _, s := range starts {
		if s.hop.Step == step && s.hop.Copy == k {
			return s.hop
		}
	}
	t.Fatalf("copy %d of %s didn't start: %+v", k, step, starts)
	return state.Hop{}
}

// Copies run on their own seat instances, at most max_parallel at once, each with its .matrix;
// an instance closes once its copy's turn ends.
func TestMatrixMaxParallel(t *testing.T) {
	e, id := hooked(t, []*spec.Step{matrixStep("build", "dev", 2, []any{"a", "b", "c"})})
	matrixSeat(t, e, id, "dev", 3)
	_, starts := e.step(t, id)
	if len(starts) != 2 {
		t.Fatalf("starts %+v", starts)
	}
	for k, s := range starts {
		if s.hop.Seat != spec.Instance("dev", k+1) || !s.restart || s.hop.Matrix["task"] != []string{"a", "b"}[k] {
			t.Errorf("copy %d: %+v", k+1, s)
		}
	}
	state.Update(e.Home, id, func(r *state.Run) error { r.Seats["dev#1"].Up = true; return nil })
	finish(t, e, id, hopOf(t, starts, "build", 1), "handoff")
	var stops []string
	state.Update(e.Home, id, func(r *state.Run) error { _, _, starts, stops, _ = e.advance(r); return nil })
	if h := hopOf(t, starts, "build", 3); len(starts) != 1 || h.Seat != "dev#3" || h.Matrix["task"] != "c" {
		t.Errorf("starts %+v", starts)
	}
	if strings.Join(stops, ",") != "dev#1" {
		t.Errorf("stops %v", stops)
	}
}

// A failed copy fails the run once its running siblings finish: the step never completes, and
// what needs it never starts. The report names the copy.
func TestMatrixFailedCopy(t *testing.T) {
	join := agent("join", "lead", nil, "build")
	e, id := hooked(t, []*spec.Step{matrixStep("build", "dev", 4, []any{"a", "b", "c"}), join}, "lead")
	matrixSeat(t, e, id, "dev", 3)
	_, starts := e.step(t, id)
	if err := state.WriteDone(e.Home, id, state.Done{Step: "build", Hop: hopOf(t, starts, "build", 1).N, Exit: "handoff"}); err != nil {
		t.Fatal(err)
	}
	finish(t, e, id, hopOf(t, starts, "build", 2), spec.ExitFailed)
	if status, s := e.step(t, id); status != "" || len(s) != 0 {
		t.Fatalf("status %q, started %+v while copy 3 runs", status, s)
	}
	if err := state.WriteDone(e.Home, id, state.Done{Step: "build", Hop: hopOf(t, starts, "build", 3).N, Exit: "handoff"}); err != nil {
		t.Fatal(err)
	}
	if status, s := e.step(t, id); status != state.Failed || len(s) != 0 {
		t.Fatalf("status %q, started %+v", status, s)
	}
	r := load(t, e, id)
	if f := r.Failure; f == nil || f.Step != "build" || f.Copy != 2 || f.Seat != "dev#2" || f.Reason != "step build#2 failed" {
		t.Fatalf("failure %+v", r.Failure)
	}
	if r.Steps["build"] != nil || r.Steps["join"] != nil {
		t.Errorf("steps %+v", r.Steps)
	}
}

// A matrix that doesn't expand fails the run before any copy starts.
func TestMatrixBadList(t *testing.T) {
	e, id := hooked(t, []*spec.Step{matrixStep("build", "dev", 4, "{{ .base }}")})
	if status, s := e.step(t, id); status != state.Failed || len(s) != 0 {
		t.Fatalf("status %q, started %+v", status, s)
	}
	if r := load(t, e, id); r.Failure == nil || r.Failure.Step != "build" || !strings.Contains(r.Failure.Reason, "step build: matrix task:") || r.Hops != 0 {
		t.Fatalf("failure %+v, hops %d", r.Failure, r.Hops)
	}
}

// A route into a matrix step whose copies are still running waits for them: it doesn't expand
// the step again over the live copies.
func TestMatrixRoutedWhileRunning(t *testing.T) {
	steps := []*spec.Step{
		agent("a", "x", map[string]string{"handoff": "build"}),
		agent("b", "y", map[string]string{"handoff": "build"}),
		matrixStep("build", "dev", 4, []any{"p", "q"}),
	}
	e, id := hooked(t, steps, "x", "y")
	matrixSeat(t, e, id, "dev", 2)
	_, first := e.step(t, id) // a and b
	finish(t, e, id, hopOf(t, first, "a", 0), "handoff")
	if _, s := e.step(t, id); strings.Join(started(s), ",") != "build,build" {
		t.Fatalf("started %v", started(s))
	}
	finish(t, e, id, hopOf(t, first, "b", 0), "handoff")
	var s []start
	if _, s = e.step(t, id); len(s) != 0 {
		t.Fatalf("started %+v", started(s))
	}
	if r := load(t, e, id); len(r.Matrix["build"].Pending) != 0 || len(r.Queue) != 1 {
		t.Fatalf("matrix %+v, queue %v", r.Matrix["build"], r.Queue)
	}
}

// A compact step pastes /compact first: Claude queues the objective behind it. Any other step
// pastes only its objective.
func TestDeliverCompactFirst(t *testing.T) {
	for _, compact := range []bool{true, false} {
		t.Run(fmt.Sprint("compact=", compact), func(t *testing.T) {
			steps := []*spec.Step{{ID: "fix", Type: spec.TypeAgent, Seat: "a", Objective: "echo OBJ-$((1+1))", Compact: compact, AllowedExits: []string{"handoff"}}}
			e, id := hooked(t, steps, "a")
			_, starts := e.step(t, id)
			if len(starts) != 1 {
				t.Fatalf("starts %+v", starts)
			}
			r := load(t, e, id)
			if err := e.deliver(context.Background(), r, starts[0]); err != nil {
				t.Fatal(err)
			}
			var screen string
			for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
				screen, _ = e.Rig.Tmux.Capture(r.Session, "a")
				if strings.Contains(screen, "OBJ-2") {
					break
				}
			}
			c, o := strings.Index(screen, "/compact"), strings.Index(screen, "OBJ-2")
			if o < 0 || (c >= 0) != compact || c > o {
				t.Fatalf("compact %v: want /compact pasted only then, before the objective:\n%s", compact, screen)
			}
		})
	}
}
