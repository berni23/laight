// Package engine runs a workflow's steps on its rig: the loop of CLAUDE.md section 4. Each tick
// reads run.json and the files agents write (out/, seats/*.idle), acts, and appends events. Only
// the engine writes run.json.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/berni23/laight/internal/inbox"
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

	cmu     sync.Mutex                 // cancels is shared with running shell steps
	cancels map[int]context.CancelFunc // how to stop each running shell step, by hop
	busy    map[string]time.Time       // run/seat → not stuck before then: its worktree had a write
	told    map[string]bool            // inbox items raised by hooks already printed
	evOff   map[string]int64           // run → how much of its events.jsonl announce has read
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

// Run drives the run until it finishes and returns its final status (state.Succeeded or
// state.Failed) and why it failed. The caller tears the rig down. When ctx ends first, it
// returns ctx's error. Either way it returns once the shell steps it started are stopped: they
// run in their own process group, which closing the engine's window doesn't reach, and one
// still running when another branch ends the run must not hold up the teardown.
func (e *Engine) Run(ctx context.Context, id string) (status, reason string, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		e.shells.Wait()
	}()
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

// tick does one round: finish the hops that are done, route them, close the seat instances that
// are done, start what can start. Once the run fails, nothing new starts: it ends when the hops
// already running have finished (CLAUDE.md §3 rule 4).
func (e *Engine) tick(ctx context.Context, id string) (status, reason string, err error) {
	var starts []start
	var stops []string
	var nudges []nudge
	var events []state.Event
	r, err := state.Update(e.Home, id, func(r *state.Run) error {
		starts, stops, nudges, events = nil, nil, nil, nil
		if !r.Live() {
			status, reason = r.Status, r.Error
			return errors.New("not live") // nothing to save
		}
		status, reason, starts, stops, events = e.advance(r)
		if status != "" {
			// Saved with the state that implies it: an engine that dies before the teardown
			// must not find an empty queue later and call a failed run a success.
			r.Status, r.Error = status, reason
			return nil
		}
		var evs []state.Event
		nudges, evs = e.watch(r)
		events = append(events, evs...)
		return nil
	})
	if status != "" {
		for _, ev := range events {
			e.event(ev)
		}
		if r != nil {
			e.announceDenials(r) // a seat denied just before the run's last laight done
		}
		return status, reason, nil
	}
	if err != nil {
		return "", "", err
	}
	for _, ev := range events {
		e.event(ev)
	}
	e.announce(r)
	for _, name := range stops {
		if err := e.Rig.Tmux.KillWindow(r.Session, r.Seats[name].Window); err != nil {
			e.logf("warning: can't close %s: %v", name, err)
		}
	}
	for _, n := range nudges {
		e.apply(r, n)
	}
	for _, s := range starts {
		if err := e.deliver(ctx, r, s); err != nil {
			if ctx.Err() != nil {
				return "", "", ctx.Err()
			}
			if err := e.fail(id, &s.hop, kindOf(err), err); err != nil {
				return "", "", err
			}
		}
	}
	return "", "", nil
}

