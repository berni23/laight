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

// stuckItems returns the run's pending stuck items.
func stuckItems(t *testing.T, e *Engine, id string) []*inbox.Item {
	t.Helper()
	items, err := inbox.Pending(e.Home)
	if err != nil {
		t.Fatal(err)
	}
	var out []*inbox.Item
	for _, it := range items {
		if it.Run == id && it.Kind == inbox.Stuck {
			out = append(out, it)
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
// failed) holds its next step: past the step's timeout that is a stuck item, not a silent stall.
// While a permission item is pending for the seat, a human is already asked, so it isn't one.
// Fail frees the seat.
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
	if items := stuckItems(t, e, id); len(items) != 0 {
		t.Fatalf("stuck before the timeout: %+v", items)
	}

	perm := inbox.Item{ID: state.NewID(time.Now()), Run: id, Seat: "a", Kind: inbox.Permission, Title: "a wants to use Bash", Choices: []string{inbox.Allow, inbox.Deny}}
	if err := inbox.Put(e.Home, perm); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	tick(t, e, id)
	if items := stuckItems(t, e, id); len(items) != 0 {
		t.Fatalf("stuck while a permission item waits: %+v", items)
	}
	inbox.Drop(e.Home, perm.ID)

	tick(t, e, id)
	items := stuckItems(t, e, id)
	if len(items) != 1 || !strings.HasPrefix(items[0].Title, "a finished step one, but its turn hasn't ended") {
		t.Fatalf("items %+v", items)
	}
	if r := load(t, e, id); r.Seats["a"].Stuck != items[0].ID || len(r.Queue) != 1 {
		t.Fatalf("seat %+v, queue %v", r.Seats["a"], r.Queue)
	}
	tick(t, e, id)
	if n := len(stuckItems(t, e, id)); n != 1 {
		t.Fatalf("asked %d times", n)
	}
	if _, err := inbox.Answer(e.Home, items[0].ID, inbox.Fail); err != nil {
		t.Fatal(err)
	}
	tick(t, e, id) // the answer frees the seat…
	tick(t, e, id) // …and two starts on it
	r := load(t, e, id)
	if h := r.ActiveOn("a"); h == nil || h.Step != "two" || r.Seats["a"].Stuck != "" {
		t.Fatalf("active %+v, seat %+v", r.Active, r.Seats["a"])
	}
}

// A stuck item about a turn that then ends on its own is withdrawn: nobody needs to answer it.
func TestStuckItemWithdrawn(t *testing.T) {
	steps := two(nil)
	timeout := spec.Duration(100 * time.Millisecond)
	steps[0].Timeout = &timeout
	e, id := hooked(t, steps, "a")
	tick(t, e, id)
	time.Sleep(200 * time.Millisecond)
	tick(t, e, id)
	items := stuckItems(t, e, id)
	if len(items) != 1 || !strings.HasPrefix(items[0].Title, "a has been quiet for") {
		t.Fatalf("items %+v", items)
	}
	finish(t, e, id, state.Hop{Step: "one", Seat: "a", N: 1}, "handoff")
	tick(t, e, id)
	if items := stuckItems(t, e, id); len(items) != 0 {
		t.Fatalf("still asked: %+v", items)
	}
	if _, err := os.Stat(filepath.Join(e.Home, "inbox", items[0].ID+".json")); !os.IsNotExist(err) {
		t.Errorf("item file: %v", err)
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

// Failing a quiet hooked seat from the inbox fails its step, but its turn may still be running:
// the next step waits for the Stop hook, so a late `laight done` from that turn can't finish it.
func TestStuckFailWaitsForTurn(t *testing.T) {
	steps := two(map[string]string{"failed": "two"})
	timeout := spec.Duration(100 * time.Millisecond)
	steps[0].Timeout = &timeout
	e, id := hooked(t, steps, "a")
	tick(t, e, id)
	time.Sleep(200 * time.Millisecond)
	tick(t, e, id)
	items := stuckItems(t, e, id)
	if len(items) != 1 {
		t.Fatalf("items %+v", items)
	}
	if _, err := inbox.Answer(e.Home, items[0].ID, inbox.Fail); err != nil {
		t.Fatal(err)
	}
	tick(t, e, id) // the answer fails the hop…
	tick(t, e, id) // …which routes to two, on the same seat
	r := load(t, e, id)
	if r.Steps["one"] == nil || r.Steps["one"].Exit != spec.ExitFailed || r.ActiveOn("a") != nil || !r.Seats["a"].Turn {
		t.Fatalf("steps %+v, active %+v, seat %+v", r.Steps, r.Active, r.Seats["a"])
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

// A gate is never stuck, even with a timeout of its own: it already waits on a human.
func TestGateNeverStuck(t *testing.T) {
	timeout := spec.Duration(100 * time.Millisecond)
	e, id := hooked(t, []*spec.Step{{ID: "ask", Type: spec.TypeGate, Ask: "Go on?", AllowedExits: []string{"yes"}, Timeout: &timeout}})
	tick(t, e, id)
	time.Sleep(200 * time.Millisecond)
	tick(t, e, id)
	items, _ := inbox.Pending(e.Home)
	if len(items) != 1 || items[0].Kind != inbox.Gate {
		t.Fatalf("items %+v", items)
	}
}

// A nudge answered after the agent started working again and reached a permission dialog is not
// pasted: its Enter would approve the tool without anyone answering the permission item.
func TestNudgeSkippedWhilePermissionWaits(t *testing.T) {
	e, id := hooked(t, two(nil), "a")
	tick(t, e, id)
	if err := os.WriteFile(StoppedMark(e.Home, id, "a"), []byte(IdleMark("one", 1)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tick(t, e, id)
	items := stuckItems(t, e, id)
	if len(items) != 1 {
		t.Fatalf("items %+v", items)
	}
	perm := inbox.Item{ID: state.NewID(time.Now()), Run: id, Seat: "a", Kind: inbox.Permission, Title: "a wants to use Bash", Choices: []string{inbox.Allow, inbox.Deny}}
	if err := inbox.Put(e.Home, perm); err != nil {
		t.Fatal(err)
	}
	if _, err := inbox.Answer(e.Home, items[0].ID, inbox.Nudge); err != nil {
		t.Fatal(err)
	}
	tick(t, e, id)
	time.Sleep(300 * time.Millisecond) // a paste would have reached the pane by now
	r := load(t, e, id)
	screen, err := e.Rig.Tmux.Capture(r.Session, "a")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(screen, "laight: Step") || r.Seats["a"].Stuck != "" {
		t.Fatalf("nudged with a permission item waiting; seat %+v\n%s", r.Seats["a"], screen)
	}
}

// A question or permission item a seat's hook raised is printed in the engine window once, with
// the agent's options and how to answer; items of other runs are not.
func TestAnnounce(t *testing.T) {
	e, id := hooked(t, two(nil), "a")
	var log strings.Builder
	e.Log = &log
	q := inbox.Item{ID: state.NewID(time.Now()), Run: id, Seat: "a", Kind: inbox.Question, Title: "a asks: which fix?",
		Questions: []inbox.Ask{{Question: "which fix?", Options: []inbox.Option{{Label: "reject"}, {Label: "document"}}}}}
	other := inbox.Item{ID: state.NewID(time.Now()), Run: "01OTHER", Seat: "a", Kind: inbox.Permission, Title: "a wants to use Bash", Choices: []string{inbox.Allow, inbox.Deny}}
	for _, it := range []inbox.Item{q, other} {
		if err := inbox.Put(e.Home, it); err != nil {
			t.Fatal(err)
		}
	}
	tick(t, e, id)
	tick(t, e, id)
	got := log.String()
	for _, want := range []string{"? a asks: which fix?\n", "1. reject", "2. document", "laight inbox " + q.Short() + " <number"} {
		if strings.Count(got, want) != 1 {
			t.Errorf("%q printed %d times:\n%s", want, strings.Count(got, want), got)
		}
	}
	if strings.Contains(got, "Bash") {
		t.Errorf("another run's item printed:\n%s", got)
	}
}
