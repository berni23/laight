// Package engine runs a workflow's steps on its rig: the loop of CLAUDE.md section 4. Each tick
// reads run.json and the files agents write (out/, seats/*.idle), acts, and appends events. Only
// the engine writes run.json, so a restarted engine continues from the files alone.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/berni23/laight/internal/rig"
	"github.com/berni23/laight/internal/runtime"
	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// Poll is how often the engine looks at the run's files.
var Poll = 500 * time.Millisecond

// Engine drives the runs of one laight home.
type Engine struct {
	Home string
	Rig  *rig.Rig
	Log  io.Writer // progress lines, shown in the engine window

	mu     sync.Mutex     // Log is shared with running shell steps
	shells sync.WaitGroup // running shell steps
}

func (e *Engine) logf(format string, args ...any) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Log != nil {
		fmt.Fprintf(e.Log, format+"\n", args...)
	}
}

func (e *Engine) event(ev state.Event) {
	if err := state.Append(e.Home, ev); err != nil {
		e.logf("warning: can't write events.jsonl: %v", err)
	}
}

// Supported reports the first thing in the workflow this engine can't run yet, or nil.
func Supported(w *spec.Workflow) error {
	for _, st := range w.Steps {
		switch {
		case st.Type == spec.TypeGate:
			return fmt.Errorf("step %s: gates are not supported yet (#9)", st.ID)
		case len(st.Needs) > 0 || len(st.Matrix) > 0:
			return fmt.Errorf("step %s: needs and matrix are not supported yet (#12)", st.ID)
		}
	}
	return nil
}

// Run drives the run until it finishes and returns its final status (state.Succeeded or
// state.Failed) and why it failed. The caller tears the rig down. When ctx ends first, it
// returns ctx's error, once the shell steps it started are stopped: they run in their own
// process group, which closing the engine's window doesn't reach.
func (e *Engine) Run(ctx context.Context, id string) (status, reason string, err error) {
	defer e.shells.Wait()
	for {
		status, reason, err := e.tick(ctx, id)
		if err != nil || status != "" {
			return status, reason, err
		}
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-time.After(Poll):
		}
	}
}

// start is a hop the engine has to deliver after updating run.json.
type start struct {
	hop     state.Hop
	restart bool // relaunch the seat first (session: fresh, or its pane died)
}

// tick does one round: finish the hops that are done, route them, start what can start.
func (e *Engine) tick(ctx context.Context, id string) (status, reason string, err error) {
	var starts []start
	var events []state.Event
	r, err := state.Update(e.Home, id, func(r *state.Run) error {
		starts, events = nil, nil
		if !r.Live() {
			status, reason = r.Status, r.Error
			return errors.New("not live") // nothing to save
		}
		status, reason, starts, events = e.advance(r)
		if status != "" {
			// Saved with the state that implies it: an engine that dies before the teardown
			// must not find an empty queue later and call a failed run a success.
			r.Status, r.Error = status, reason
		}
		return nil
	})
	if status != "" {
		for _, ev := range events {
			e.event(ev)
		}
		return status, reason, nil
	}
	if err != nil {
		return "", "", err
	}
	for _, ev := range events {
		e.event(ev)
	}
	for _, s := range starts {
		if err := e.deliver(ctx, r, s); err != nil {
			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
			// Finish the hop as failed, so it goes through routing like any other ending.
			e.logf("✕ %s: %v", s.hop.Step, err)
			msg, _, _ := strings.Cut(err.Error(), "\n")
			if err := state.WriteDone(e.Home, id, state.Done{
				Step: s.hop.Step, Hop: s.hop.N, Exit: spec.ExitFailed, Out: map[string]any{"error": msg}, Engine: true,
			}); err != nil && !errors.Is(err, state.ErrAlreadyDone) {
				return "", "", err
			}
		}
	}
	return "", "", nil
}

