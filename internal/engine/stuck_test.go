package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
)

// events returns the run's events of one type.
func eventsOf(t *testing.T, e *Engine, id, typ string) []state.Event {
	t.Helper()
	evs, err := state.Events(e.Home, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []state.Event
	for _, ev := range evs {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

func tick(t *testing.T, e *Engine, id string) {
	t.Helper()
	if _, _, err := e.tick(context.Background(), id); err != nil {
		t.Fatal(err)
	}
}

// A hooked seat that ran `laight done` but whose Stop hook never said its turn ended (the hook
// failed) holds its next step: past the step's timeout it is stopped, so a turn still running
// can't finish in its next step, and that step relaunches it. While a question item is pending
// for the seat, a human is already asked, so it waits.
func TestStuckTurnNeverEnded(t *testing.T) {
	steps := two(map[string]string{"handoff": "two"})
	timeout := spec.Duration(time.Second)
	steps[0].Timeout = &timeout
	e, id := hooked(t, steps, "a")
	tick(t, e, id)
	if err := state.WriteDone(e.Home, id, state.Done{Step: "one", Hop: 1, Exit: "handoff"}); err != nil {
		t.Fatal(err)
	}
	tick(t, e, id)
	if r := load(t, e, id); !r.Seats["a"].Up || len(r.Queue) != 1 {
		t.Fatalf("stopped before the timeout: %+v", r.Seats["a"])
	}

	q := inbox.Item{ID: state.NewID(time.Now()), Run: id, Seat: "a", Kind: inbox.Question, Title: "a asks: which fix?", Questions: []inbox.Ask{{Question: "which fix?"}}}
	if err := inbox.Put(e.Home, q); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	tick(t, e, id)
	if r := load(t, e, id); !r.Seats["a"].Up {
		t.Fatalf("stopped while a question item waits: %+v", r.Seats["a"])
	}
	inbox.Drop(e.Home, q.ID)

	var starts []start
	if _, err := state.Update(e.Home, id, func(r *state.Run) error {
		nudges, _ := e.watch(r)
		if len(nudges) != 1 || !nudges[0].kill || nudges[0].window != "a" {
			t.Errorf("nudges %+v", nudges)
		}
		if s := r.Seats["a"]; s.Up || s.Turn {
			t.Errorf("seat %+v", s)
		}
		_, _, starts, _, _ = e.advance(r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(starts) != 1 || starts[0].hop.Step != "two" || !starts[0].restart {
		t.Fatalf("starts %+v", starts)
	}
	if evs := eventsOf(t, e, id, "step.timeout"); len(evs) != 0 {
		t.Errorf("a done step timed out: %+v", evs)
	}
}

// A step quiet past its timeout fails as a timeout, with the end of its pane, and the run ends
// red once nothing runs. Nobody is asked.
func TestQuietStepTimesOut(t *testing.T) {
	steps := two(nil)
	timeout := spec.Duration(100 * time.Millisecond)
	steps[0].Timeout = &timeout
	e, id := hooked(t, steps, "a")
	tick(t, e, id)
	r := load(t, e, id)
	if err := e.Rig.Tmux.Paste(r.Session, "a", "echo waiting-on-a-dialog"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	tick(t, e, id) // fails the hop…
	status, reason, err := e.tick(context.Background(), id)
	if status != state.Failed || err != nil || !strings.Contains(reason, "a was quiet for") {
		t.Fatalf("tick: %q %q %v", status, reason, err)
	}
	f := load(t, e, id).Failure
	if f == nil || f.Kind != state.KindTimeout || f.Step != "one" || f.Seat != "a" || !strings.Contains(f.Detail, "waiting-on-a-dialog") {
		t.Fatalf("failure %+v", f)
	}
	if evs := eventsOf(t, e, id, "step.timeout"); len(evs) != 1 {
		t.Errorf("step.timeout events %+v", evs)
	}
	if items, _ := inbox.Pending(e.Home); len(items) != 0 {
		t.Errorf("a human was asked: %+v", items)
	}
}

// A hooked seat whose transcript stays still is quiet, however much its spinner grows the pane
// log (#72); with no transcript to go by, the pane log still counts.
func TestQuietWhileSpinnerRuns(t *testing.T) {
	steps := two(nil)
	timeout := spec.Duration(300 * time.Millisecond)
	steps[0].Timeout = &timeout
	e, id := hooked(t, steps, "a")
	tick(t, e, id)
	seats := filepath.Join(state.Dir(e.Home, id), "seats")
	transcript := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	spin := func() {
		for range 6 {
			f, err := os.OpenFile(filepath.Join(seats, "a.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			f.WriteString("✻ Waiting for 1 background agent to finish\r")
			f.Close()
			time.Sleep(100 * time.Millisecond)
			tick(t, e, id)
		}
	}
	spin() // no transcript in .ready: the moving log keeps the seat busy
	if f := load(t, e, id).Failure; f != nil {
		t.Fatalf("timed out while its pane log moved and no transcript was known: %+v", f)
	}
	if err := os.WriteFile(filepath.Join(seats, "a.ready"), []byte(`{"transcript_path":"`+transcript+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	spin()
	if f := load(t, e, id).Failure; f == nil || f.Kind != state.KindTimeout {
		t.Fatalf("a spinner kept a seat with a still transcript from timing out: %+v", f)
	}
}

// activity is the newest of the transcript and its agents' (in folders below too), not the pane
// log, once the SessionStart hook said where the transcript is; the pane log otherwise.
func TestActivity(t *testing.T) {
	seats, dir := t.TempDir(), t.TempDir()
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	at := func(file string, ago time.Duration) {
		t.Helper()
		os.MkdirAll(filepath.Dir(file), 0o700)
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		m := base.Add(-ago)
		if err := os.Chtimes(file, m, m); err != nil {
			t.Fatal(err)
		}
	}
	ready := func(s string) {
		if err := os.WriteFile(filepath.Join(seats, "a.ready"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	transcript := filepath.Join(dir, "sess.jsonl")
	at(filepath.Join(seats, "a.log"), 0)
	if got := activity(seats, "a"); !got.Equal(base) {
		t.Errorf("no .ready: %v, want the log's %v", got, base)
	}
	ready(`{"session_id":"x","transcript_path":""}`)
	if got := activity(seats, "a"); !got.Equal(base) {
		t.Errorf("no transcript: %v, want the log's", got)
	}
	ready(`{"transcript_path":"` + transcript + `"}`)
	if got := activity(seats, "a"); !got.Equal(base) {
		t.Errorf("transcript missing: %v, want the log's", got)
	}
	at(transcript, 10*time.Minute)
	if got, want := activity(seats, "a"), base.Add(-10*time.Minute); !got.Equal(want) {
		t.Errorf("transcript: %v, want %v (not the log's)", got, want)
	}
	at(filepath.Join(dir, "sess", "subagents", "agent-a1.jsonl"), 5*time.Minute)
	at(filepath.Join(dir, "sess", "subagents", "wf", "agent-a2.jsonl"), 2*time.Minute)
	if got, want := activity(seats, "a"), base.Add(-2*time.Minute); !got.Equal(want) {
		t.Errorf("subagents: %v, want %v", got, want)
	}
}

func TestLastLines(t *testing.T) {
	screen := "a\n\n  b   \n\n\n   \nc\n\n\n"
	if got := lastLines(screen, 2); got != "  b\nc" {
		t.Errorf("lastLines = %q", got)
	}
	if got := lastLines("", 2); got != "" {
		t.Errorf("lastLines of nothing = %q", got)
	}
}

// A timed-out step routes like any failure, but its turn may still be running: the seat takes
// its next step only once its Stop hook says the turn ended.
func TestTimeoutWaitsForTurn(t *testing.T) {
	steps := two(map[string]string{"failed": "two"})
	timeout := spec.Duration(100 * time.Millisecond)
	steps[0].Timeout = &timeout
	e, id := hooked(t, steps, "a")
	tick(t, e, id)
	time.Sleep(200 * time.Millisecond)
	tick(t, e, id) // the hop times out…
	// From here the seat's turn is waiting on its Stop hook. Past the same 100 ms a slow tick
	// would take it for a turn that never ended and stop the seat (TestStuckTurnNeverEnded):
	// not what this test is about, so the clock goes out of reach.
	if _, err := state.Update(e.Home, id, func(r *state.Run) error {
		long := spec.Duration(time.Hour)
		r.Spec.Step("one").Timeout = &long
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tick(t, e, id) // …and routes to two, on the same seat
	r := load(t, e, id)
	if r.Steps["one"] == nil || r.Steps["one"].Exit != spec.ExitFailed || r.ActiveOn("a") != nil || !r.Seats["a"].Turn || r.Failure != nil {
		t.Fatalf("steps %+v, active %+v, seat %+v, failure %+v", r.Steps, r.Active, r.Seats["a"], r.Failure)
	}
	mark := filepath.Join(state.Dir(e.Home, id), "seats", "a.idle")
	if err := os.WriteFile(mark, []byte(IdleMark("one", 1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tick(t, e, id)
	if h := load(t, e, id).ActiveOn("a"); h == nil || h.Step != "two" {
		t.Fatalf("two didn't start after the turn ended: %+v", h)
	}
}

// Only a worktree laight made for the seat counts as its writes: in the main checkout (cwd:
// repo) or any other folder, the user's own edits would keep a stuck seat from being reported.
func TestOwnWrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Minute)
	if m := ownWrite(&state.Seat{Dir: dir}, since); !m.IsZero() {
		t.Errorf("a folder laight doesn't own counted: %v", m)
	}
	if m := ownWrite(&state.Seat{Dir: dir, Worktree: true}, since); m.IsZero() {
		t.Error("a write in the seat's worktree didn't count")
	}
}

// A gate is never timed, even with a timeout of its own: it already waits on a human.
func TestGateNeverStuck(t *testing.T) {
	timeout := spec.Duration(100 * time.Millisecond)
	e, id := hooked(t, []*spec.Step{{ID: "ask", Type: spec.TypeGate, Ask: "Go on?", AllowedExits: []string{"yes"}, Timeout: &timeout}})
	tick(t, e, id)
	time.Sleep(200 * time.Millisecond)
	tick(t, e, id)
	r := load(t, e, id)
	if len(r.Active) != 1 || r.Failure != nil {
		t.Fatalf("active %+v, failure %+v", r.Active, r.Failure)
	}
}

// An agent that stopped without `laight done` (its Stop hook already reminded it once) is reminded
// once more by the engine; if it stops again, its hop fails as stuck and the run ends red.
func TestStoppedNudgedThenStuck(t *testing.T) {
	e, id := hooked(t, two(nil), "a")
	e.Rig.Laight = "laight"
	tick(t, e, id)
	stop := func() {
		t.Helper()
		if err := os.WriteFile(StoppedMark(e.Home, id, "a"), []byte(IdleMark("one", 1)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stop()
	tick(t, e, id)
	r := load(t, e, id)
	var screen string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if screen, _ = e.Rig.Tmux.Capture(r.Session, "a"); strings.Contains(screen, "laight: Step one is not finished") {
			break
		}
	}
	if !strings.Contains(screen, "laight: Step one is not finished") || r.Active[0].Nudged.IsZero() {
		t.Fatalf("not nudged; hop %+v\n%s", r.Active[0], screen)
	}
	tick(t, e, id)
	if r := load(t, e, id); len(r.Active) != 1 || len(eventsOf(t, e, id, "step.nudged")) != 1 {
		t.Fatalf("nudged twice, or failed on the same stop: %+v", r.Active)
	}
	time.Sleep(10 * time.Millisecond) // a later mtime
	stop()
	tick(t, e, id)
	status, _, _ := e.tick(context.Background(), id)
	f := load(t, e, id).Failure
	if status != state.Failed || f == nil || f.Kind != state.KindStuck || f.Step != "one" || !strings.Contains(f.Detail, "laight: Step one") {
		t.Fatalf("status %q, failure %+v", status, f)
	}
	if r := load(t, e, id); r.Seats["a"].Turn {
		t.Error("a stopped agent's turn is over, but the seat still waits for it")
	}
}

// A question item a seat's hook raised is printed in the engine window once, with the agent's
// options and how to answer, and so is every request a seat was denied; other runs' are not.
func TestAnnounce(t *testing.T) {
	e, id := hooked(t, two(nil), "a")
	var log strings.Builder
	e.Log = &log
	q := inbox.Item{ID: state.NewID(time.Now()), Run: id, Seat: "a", Kind: inbox.Question, Title: "a asks: which fix?",
		Questions: []inbox.Ask{{Question: "which fix?", Options: []inbox.Option{{Label: "reject"}, {Label: "document"}}}}}
	other := inbox.Item{ID: state.NewID(time.Now()), Run: "01OTHER", Seat: "a", Kind: inbox.Question, Title: "a asks: Bash?", Questions: []inbox.Ask{{Question: "Bash?"}}}
	for _, it := range []inbox.Item{q, other} {
		if err := inbox.Put(e.Home, it); err != nil {
			t.Fatal(err)
		}
	}
	deny := func(run string, data map[string]any) {
		if err := state.Append(e.Home, state.Event{Run: run, Type: "permission.denied", Seat: "a", Data: data}); err != nil {
			t.Fatal(err)
		}
	}
	deny(id, map[string]any{"tool": "Bash", "input": "git push origin dev\nsecond line", "by": "laight"})
	tick(t, e, id)
	deny(id, map[string]any{"tool": "WebFetch", "input": `{"url":"https://x"}`, "by": "auto mode", "reason": "exfiltration"})
	tick(t, e, id)
	got := log.String()
	for _, want := range []string{"? a asks: which fix?\n", "1. reject", "2. document", "laight inbox " + q.Short() + " <number",
		"⊘ a denied Bash: git push origin dev (nobody to ask)\n", `⊘ a denied WebFetch: {"url":"https://x"} (auto mode: exfiltration)` + "\n"} {
		if strings.Count(got, want) != 1 {
			t.Errorf("%q printed %d times:\n%s", want, strings.Count(got, want), got)
		}
	}
	if strings.Contains(got, "Bash?") {
		t.Errorf("another run's item printed:\n%s", got)
	}
}

// A request denied in the tick that ends the run is printed too, before the engine returns.
func TestAnnounceDenialAtTheEnd(t *testing.T) {
	e, id := hooked(t, two(nil)[:1], "a")
	var log strings.Builder
	e.Log = &log
	tick(t, e, id)
	if err := state.Append(e.Home, state.Event{Run: id, Type: "permission.denied", Seat: "a",
		Data: map[string]any{"tool": "Bash", "input": "touch x", "by": "laight"}}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteDone(e.Home, id, state.Done{Step: "one", Hop: 1, Exit: "handoff"}); err != nil {
		t.Fatal(err)
	}
	status, _, err := e.tick(context.Background(), id)
	if err != nil || status != state.Succeeded {
		t.Fatalf("tick: %q %v", status, err)
	}
	if !strings.Contains(log.String(), "⊘ a denied Bash: touch x") {
		t.Errorf("not printed:\n%s", log.String())
	}
}

// A reminder that can't be pasted fails the hop as stuck at once: the hop is already marked
// nudged, so nothing else would ever end it in a step without a timeout (#80 review, question 15).
func TestFailedNudgeFailsHop(t *testing.T) {
	e, id := hooked(t, two(nil), "a")
	tick(t, e, id)
	r := load(t, e, id)
	e.apply(r, nudge{window: "no-such-window", text: "laight: remind", step: "one", hop: 1})
	d, err := state.ReadDone(e.Home, id, "one", 1)
	if err != nil || d == nil || d.Exit != spec.ExitFailed || d.Kind != state.KindStuck {
		t.Fatalf("done %+v %v", d, err)
	}
}
