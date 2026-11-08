package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/berni23/laight/internal/inbox"
	"github.com/berni23/laight/internal/runtime"
	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// Stuck detection (CLAUDE.md §12.2). `laight done` and the hooks are the proof that a step ended;
// file timestamps only ever add evidence that something is still happening. Nobody is asked
// (requirement 4): a stopped agent (the Stop hook's .stopped mark, after its own reminder) is at
// its prompt, so the engine pastes the reminder once, and fails the hop (stuck) if it stops again.
// A quiet one may be showing a dialog that a paste's Enter would answer, so it fails at once
// (timeout), and the report's pane lines show what it was waiting on. So does a quiet shell step.

// nudge is what to do to a seat once run.json is saved: paste a reminder, send keys, or close
// its window.
type nudge struct {
	window, text string
	keys         []string
	kill         bool
}

// Reminder is how an agent is told to finish its step: by the Stop hook, and by a nudge.
func Reminder(st *spec.Step, laight string) string {
	return fmt.Sprintf("Step %s is not finished. When it is, run: %s done <exit> [key=value…] (exits: %s). If it can't be done, run: %s done failed reason=\"<why>\".",
		st.ID, tmux.Quote(laight), strings.Join(st.AllowedExits, ", "), tmux.Quote(laight))
}

// StoppedMark is the file the Stop hook writes, with IdleMark(step, hop), when the agent stops
// a second time without finishing its step.
func StoppedMark(home, run, seat string) string {
	return filepath.Join(state.Dir(home, run), "seats", seat+".stopped")
}

// watch nudges stopped agents and fails stuck hops. It runs with run.json locked, after advance,
// and returns what to do to the seats once it is saved.
func (e *Engine) watch(r *state.Run) (nudges []nudge, events []state.Event) {
	now := time.Now().UTC()
	for _, name := range slices.Sorted(maps.Keys(r.Seats)) {
		n, evs := e.watchSeat(r, name, now)
		if n != nil {
			nudges = append(nudges, *n)
		}
		events = append(events, evs...)
	}
	for _, h := range r.Active {
		if st := r.Spec.Step(h.Step); st != nil && st.Type == spec.TypeShell {
			events = append(events, e.watchShell(r, h, st, now)...)
		}
	}
	return nudges, events
}

