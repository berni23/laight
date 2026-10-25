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
	"github.com/berni23/laight/internal/rig"
	"github.com/berni23/laight/internal/runtime"
	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// Stuck detection (CLAUDE.md §12.2). `laight done` and the hooks are the proof that a step ended;
// file timestamps only ever add evidence that something is still happening. A step is stuck when
// its agent stopped twice without `laight done` (the Stop hook's .stopped mark), or when nothing
// it leaves a trace in changed for longer than the step's timeout. Either way a stuck item asks a
// human; nothing routes on it, and nothing new is stored but the open item and when its clock
// last restarted. The choices follow what is known about the pane: a stopped agent is at its
// prompt, so a reminder can be pasted (nudge | fail); a quiet one may be showing a dialog that a
// paste's Enter would answer, so it can only be waited on or failed (wait | fail).

// nudge is what to type into a seat once run.json is saved: a reminder to paste, or keys.
type nudge struct {
	window, text string
	keys         []string
}

// Reminder is how an agent is told to finish its step: by the Stop hook, and by a nudge.
func Reminder(st *spec.Step, laight string) string {
	return fmt.Sprintf("Step %s is not finished. When it is, run: %s done <exit> [key=value…] (exits: %s, failed).",
		st.ID, tmux.Quote(laight), strings.Join(st.AllowedExits, ", "))
}

// StoppedMark is the file the Stop hook writes, with IdleMark(step, hop), when the agent stops
// a second time without finishing its step.
func StoppedMark(home, run, seat string) string {
	return filepath.Join(state.Dir(home, run), "seats", seat+".stopped")
}

// watch raises stuck items and acts on their answers. It runs with run.json locked, after
// advance, and returns the reminders to paste.
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
	if s.Stuck != "" {
		return e.stuckAnswer(r, name, now)
	}
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
	since := s.Clock
	if h != nil {
		since = later(since, h.Started)
	} else {
		since = later(since, mtime(state.DoneFile(e.Home, r.ID, s.Step, s.Hop)))
	}

	stopped := StoppedMark(e.Home, r.ID, name)
	var reason string
	choices := []string{inbox.Wait, inbox.Fail}
	if b, _ := os.ReadFile(stopped); h != nil && strings.TrimSpace(string(b)) == IdleMark(s.Step, s.Hop) && mtime(stopped).After(since) {
		reason = fmt.Sprintf("%s stopped without finishing step %s", name, label(h.Step, h.Copy))
		choices = []string{inbox.Nudge, inbox.Fail} // waiting on an agent at its prompt would wait for ever
	} else {
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
		if waitingOnPermission(e.Home, r.ID, name) {
			return nil, nil // a human is already asked
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
		if h != nil {
			reason = fmt.Sprintf("%s has been quiet for %s on step %s", name, quiet, label(h.Step, h.Copy))
		} else {
			reason = fmt.Sprintf("%s finished step %s, but its turn hasn't ended (quiet for %s)", name, s.Step, quiet)
		}
	}

	// The pane is quoted only once its secrets can be stripped from it.
	excerpt := "(the pane isn't quoted: its secrets can't be read to strip them)"
	secrets, err := rig.Secrets(r, name)
	if err == nil {
		screen, _ := e.Rig.Tmux.Capture(r.Session, s.Window)
		excerpt = lastLines(screen, 15)
	} else {
		e.logf("warning: %s: %v", name, err)
	}
	it := inbox.Item{Step: s.Step, Seat: name, Hop: s.Hop, Title: reason, Choices: choices,
		Detail: fmt.Sprintf("%s\nTo look first: laight attach %s %s\n\n%s", reason, r.Short(), name, excerpt)}
	ev, err := e.raise(r, it, secrets...)
	if err != nil {
		e.logf("warning: can't raise an inbox item: %v", err)
		return nil, nil
	}
	s.Stuck = ev.Data["item"].(string)
	evs := []state.Event{ev}
	if !slices.Contains(choices, inbox.Nudge) {
		evs = append([]state.Event{{Run: r.ID, Type: "step.timeout", Step: s.Step, Seat: name, Hop: s.Hop}}, evs...)
	}
	return nil, evs
}