// advance moves the run forward in memory: it is called with run.json locked, and does no tmux
// work beyond looking. It returns a final status when the run is over, else the hops to start
// and the seat instances to close.
func (e *Engine) advance(r *state.Run) (status, reason string, starts []start, stops []string, events []state.Event) {
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
	// Rule 1, once: before any hop, unless an entry step is already expanded, or the run already
	// failed (a matrix that didn't expand fails it at hop 0).
	if r.Hops == 0 && len(r.Queue) == 0 && len(r.Active) == 0 && len(r.Matrix) == 0 && r.Failure == nil {
		r.Queue = entrySteps(r.Spec)
	}

	// failRun records why the run fails: the first failure wins, later ones are only step.done.
	failRun := func(f state.Failure) {
		if r.Failure != nil {
			return
		}
		e.redact(r, &f)
		f.At = time.Now().UTC()
		r.Failure = &f
		e.logf("✕ the run fails: %s; it ends once its running steps finish", f.Reason)
	}

	// 1. Hops that finished: `laight done`, a failure the engine recorded, a crashed seat, or a
	// gate a human answered.
	var running []*state.Hop
	for _, h := range r.Active {
		d, err := state.ReadDone(e.Home, r.ID, h.Step, h.N)
		if err != nil {
			e.logf("warning: %v", err)
		}
		if d == nil && h.Item != "" {
			if it, err := inbox.Answered(e.Home, h.Item); err != nil {
				if err := e.fail(r.ID, h, state.KindCrash, fmt.Errorf("can't read the answer to item %s: %w", state.Short(h.Item), err)); err != nil {
					e.logf("warning: %v", err)
				}
				d, _ = state.ReadDone(e.Home, r.ID, h.Step, h.N)
			} else if it != nil {
				ans := state.Done{Step: h.Step, Hop: h.N, Exit: it.Answer, Engine: true}
				if err := state.WriteDone(e.Home, r.ID, ans); err != nil && !errors.Is(err, state.ErrAlreadyDone) {
					e.logf("warning: %v", err)
				}
				if d, _ = state.ReadDone(e.Home, r.ID, h.Step, h.N); d == nil {
					d = &ans
				}
			}
		}
		if d == nil && h.Seat != "" && e.seatGone(r, h.Seat) {
			// Recorded like any other ending, so `laight done` racing the crash still wins.
			crash := state.Done{Step: h.Step, Hop: h.N, Exit: spec.ExitFailed, Engine: true, Kind: state.KindCrash,
				Out: map[string]any{"error": "seat " + h.Seat + " crashed"}, Detail: e.paneTail(r, h.Seat)}
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
		res := &state.StepResult{Hop: h.N, Exit: d.Exit, Out: d.Out}
		data := map[string]any{"exit": d.Exit, "seconds": int(time.Since(h.Started).Seconds())}
		if len(d.Out) > 0 {
			data["out"] = d.Out
		}
		if h.Copy > 0 {
			data["copy"] = h.Copy
		}
		ev("step.done", h, data)
		next := st.NextHop.On[d.Exit]
		m := r.Matrix[h.Step]
		switch {
		case h.Copy == 0:
			r.Steps[h.Step] = res
		case m != nil:
			m.Results[h.Copy-1] = res // a matrix step doesn't route (rule 6)
		}
		switch {
		case next == "" && d.Exit == spec.ExitFailed:
			e.logf("✕ %s failed", label(h.Step, h.Copy)) // failRun prints why, redacted
			failRun(e.failure(r, h, st, d))
		case h.Copy > 0:
			e.logf("✓ %s → %s", label(h.Step, h.Copy), d.Exit)
			if m != nil && finished(m) {
				// Every copy is through: that is what `needs` waits for (rule 2).
				r.Steps[h.Step] = &state.StepResult{Hop: h.N, Copies: m.Results}
				delete(r.Matrix, h.Step)
				e.logf("✓ %s: all %d copies done", h.Step, len(m.Results))
			}
		case next == "" || next == spec.End:
			e.logf("✓ %s → %s (branch ends)", h.Step, d.Exit)
		default:
			e.logf("✓ %s → %s → %s", h.Step, d.Exit, next)
			r.Queue = append(r.Queue, next)
		}
		// A seat without hooks is free now; one with hooks once its Stop hook says the turn ended.
		// An ending the engine wrote has no turn behind it to wait for, unless it says so.
		if s := r.Seats[h.Seat]; s != nil {
			if rt, err := runtime.For(r.Spec.SeatOf(h.Seat).Runtime); (d.Engine && !d.Turn) || (err == nil && !rt.Hooks()) {
				s.Turn = false
			}
		}
	}
	r.Active = running

	// 1b. Steps whose needs are all met (rule 2) join the queue. Every candidate is judged on the
	// state before this pass, so the order of the file doesn't matter: another candidate that can
	// route back to a need holds it, unless this one can too (then both are in that loop).
	var ready []*spec.Step
	for _, st := range r.Spec.Steps {
		if len(st.Needs) > 0 && r.Steps[st.ID] == nil && !live(r, st.ID) && len(unmet(r, st)) == 0 {
			ready = append(ready, st)
		}
	}
	for _, st := range ready {
		var others, own []string
		for _, o := range ready {
			if o != st {
				others = append(others, o.ID)
			}
		}
		for _, next := range st.NextHop.On {
			own = append(own, next)
		}
		held, loop := reach(r, others...), reach(r, own...)
		if slices.ContainsFunc(st.Needs, func(n string) bool { return held[n] && !loop[n] }) {
			continue
		}
		e.logf("✓ %s: needs met (%s)", st.ID, strings.Join(st.Needs, ", "))
		r.Queue = append(r.Queue, st.ID)
	}

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

	// begin starts a hop of st on seat (a seat, a seat instance, or none for shell steps and
	// gates), unless max_hops refuses it: then the run fails.
	begin := func(st *spec.Step, seat string, copy int, values map[string]any) {
		if r.Hops >= r.Spec.LoopGuards.MaxHops {
			failRun(state.Failure{Step: st.ID, Copy: copy, Kind: state.KindMaxHops,
				Reason: fmt.Sprintf("loop_guards.max_hops (%d) reached before step %s", r.Spec.LoopGuards.MaxHops, label(st.ID, copy))})
			return
		}
		r.Hops++
		h := &state.Hop{Step: st.ID, Seat: seat, N: r.Hops, Started: time.Now().UTC(), Copy: copy, Matrix: values}
		r.Active = append(r.Active, h)
		var data map[string]any
		if copy > 0 {
			data = map[string]any{"copy": copy, "matrix": values}
		}
		ev("step.started", h, data)
		if st.Type == spec.TypeGate {
			if it, err := e.gate(r, h); err != nil {
				if err := e.fail(r.ID, h, kindOf(err), err); err != nil {
					e.logf("warning: %v", err)
				}
			} else {
				ev("inbox.raised", h, map[string]any{"item": it.ID, "kind": it.Kind})
			}
			return
		}
		if seat == "" {
			starts = append(starts, start{hop: *h})
			return
		}
		s := r.Seats[seat]
		if s == nil { // a seat instance's first copy: the instance starts with it
			s = &state.Seat{Window: seat}
			r.Seats[seat] = s
		}
		if s.Up && e.seatGone(r, seat) { // its agent exited while it was idle
			events = append(events, state.Event{Run: r.ID, Type: "seat.crashed", Seat: seat})
			s.Up = false
		}
		restart := !s.Up || (r.Spec.SeatOf(seat).Session == "fresh" && s.Given > 0)
		s.Step, s.Hop, s.Turn, s.Given = st.ID, h.N, true, s.Given+1
		starts = append(starts, start{hop: *h, restart: restart})
	}
	busy := func(seat string) bool {
		s := r.Seats[seat]
		return s != nil && (s.Turn || r.ActiveOn(seat) != nil)
	}

	// 3. Queued steps whose seat is free (rule 5: one step per seat at a time). Shell steps and
	// gates have no seat: they start at once. A matrix step expands into its copies instead.
	var waiting []string
	for _, id := range r.Queue {
		st := r.Spec.Step(id)
		if r.Failure != nil { // a failing run starts nothing new
			waiting = append(waiting, id)
			continue
		}
		if r.Matrix[id] != nil { // routed to again while its copies run: it waits for them
			waiting = append(waiting, id)
			continue
		}
		if len(st.Matrix) > 0 {
			copies, err := spec.Copies(st, r.TemplateData(nil))
			if err != nil {
				failRun(state.Failure{Step: id, Kind: state.KindConfig, Reason: "step " + id + ": " + err.Error()})
				continue
			}
			m := &state.MatrixRun{Copies: copies, Results: make([]*state.StepResult, len(copies))}
			for k := range copies {
				m.Pending = append(m.Pending, k+1)
			}
			if r.Matrix == nil {
				r.Matrix = map[string]*state.MatrixRun{}
			}
			r.Matrix[id] = m
			e.logf("⊞ %s: %d copies", id, len(copies))
			continue
		}
		if st.Seat != "" && (r.Seats[st.Seat] == nil || busy(st.Seat)) {
			waiting = append(waiting, id)
			continue
		}
		begin(st, st.Seat, 0, nil)
	}
	r.Queue = waiting

	// 3b. Matrix copies, in copy order, up to max_parallel live at once. Copy k runs on its own
	// seat instance, <seat>#k (rule 5), and waits for it while another matrix step has it.
	for _, id := range slices.Sorted(maps.Keys(r.Matrix)) {
		if r.Failure != nil {
			break
		}
		m, st := r.Matrix[id], r.Spec.Step(id)
		live := 0
		for _, h := range r.Active {
			if h.Step == id {
				live++
			}
		}
		var later []int
		for _, k := range m.Pending {
			seat := ""
			if st.Seat != "" {
				seat = spec.Instance(st.Seat, k)
			}
			if r.Failure != nil || live >= st.MaxParallel || (seat != "" && busy(seat)) {
				later = append(later, k)
				continue
			}
			n := len(r.Active)
			begin(st, seat, k, m.Copies[k-1])
			live += len(r.Active) - n
		}
		m.Pending = later
	}

	// A seat instance closes once its copy is over. Its worktree stays (on_finish decides).
	for _, name := range slices.Sorted(maps.Keys(r.Seats)) {
		if s := r.Seats[name]; strings.Contains(name, "#") && s.Up && !s.Turn && r.ActiveOn(name) == nil {
			s.Up = false
			stops = append(stops, name)
			events = append(events, state.Event{Run: r.ID, Type: "seat.down", Seat: name})
		}
	}

	// 4. Rule 4: a failed run ends once nothing but gates runs; any other, once nothing runs or
	// waits. A
	// step whose needs were never met (a needed step ended on an exit routed elsewhere, or never
	// ran) is skipped.
	if r.Failure != nil && !slices.ContainsFunc(r.Active, func(h *state.Hop) bool { return h.Item == "" }) {
		// Only gates are left, if anything: their exits could start nothing, and nobody watches
		// them. Down withdraws their items.
		return state.Failed, r.Failure.Reason, nil, stops, events
	}
	if len(r.Active) == 0 && len(r.Queue) == 0 && len(r.Matrix) == 0 {
		for _, st := range r.Spec.Steps {
			if len(st.Needs) > 0 && r.Steps[st.ID] == nil {
				miss := unmet(r, st)
				e.logf("⊘ %s skipped: needs %s", st.ID, strings.Join(miss, ", "))
				events = append(events, state.Event{Run: r.ID, Type: "step.skipped", Step: st.ID,
					Data: map[string]any{"needs": miss}})
			}
		}
		return state.Succeeded, "", nil, nil, events
	}
	return "", "", starts, stops, events
}

// gate asks a gate's question in the inbox; the answer, one of the step's exits, finishes the hop.
func (e *Engine) gate(r *state.Run, h *state.Hop) (*inbox.Item, error) {
	st := r.Spec.Step(h.Step)
	title, err := spec.Render(st.ID+".ask", st.Ask, r.TemplateData(h))
	if err != nil {
		return nil, configError{fmt.Errorf("ask: %w", err)}
	}
	h.Item = state.NewID(time.Now())
	it := inbox.Item{ID: h.Item, Run: r.ID, Repo: r.Repo, Workflow: r.Workflow, Step: st.ID, Hop: h.N,
		Kind: inbox.Gate, Title: strings.TrimSpace(title), Choices: st.AllowedExits}
	if err := inbox.Put(e.Home, it); err != nil {
		return nil, err
	}
	e.logf("? %s: %s (laight inbox %s %s)", st.ID, it.Title, it.Short(), strings.Join(it.Choices, "|"))
	return &it, nil
}

// configError is a template in the workflow that didn't render: the YAML is wrong, not an agent
// or a seat (CircleCI's "errored, type config"). Declared outputs (#82) will catch most of them
// before the run starts.
type configError struct{ error }

func (e configError) Unwrap() error { return e.error }

// kindOf is the failure kind of an error that kept a step from starting.
func kindOf(err error) string {
	var ce configError
	if errors.As(err, &ce) {
		return state.KindConfig
	}
	return state.KindCrash
}

// fail finishes a hop as failed, of the given kind, so it goes through routing like any other
// ending.
func (e *Engine) fail(id string, h *state.Hop, kind string, err error) error {
	e.logf("✕ %s: %v", h.Step, err)
	msg, _, _ := strings.Cut(err.Error(), "\n")
	err = state.WriteDone(e.Home, id, state.Done{
		Step: h.Step, Hop: h.N, Exit: spec.ExitFailed, Out: map[string]any{"error": msg}, Engine: true, Kind: kind,
	})
	if errors.Is(err, state.ErrAlreadyDone) {
		return nil
	}
	return err
}

// deliver gives a started hop to its seat: relaunch it if needed, compact it if the step says
// so, then paste the objective. A shell step runs its command instead.
func (e *Engine) deliver(ctx context.Context, r *state.Run, s start) error {
	st := r.Spec.Step(s.hop.Step)
	if st.Type == spec.TypeShell {
		return e.shell(ctx, r, s.hop)
	}
	text, err := spec.Render(st.ID+".objective", st.Objective, r.TemplateData(&s.hop))
	if err != nil {
		return configError{fmt.Errorf("objective: %w", err)}
	}
	seat := s.hop.Seat // the step's seat, or a matrix copy's instance of it
	if s.restart {
		if err := e.Rig.Restart(ctx, r.ID, seat); err != nil {
			return fmt.Errorf("seat %s: %w", seat, err)
		}
	}
	e.logf("▶ %s (seat %s, hop %d)", label(st.ID, s.hop.Copy), seat, s.hop.N)
	if st.Compact {
		// Claude queues the objective behind the compaction, and runs it once that is done.
		// Compacting ends no turn (no Stop hook), so the step still ends with laight done.
		if err := e.Rig.Tmux.Paste(r.Session, r.Seats[seat].Window, "/compact"); err != nil {
			return err
		}
	}
	return e.Rig.Tmux.Paste(r.Session, r.Seats[seat].Window, strings.TrimRight(text, "\n"))
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

// live reports whether step is queued, running, or has matrix copies under way.
func live(r *state.Run, step string) bool {
	if r.Matrix[step] != nil {
		return true
	}
	for _, id := range r.Queue {
		if id == step {
			return true
		}
	}
	for _, h := range r.Active {
		if h.Step == step {
			return true
		}
	}
	return false
}

// unmet returns the needs of st that haven't finished (rule 2): a needed step has finished when
// its latest hop ended on an exit other than failed and no live step can route back to it, so a
// step in a review/fix loop counts only once the loop has left it.
func unmet(r *state.Run, st *spec.Step) []string {
	var running []string
	for _, s := range r.Spec.Steps {
		if live(r, s.ID) {
			running = append(running, s.ID)
		}
	}
	again := reach(r, running...) // steps that can still start: the live ones and every route from them
	var miss []string
	for _, n := range st.Needs {
		if res := r.Steps[n]; res == nil || res.Exit == spec.ExitFailed || again[n] {
			miss = append(miss, n)
		}
	}
	return miss
}

// reach returns the steps from and every step their routes can lead to.
func reach(r *state.Run, from ...string) map[string]bool {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		if s := r.Spec.Step(id); s != nil {
			for _, next := range s.NextHop.On {
				walk(next)
			}
		}
	}
	for _, id := range from {
		walk(id)
	}
	return seen
}

// IdleMark is what a seat's Stop hook writes to seats/<seat>.idle once the agent's turn ends
// after it finished step-hop: the seat may get its next step.
func IdleMark(step string, hop int) string { return fmt.Sprintf("%s-%d", step, hop) }

// finished reports whether every copy of a matrix step has finished without failing.
func finished(m *state.MatrixRun) bool {
	if len(m.Pending) > 0 {
		return false
	}
	for _, res := range m.Results {
		if res == nil || res.Exit == spec.ExitFailed {
			return false
		}
	}
	return true
}

// label names a step, or one copy of a matrix step: build#2.
func label(step string, copy int) string {
	if copy == 0 {
		return step
	}
	return spec.Instance(step, copy)
}

func errSuffix(out map[string]any) string {
	if msg := reasonOf(out); msg != "" {
		return ": " + msg
	}
	return ""
}

// reasonOf is why a hop failed, from its outputs: the agent's `reason=`, else the engine's error.
func reasonOf(out map[string]any) string {
	for _, k := range []string{"reason", "error"} {
		if msg, ok := out[k].(string); ok && msg != "" {
			return msg
		}
	}
	return ""
}

// failure is the run's failure report for a hop that failed with no route. Its kind is what the
// engine recorded, else an agent's exit or a shell step's. Its detail is the end of the pane or
// the shell log, redacted, unless the engine already captured it.
func (e *Engine) failure(r *state.Run, h *state.Hop, st *spec.Step, d *state.Done) state.Failure {
	f := state.Failure{Step: h.Step, Hop: h.N, Seat: h.Seat, Copy: h.Copy, Kind: d.Kind, Detail: d.Detail}
	if f.Kind == "" {
		f.Kind = state.KindExit
		if st.Type == spec.TypeShell {
			f.Kind = state.KindShell
		} else if evs, _, err := state.EventsFrom(e.Home, r.ID, 0); err == nil && slices.ContainsFunc(evs, func(ev state.Event) bool {
			return ev.Type == "permission.denied" && ev.Step == h.Step && ev.Hop == h.N
		}) {
			f.Kind = state.KindPermission // it gave up after a request was denied
		}
	}
	f.Reason = "step " + label(h.Step, h.Copy) + " failed" + errSuffix(d.Out)
	if f.Detail == "" {
		if st.Type == spec.TypeShell {
			b, _ := os.ReadFile(filepath.Join(state.Dir(e.Home, r.ID), ShellLog(h.Step, h.N)))
			f.Detail = inbox.Scrub(lastLines(string(b), 15))
		} else if h.Seat != "" {
			f.Detail = e.paneTail(r, h.Seat)
		}
	}
	return f
}

// redact strips a failure's reason and detail of the seat's secrets, anything that looks like a
// token, and control characters, before it reaches run.json, events and the user's terminal
// (§12.4). The reason is cut to an excerpt.
func (e *Engine) redact(r *state.Run, f *state.Failure) {
	var secrets []string
	if f.Seat != "" && r.Spec.SeatOf(f.Seat) != nil {
		var err error
		if secrets, err = rig.Secrets(r, f.Seat); err != nil {
			e.logf("warning: %s: %v", f.Seat, err)
		}
	}
	f.Reason = inbox.Scrub(inbox.Clean(f.Reason), secrets...)
	if len(f.Reason) > 500 {
		f.Reason = strings.ToValidUTF8(f.Reason[:500], "") + "…"
	}
	f.Detail = inbox.Scrub(inbox.Clean(f.Detail), secrets...)
}

// paneTail is the last lines of a seat's pane, its secrets stripped, or a note saying why it
// isn't quoted.
func (e *Engine) paneTail(r *state.Run, seat string) string {
	s := r.Seats[seat]
	if s == nil {
		return ""
	}
	secrets, err := rig.Secrets(r, seat)
	if err != nil {
		e.logf("warning: %s: %v", seat, err)
		return "(the pane isn't quoted: its secrets can't be read to strip them)"
	}
	screen, err := e.Rig.Tmux.Capture(r.Session, s.Window)
	if err != nil {
		return ""
	}
	return inbox.Scrub(lastLines(screen, 15), secrets...)
}
