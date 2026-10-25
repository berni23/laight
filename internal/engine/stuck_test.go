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
	timeout := spec.Duration(300 * time.Millisecond)
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
	time.Sleep(400 * time.Millisecond)
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