// stuckAnswer acts on the answer to a seat's stuck item. An item about a turn that has since
// ended, or a seat that moved on to another step, is withdrawn.
func (e *Engine) stuckAnswer(r *state.Run, name string, now time.Time) (*nudge, []state.Event) {
	s := r.Seats[name]
	it, err := inbox.Answered(e.Home, s.Stuck)
	if err != nil {
		e.logf("warning: can't read the answer to %s's stuck item: %v", name, err)
		s.Stuck = "" // asked again if it still is
		return nil, nil
	}
	if it == nil {
		if p, err := inbox.Get(e.Home, s.Stuck); err == nil && (!s.Turn || p.Hop != s.Hop) {
			inbox.Drop(e.Home, s.Stuck)
			s.Stuck = ""
		}
		return nil, nil
	}
	s.Stuck, s.Clock = "", now
	if !s.Turn || it.Hop != s.Hop {
		return nil, nil
	}
	h := r.ActiveOn(name)
	switch it.Answer {
	case inbox.Nudge:
		if h == nil {
			return nil, nil // finished meanwhile: nothing to remind it of
		}
		e.logf("↻ %s: nudged from the inbox", name)
		return &nudge{window: s.Window, text: "laight: " + Reminder(r.Spec.Step(s.Step), e.Rig.Laight)}, nil
	case inbox.Fail:
		if h == nil {
			e.logf("✕ %s: its turn given up on from the inbox", name)
			s.Turn = false
			return nil, nil
		}
		// A stopped agent's turn is over. A quiet one's may still be running: its seat waits for
		// the Stop hook before the next step, or a late `laight done` from that turn would finish
		// the next hop. A seat without hooks never says, so its turn is interrupted instead.
		var n *nudge
		turn := !slices.Contains(it.Choices, inbox.Nudge)
		if rt, err := runtime.For(r.Spec.SeatOf(name).Runtime); turn && err == nil && !rt.Hooks() {
			n, turn = &nudge{window: s.Window, keys: []string{"C-c"}}, false
		}
		e.logf("✕ %s: stuck; failed from the inbox", h.Step)
		err := state.WriteDone(e.Home, r.ID, state.Done{Step: h.Step, Hop: h.N, Exit: spec.ExitFailed,
			Out: map[string]any{"error": "stuck; failed from the inbox"}, Engine: true, Turn: turn})
		if err != nil && !errors.Is(err, state.ErrAlreadyDone) {
			e.logf("warning: %v", err)
		}
		return n, nil
	default:
		e.logf("… %s: waiting on from the inbox", name)
	}
	return nil, nil
}

// watchShell looks at a running shell step: its output log is its only trace. A stuck one can
// wait, or fail, which stops its command.
func (e *Engine) watchShell(r *state.Run, h *state.Hop, st *spec.Step, now time.Time) []state.Event {
	if h.Stuck != "" {
		it, err := inbox.Answered(e.Home, h.Stuck)
		switch {
		case err != nil:
			e.logf("warning: can't read the answer to %s's stuck item: %v", h.Step, err)
			h.Stuck = ""
		case it == nil:
		case it.Answer == inbox.Fail:
			h.Stuck, h.Clock = "", now
			if err := e.fail(r.ID, h, errors.New("stuck; failed from the inbox")); err != nil {
				e.logf("warning: %v", err)
			}
			e.stopShell(h.N)
		default:
			h.Stuck, h.Clock = "", now
			e.logf("… %s: waiting on from the inbox", h.Step)
		}
		return nil
	}
	timeout := st.Quiet()
	if timeout <= 0 {
		return nil
	}
	log := filepath.Join(state.Dir(e.Home, r.ID), ShellLog(h.Step, h.N))
	last := later(later(h.Clock, h.Started), mtime(log))
	if now.Sub(last) < timeout {
		return nil
	}
	b, _ := os.ReadFile(log)
	reason := fmt.Sprintf("shell step %s has been quiet for %s", h.Step, now.Sub(last).Round(time.Second))
	ev, err := e.raise(r, inbox.Item{Step: h.Step, Hop: h.N, Title: reason, Choices: []string{inbox.Wait, inbox.Fail},
		Detail: fmt.Sprintf("%s\nOutput: %s\n\n%s", reason, log, lastLines(string(b), 15))})
	if err != nil {
		e.logf("warning: can't raise an inbox item: %v", err)
		return nil
	}
	h.Stuck = ev.Data["item"].(string)
	return []state.Event{{Run: r.ID, Type: "step.timeout", Step: h.Step, Hop: h.N}, ev}
}

// raise queues a stuck item and returns its inbox.raised event.
func (e *Engine) raise(r *state.Run, it inbox.Item, secrets ...string) (state.Event, error) {
	it.ID, it.Run, it.Repo, it.Workflow, it.Kind = state.NewID(time.Now()), r.ID, r.Repo, r.Workflow, inbox.Stuck
	if err := inbox.Put(e.Home, it, secrets...); err != nil {
		return state.Event{}, err
	}
	e.logf("? %s (laight inbox %s %s)", it.Title, it.Short(), strings.Join(it.Choices, "|"))
	return state.Event{Run: r.ID, Type: "inbox.raised", Step: it.Step, Seat: it.Seat, Hop: it.Hop,
		Data: map[string]any{"item": it.ID, "kind": it.Kind}}, nil
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

// waitingOnPermission reports whether the seat has a permission item pending.
func waitingOnPermission(home, run, seat string) bool {
	items, _ := inbox.Pending(home)
	return slices.ContainsFunc(items, func(it *inbox.Item) bool {
		return it.Run == run && it.Seat == seat && it.Kind == inbox.Permission
	})
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