// watchSeat looks at one seat whose turn hasn't ended: on a step (no `laight done` yet), or after
// one (done, but the Stop hook never said the turn ended).
func (e *Engine) watchSeat(r *state.Run, name string, now time.Time) (*nudge, []state.Event) {
	s := r.Seats[name]
	st := r.Spec.Step(s.Step)
	if !s.Turn || !s.Up || st == nil {
		return nil, nil
	}
	h := r.ActiveOn(name)
	if h != nil && (h.Step != s.Step || h.N != s.Hop) {
		return nil, nil
	}
	if d, _ := state.ReadDone(e.Home, r.ID, s.Step, s.Hop); d != nil && h != nil {
		return nil, nil // advance takes it next tick
	}
	var since time.Time
	if h != nil {
		since = later(h.Started, h.Nudged)
	} else {
		since = mtime(state.DoneFile(e.Home, r.ID, s.Step, s.Hop))
	}

	stopped := StoppedMark(e.Home, r.ID, name)
	if b, _ := os.ReadFile(stopped); h != nil && strings.TrimSpace(string(b)) == IdleMark(s.Step, s.Hop) && mtime(stopped).After(since) {
		if h.Nudged.IsZero() {
			e.logf("↻ %s stopped without finishing step %s: reminded", name, label(h.Step, h.Copy))
			h.Nudged = now
			return &nudge{window: s.Window, text: "laight: " + Reminder(st, e.Rig.Laight)},
				[]state.Event{{Run: r.ID, Type: "step.nudged", Step: h.Step, Seat: name, Hop: h.N}}
		}
		reason := fmt.Sprintf("%s stopped without finishing step %s, again after a reminder", name, label(h.Step, h.Copy))
		e.failHop(r, h, state.KindStuck, reason, false) // at its prompt: its turn is over
		return nil, nil
	}

	timeout := st.Quiet()
	if timeout <= 0 {
		return nil, nil
	}
	seats := filepath.Join(state.Dir(e.Home, r.ID), "seats")
	last := since
	for _, f := range []string{name + ".log", name + ".ready", name + ".idle", name + ".stopped"} {
		last = later(last, mtime(filepath.Join(seats, f)))
	}
	if now.Sub(last) < timeout || now.Before(e.busy[r.ID+"/"+name]) {
		return nil, nil
	}
	if waitingOnQuestion(e.Home, r.ID, name) {
		return nil, nil // a human is already asked (until #64)
	}
	// Last and costliest: a quiet build or test run still writes files. Only in a worktree
	// laight made for the seat: in the main checkout or any other folder, the user's own
	// edits would count.
	if m := ownWrite(s, now.Add(-timeout)); !m.IsZero() {
		if e.busy == nil {
			e.busy = map[string]time.Time{}
		}
		e.busy[r.ID+"/"+name] = m.Add(timeout)
		return nil, nil
	}
	quiet := now.Sub(last).Round(time.Second)
	if h == nil {
		// Its step is done, but its turn never ended. Freed as it is, a turn still running would
		// finish in its next step: the seat is stopped, and its next step relaunches it.
		e.logf("⟳ %s finished step %s, but its turn hasn't ended (quiet for %s): stopped; its next step relaunches it", name, s.Step, quiet)
		s.Up, s.Turn = false, false
		return &nudge{window: s.Window, kill: true}, []state.Event{{Run: r.ID, Type: "seat.down", Seat: name,
			Data: map[string]any{"reason": fmt.Sprintf("turn never ended after step %s (quiet for %s)", s.Step, quiet)}}}
	}
	reason := fmt.Sprintf("%s was quiet for %s on step %s", name, quiet, label(h.Step, h.Copy))
	// Its turn may still be running: the seat waits for its Stop hook before another step, or a
	// late `laight done` from that turn would finish the next hop. A seat without hooks never
	// says, so its turn is interrupted instead.
	var n *nudge
	turn := true
	if rt, err := runtime.For(r.Spec.SeatOf(name).Runtime); err == nil && !rt.Hooks() {
		n, turn = &nudge{window: s.Window, keys: []string{"C-c"}}, false
	}
	e.failHop(r, h, state.KindTimeout, reason, turn)
	return n, []state.Event{{Run: r.ID, Type: "step.timeout", Step: h.Step, Seat: name, Hop: h.N}}
}

// failHop ends a seat's hop as failed, of the given kind, with the end of its pane: advance
// routes it next tick. turn says whether the agent's turn may still be running.
func (e *Engine) failHop(r *state.Run, h *state.Hop, kind, reason string, turn bool) {
	e.logf("✕ %s", reason)
	err := state.WriteDone(e.Home, r.ID, state.Done{Step: h.Step, Hop: h.N, Exit: spec.ExitFailed,
		Out: map[string]any{"error": reason}, Engine: true, Turn: turn, Kind: kind, Detail: e.paneTail(r, h.Seat)})
	if err != nil && !errors.Is(err, state.ErrAlreadyDone) {
		e.logf("warning: %v", err)
	}
}

// watchShell looks at a running shell step: its output log is its only trace. A quiet one fails,
// which stops its command.
func (e *Engine) watchShell(r *state.Run, h *state.Hop, st *spec.Step, now time.Time) []state.Event {
	timeout := st.Quiet()
	if timeout <= 0 {
		return nil
	}
	log := filepath.Join(state.Dir(e.Home, r.ID), ShellLog(h.Step, h.N))
	last := later(h.Started, mtime(log))
	if now.Sub(last) < timeout {
		return nil
	}
	reason := fmt.Sprintf("shell step %s was quiet for %s", h.Step, now.Sub(last).Round(time.Second))
	if err := e.fail(r.ID, h, state.KindTimeout, errors.New(reason)); err != nil {
		e.logf("warning: %v", err)
	}
	e.stopShell(h.N)
	return []state.Event{{Run: r.ID, Type: "step.timeout", Step: h.Step, Hop: h.N}}
}

