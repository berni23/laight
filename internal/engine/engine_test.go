package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
		status, _, starts, _ = e.advance(r)
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

// The verdict is saved with the state that implies it: an engine that dies before tearing the
// rig down must not find an empty queue later and call a failed run a success.
func TestVerdictSavedWithState(t *testing.T) {
	e, id := hooked(t, two(map[string]string{"handoff": "two"}), "a")
	ctx := context.Background()
	if status, _, err := e.tick(ctx, id); status != "" || err != nil {
		t.Fatalf("tick: %s %v", status, err)
	}
	if err := state.WriteDone(e.Home, id, state.Done{Step: "one", Hop: 1, Exit: spec.ExitFailed, Out: map[string]any{"error": "boom"}}); err != nil {
		t.Fatal(err)
	}
	status, reason, err := e.tick(ctx, id)
	if status != state.Failed || err != nil {
		t.Fatalf("tick: %s %q %v", status, reason, err)
	}
	if r := load(t, e, id); r.Status != state.Failed || r.Error != "step one failed: boom" {
		t.Errorf("run.json: %s %q", r.Status, r.Error)
	}
	// A second engine on the same run gets the same verdict, not a success.
	if status, reason, _ := e.tick(ctx, id); status != state.Failed || reason != "step one failed: boom" {
		t.Errorf("second engine: %s %q", status, reason)
	}
}