// advance moves the run forward in memory: it is called with run.json locked, and does no tmux
// work beyond looking. It returns a final status when the run is over.
func (e *Engine) advance(r *state.Run) (status, reason string, starts []start, events []state.Event) {
	ev := func(typ string, h *state.Hop, data map[string]any) {
		x := state.Event{Run: r.ID, Type: typ, Data: data}
		if h != nil {
			x.Step, x.Seat, x.Hop = h.Step, h.Seat, h.N
		}
		events = append(events, x)
	}
	if r.Steps == nil {
		r.Steps = map[string]*state.StepResult{}
	}
	if r.Hops == 0 && len(r.Queue) == 0 && len(r.Active) == 0 {
		r.Queue = entrySteps(r.Spec)
	}

	// 1. Hops that finished: `laight done`, a failure the engine recorded, or a crashed seat.
	var running []*state.Hop
	for _, h := range r.Active {
		d, err := state.ReadDone(e.Home, r.ID, h.Step, h.N)
		if err != nil {
			e.logf("warning: %v", err)
		}
		if d == nil && h.Seat != "" && e.seatGone(r, h.Seat) {
			// Recorded like any other ending, so `laight done` racing the crash still wins.
			crash := state.Done{Step: h.Step, Hop: h.N, Exit: spec.ExitFailed, Engine: true,
				Out: map[string]any{"error": "seat " + h.Seat + " crashed"}}
			if err := state.WriteDone(e.Home, r.ID, crash); err != nil && !errors.Is(err, state.ErrAlreadyDone) {
				e.logf("warning: %v", err)
			}
			if d, _ = state.ReadDone(e.Home, r.ID, h.Step, h.N); d == nil {
				d = &crash
			}
			if d.Engine {
				ev("seat.crashed", h, nil)
			}
			if s := r.Seats[h.Seat]; s != nil {
				s.Up, s.Turn = false, false
			}
		}
		if d == nil {
			running = append(running, h)
			continue
		}
		st := r.Spec.Step(h.Step)
		r.Steps[h.Step] = &state.StepResult{Hop: h.N, Exit: d.Exit, Out: d.Out}
		data := map[string]any{"exit": d.Exit, "seconds": int(time.Since(h.Started).Seconds())}
		if len(d.Out) > 0 {
			data["out"] = d.Out
		}
		ev("step.done", h, data)
		next := st.NextHop.On[d.Exit]
		switch {
		case next == "" && d.Exit == spec.ExitFailed:
			e.logf("✕ %s failed%s", h.Step, errSuffix(d.Out))
			r.Active = nil
			return state.Failed, "step " + h.Step + " failed" + errSuffix(d.Out), nil, events
		case next == "" || next == spec.End:
			e.logf("✓ %s → %s (branch ends)", h.Step, d.Exit)
		default:
			e.logf("✓ %s → %s → %s", h.Step, d.Exit, next)
			r.Queue = append(r.Queue, next)
		}
		// A seat without hooks is free now; one with hooks once its Stop hook says the turn ended.
		// An ending the engine wrote has no turn behind it to wait for.
		if s := r.Seats[h.Seat]; s != nil {
			if rt, err := runtime.For(r.Spec.Seats[h.Seat].Runtime); d.Engine || (err == nil && !rt.Hooks()) {
				s.Turn = false
			}
		}
	}
	r.Active = running

	// 2. Seats whose turn ended after their step finished, or whose agent exited instead.
	for name, s := range r.Seats {
		if s.Turn && r.ActiveOn(name) == nil {
			b, _ := os.ReadFile(filepath.Join(state.Dir(e.Home, r.ID), "seats", name+".idle"))
			switch {
			case strings.TrimSpace(string(b)) == IdleMark(s.Step, s.Hop):
				s.Turn = false
			case e.seatGone(r, name):
				events = append(events, state.Event{Run: r.ID, Type: "seat.crashed", Seat: name})
				s.Up, s.Turn = false, false
			}
		}
	}

	// 3. Queued steps whose seat is free (rule 5: one step per seat at a time). Shell steps have
	// no seat: they start at once.
	var waiting []string
	for _, id := range r.Queue {
		st := r.Spec.Step(id)
		s := r.Seats[st.Seat]
		if st.Type != spec.TypeShell && (s == nil || s.Turn || r.ActiveOn(st.Seat) != nil) {
			waiting = append(waiting, id)
			continue
		}
		if r.Hops >= r.Spec.LoopGuards.MaxHops {
			e.logf("✕ max_hops (%d) reached before %s", r.Spec.LoopGuards.MaxHops, id)
			return state.Failed, fmt.Sprintf("loop_guards.max_hops (%d) reached before step %s", r.Spec.LoopGuards.MaxHops, id), nil, events
		}
		r.Hops++
		h := &state.Hop{Step: id, Seat: st.Seat, N: r.Hops, Started: time.Now().UTC()}
		r.Active = append(r.Active, h)
		if s == nil {
			starts = append(starts, start{hop: *h})
			ev("step.started", h, nil)
			continue
		}
		if s.Up && e.seatGone(r, st.Seat) { // its agent exited while it was idle
			events = append(events, state.Event{Run: r.ID, Type: "seat.crashed", Seat: st.Seat})
			s.Up = false
		}
		restart := !s.Up || (r.Spec.Seats[st.Seat].Session == "fresh" && s.Given > 0)
		s.Step, s.Hop, s.Turn, s.Given = id, h.N, true, s.Given+1
		starts = append(starts, start{hop: *h, restart: restart})
		ev("step.started", h, nil)
	}
	r.Queue = waiting

	// 4. Rule 4: nothing running or waiting, and nothing failed unrouted.
	if len(r.Active) == 0 && len(r.Queue) == 0 {
		return state.Succeeded, "", nil, events
	}
	return "", "", starts, events
}