// trackShell remembers how to stop a running shell step, so a stuck one can be failed.
func (e *Engine) trackShell(hop int, cancel context.CancelFunc) {
	e.cmu.Lock()
	defer e.cmu.Unlock()
	if e.cancels == nil {
		e.cancels = map[int]context.CancelFunc{}
	}
	e.cancels[hop] = cancel
}

func (e *Engine) untrackShell(hop int) {
	e.cmu.Lock()
	defer e.cmu.Unlock()
	delete(e.cancels, hop)
}

// stopShell stops a running shell step's command; its hop must already be recorded as ended.
func (e *Engine) stopShell(hop int) {
	e.cmu.Lock()
	cancel := e.cancels[hop]
	e.cmu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// waitingOnQuestion reports whether the seat has a question item pending: its pane shows
// Claude's dialog, and a human is asked. (A permission is denied at once, never an item: #61.)
func waitingOnQuestion(home, run, seat string) bool {
	items, _ := inbox.Pending(home)
	return slices.ContainsFunc(items, func(it *inbox.Item) bool {
		return it.Run == run && it.Seat == seat && it.Kind == inbox.Question
	})
}

// announce prints, once each, the run's question items that a seat's hook raised (the engine
// prints its own as it raises them), so the run's terminal says what waits and how to answer
// it, and every request a seat was denied.
func (e *Engine) announce(r *state.Run) {
	e.announceDenials(r)
	items, _ := inbox.Pending(e.Home)
	for _, it := range items {
		if it.Run != r.ID || it.Kind != inbox.Question || e.told[it.ID] {
			continue
		}
		if e.told == nil {
			e.told = map[string]bool{}
		}
		e.told[it.ID] = true
		e.logf("? %s", it.Title)
		for i, q := range it.Questions {
			if len(it.Questions) > 1 {
				e.logf("    Q%d. %s", i+1, q.Question)
			}
			for k, o := range q.Options {
				e.logf("      %d. %s", k+1, o.Label)
			}
		}
		e.logf("  answer: laight inbox %s <number or \"your own words\">%s; in full: laight inbox %s; or in window %s",
			it.Short(), strings.Repeat(" …", len(it.Questions)-1), it.Short(), it.Seat)
	}
}

// announceDenials prints the permission.denied events the seats' hooks appended since the last
// tick: what a seat asked for and wasn't given.
func (e *Engine) announceDenials(r *state.Run) {
	evs, off, err := state.EventsFrom(e.Home, r.ID, e.evOff[r.ID])
	if err != nil {
		return
	}
	if e.evOff == nil {
		e.evOff = map[string]int64{}
	}
	e.evOff[r.ID] = off
	for _, ev := range evs {
		if ev.Type != "permission.denied" {
			continue
		}
		what, _, _ := strings.Cut(fmt.Sprint(ev.Data["input"]), "\n")
		by := "nobody to ask"
		if ev.Data["by"] == "auto mode" {
			by = "auto mode"
			if reason, ok := ev.Data["reason"].(string); ok && reason != "" {
				by += ": " + reason
			}
		}
		e.logf("⊘ %s denied %v: %s (%s)", ev.Seat, ev.Data["tool"], what, by)
	}
}

// ownWrite is lastWrite in the seat's folder, if it is a worktree laight made for it.
func ownWrite(s *state.Seat, after time.Time) time.Time {
	if !s.Worktree {
		return time.Time{}
	}
	return lastWrite(s.Dir, after)
}

// errFound stops a walk early.
var errFound = errors.New("found")

// lastWrite returns the time of a write in dir after after (the first one it finds), outside
// .git, or zero if there is none.
func lastWrite(dir string, after time.Time) time.Time {
	var found time.Time
	if dir == "" {
		return found
	}
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == ".git" && path != dir {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(after) {
			found = info.ModTime()
			return errFound
		}
		return nil
	})
	return found
}

func mtime(file string) time.Time {
	info, err := os.Stat(file)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// lastLines returns the last n non-blank lines of s, trailing spaces trimmed: a TUI pads its
// screen with both.
func lastLines(s string, n int) string {
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, " \t"); strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
