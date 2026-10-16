package state

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/berni23/laight/internal/spec"
)

func TestNewID(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	a, b := NewID(now), NewID(now.Add(time.Millisecond))
	if len(a) != 26 || strings.Trim(a, "0123456789ABCDEFGHJKMNPQRSTVWXYZ") != "" {
		t.Fatalf("NewID = %q, not a ULID", a)
	}
	if a >= b {
		t.Errorf("ids don't sort by time: %s >= %s", a, b)
	}
	if a[:10] != NewID(now)[:10] || a == NewID(now) {
		t.Error("same millisecond: want the same time part and a different random part")
	}
	// The ULID spec's example: 1469922850259 ms encodes as 01ARZ3NDEK.
	if got := NewID(time.UnixMilli(1469922850259))[:10]; got != "01ARZ3NDEK" {
		t.Errorf("time part = %s, want 01ARZ3NDEK", got)
	}
}

func TestShortOfShortID(t *testing.T) {
	if got := Short("AB12"); got != "ab12" {
		t.Errorf("Short(AB12) = %q", got)
	}
}

func TestCreateUpdateFind(t *testing.T) {
	home := t.TempDir()
	mk := func(id, repo, status string) {
		w := &spec.Workflow{ID: "wf", Timeouts: spec.Timeouts{Step: spec.Duration(90 * time.Minute)}}
		if err := Create(home, &Run{ID: id, Workflow: "wf", Repo: repo, Status: status, Spec: w}); err != nil {
			t.Fatal(err)
		}
	}
	mk("01M50RN02M7ZEN7P5Z5R18H4Z7", "/a", Running)
	mk("01M50RN02M7ZEN7P5Z5R18AAAA", "/a", Aborted)
	mk("01M50RN02M7ZEN7P5Z5R18BBBB", "/b", Running)

	r, err := Update(home, "01M50RN02M7ZEN7P5Z5R18H4Z7", func(r *Run) error { r.Status = Failed; return nil })
	if err != nil || r.Status != Failed {
		t.Fatalf("Update = %v, %v", r, err)
	}
	r, err = Load(home, "01M50RN02M7ZEN7P5Z5R18H4Z7")
	if err != nil || r.Status != Failed || r.Spec.Timeouts.Step != spec.Duration(90*time.Minute) {
		t.Fatalf("Load after Update = %+v, %v", r, err)
	}

	live := func(r *Run) bool { return r.Live() }
	for _, tt := range []struct{ repo, ref, want, err string }{
		{ref: "18h4", want: "18h4z7"},
		{ref: "18H4Z7", want: "18h4z7"},
		{ref: "01M50RN02M7ZEN7P5Z5R18A", want: "18aaaa"},
		{ref: "18", err: "matches 3 runs"},
		{ref: "zz", err: `no run "zz"`},
		{repo: "/b", want: "18bbbb"},
		{repo: "/a", err: "no live run in this project; name one: 18h4z7 (wf, failed), 18aaaa (wf, aborted)"},
		{repo: "/c", err: "no runs in this project yet"},
	} {
		r, err := Find(home, tt.repo, tt.ref, live)
		switch {
		case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
			t.Errorf("Find(%q, %q) error = %v, want %q", tt.repo, tt.ref, err, tt.err)
		case tt.err == "" && (err != nil || r.Short() != tt.want):
			t.Errorf("Find(%q, %q) = %v, %v; want %s", tt.repo, tt.ref, r, err, tt.want)
		}
	}
}

func TestEventsAndRegister(t *testing.T) {
	home := t.TempDir()
	if err := Create(home, &Run{ID: "01M50RN02M7ZEN7P5Z5R18H4Z7"}); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"run.started", "seat.up"} {
		if err := Append(home, Event{Run: "01M50RN02M7ZEN7P5Z5R18H4Z7", Type: typ}); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := Events(home, "01M50RN02M7ZEN7P5Z5R18H4Z7")
	if err != nil || len(evs) != 2 || evs[1].Type != "seat.up" || evs[0].TS.IsZero() {
		t.Errorf("Events = %+v, %v", evs, err)
	}

	for range 2 {
		if err := Register(home, "/x/proj"); err != nil {
			t.Fatal(err)
		}
	}
	if err := Register(home, "/y/other"); err != nil {
		t.Fatal(err)
	}
	if s := Socket(home); s == "laight" || s == Socket(t.TempDir()) {
		t.Errorf("Socket(%s) = %s: want a name of its own", home, s)
	}
}

// Many writers finishing the same hop at once: exactly one wins, and the file is its write.
func TestWriteDoneFirstWins(t *testing.T) {
	home, id := t.TempDir(), "RUN"
	errs := make(chan error, 20)
	for i := range 20 {
		go func() {
			errs <- WriteDone(home, id, Done{Step: "s", Hop: 1, Exit: fmt.Sprint("e", i)})
		}()
	}
	won := 0
	for range 20 {
		switch err := <-errs; {
		case err == nil:
			won++
		case !errors.Is(err, ErrAlreadyDone):
			t.Fatal(err)
		}
	}
	d, err := ReadDone(home, id, "s", 1)
	if won != 1 || err != nil || d == nil || !strings.HasPrefix(d.Exit, "e") {
		t.Errorf("won %d, done %+v, %v", won, d, err)
	}
}

// A shell step's hop has no seat; it must not look like the step of a seat named "".
func TestActiveOnShellHop(t *testing.T) {
	r := &Run{Active: []*Hop{{Step: "merge", N: 1}, {Step: "fix", Seat: "impl", N: 2}}}
	if h := r.ActiveOn(""); h != nil {
		t.Errorf("ActiveOn(\"\") = %+v", h)
	}
	if h := r.ActiveOn("impl"); h == nil || h.Step != "fix" {
		t.Errorf("ActiveOn(impl) = %+v", h)
	}
}