// deliver gives a started hop to its seat: relaunch it if needed, then paste the objective. A
// shell step runs its command instead.
func (e *Engine) deliver(ctx context.Context, r *state.Run, s start) error {
	st := r.Spec.Step(s.hop.Step)
	if st.Type == spec.TypeShell {
		return e.shell(ctx, r, s.hop)
	}
	text, err := spec.Render(st.ID+".objective", st.Objective, r.TemplateData())
	if err != nil {
		return fmt.Errorf("objective: %w", err)
	}
	if s.restart {
		if err := e.Rig.Restart(ctx, r.ID, st.Seat); err != nil {
			return fmt.Errorf("seat %s: %w", st.Seat, err)
		}
	}
	e.logf("▶ %s (seat %s, hop %d)", st.ID, st.Seat, s.hop.N)
	return e.Rig.Tmux.Paste(r.Session, r.Seats[st.Seat].Window, strings.TrimRight(text, "\n"))
}

// seatGone reports whether the seat's agent has exited or its window was closed. When tmux can't
// tell, it says no: a hiccup must not fail a live step; the next tick asks again.
func (e *Engine) seatGone(r *state.Run, seat string) bool {
	s := r.Seats[seat]
	if s == nil {
		return true
	}
	alive, err := e.Rig.Tmux.Alive(r.Session, s.Window)
	if err != nil && !errors.Is(err, tmux.ErrGone) {
		e.logf("warning: can't check seat %s: %v", seat, err)
		return false
	}
	return !alive
}

// entrySteps is rule 1 of CLAUDE.md section 3: the first step (unless it has needs), and every
// other step with no needs that no route leads to.
func entrySteps(w *spec.Workflow) []string {
	routed := map[string]bool{}
	for _, st := range w.Steps {
		for _, t := range st.NextHop.On {
			routed[t] = true
		}
	}
	var ids []string
	for i, st := range w.Steps {
		if len(st.Needs) == 0 && (i == 0 || !routed[st.ID]) {
			ids = append(ids, st.ID)
		}
	}
	return ids
}

// IdleMark is what a seat's Stop hook writes to seats/<seat>.idle once the agent's turn ends
// after it finished step-hop: the seat may get its next step.
func IdleMark(step string, hop int) string { return fmt.Sprintf("%s-%d", step, hop) }

func errSuffix(out map[string]any) string {
	if msg, ok := out["error"].(string); ok && msg != "" {
		return ": " + msg
	}
	return ""
}
