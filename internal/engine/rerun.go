package engine

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
)

// Rerun builds the run that continues a red one from where it failed (#66): the current workflow
// w, the old run's inputs (as given), its finished steps' results and its seats' worktrees and
// conversations, and the hop count, so loop_guards.max_hops stays a guard across reruns. What
// starts again is what the old run left unfinished: the step that failed, the steps it had routed
// to, the hops an abort cut short, and the matrix copies that failed or never ran. A run that
// failed before its first hop starts over.
//
// Anything in w may change (decided on #66, "shape only"); a rerun is refused only when the
// carried state no longer fits: a step whose result is carried, or that starts again, is gone, a
// matrix step changed shape, or one being continued is no longer a matrix. A seat whose runtime
// or cwd changed, or whose worktree is gone, starts fresh.
func Rerun(old *state.Run, w *spec.Workflow, inputs map[string]string, id string, now time.Time) (*state.Run, error) {
	if old.Live() || (old.Status != state.Failed && old.Status != state.Aborted) {
		return nil, fmt.Errorf("run %s is %s: only a failed or aborted run can be rerun", old.Short(), old.Status)
	}
	if old.RerunAs != "" {
		return nil, fmt.Errorf("run %s was rerun already, as %s: rerun that one", old.Short(), state.Short(old.RerunAs))
	}
	if old.Workflow != w.ID {
		return nil, fmt.Errorf("run %s is of workflow %s, not %s", old.Short(), old.Workflow, w.ID)
	}
	r := &state.Run{
		ID: id, Workflow: w.ID, Repo: old.Repo, Session: state.SessionName(w.ID, id),
		Inputs: inputs, Spec: w, SpecDir: old.SpecDir, Base: old.Base, Status: state.Starting,
		Seats: map[string]*state.Seat{}, Created: now.UTC(), RerunOf: old.ID,
	}
	if old.Hops == 0 && len(old.Matrix) == 0 {
		return r, nil // nothing ran: start over (rule 1)
	}
	r.Hops = old.Hops
	var problems []string
	gone := func(what, step string) {
		problems = append(problems, fmt.Sprintf("%s %s is no longer in the workflow", what, step))
	}

	// What starts again: queued and cut-short hops, and the failed step.
	var again []string
	add := func(step string) {
		if !slices.Contains(again, step) {
			again = append(again, step)
		}
	}
	for _, step := range old.Queue {
		add(step)
	}
	for _, h := range old.Active {
		if h.Copy == 0 {
			add(h.Step)
		}
	}
	if f := old.Failure; f != nil && f.Step != "" && f.Copy == 0 && old.Matrix[f.Step] == nil {
		add(f.Step)
	}

	// Matrix steps under way go on with their own expansion: the copies that failed, were cut
	// short or never started run again; the others keep their results.
	for _, step := range slices.Sorted(maps.Keys(old.Matrix)) {
		m, st := old.Matrix[step], w.Step(step)
		if st == nil {
			gone("matrix step", step)
			continue
		}
		if len(st.Matrix) == 0 {
			problems = append(problems, fmt.Sprintf("step %s had matrix copies under way and is no longer a matrix step", step))
			continue
		}
		next := &state.MatrixRun{Copies: m.Copies, Results: slices.Clone(m.Results)}
		for k, res := range next.Results {
			if res == nil || res.Exit == spec.ExitFailed {
				next.Results[k] = nil
				next.Pending = append(next.Pending, k+1)
			}
		}
		if r.Matrix == nil {
			r.Matrix = map[string]*state.MatrixRun{}
		}
		r.Matrix[step] = next
	}
	for _, step := range again {
		if w.Step(step) == nil {
			gone("step", step)
		}
	}
	r.Queue = again

	// Finished steps keep their results, but for the ones that start again.
	r.Steps = map[string]*state.StepResult{}
	for _, step := range slices.Sorted(maps.Keys(old.Steps)) {
		res := old.Steps[step]
		if slices.Contains(again, step) || r.Matrix[step] != nil {
			continue
		}
		st := w.Step(step)
		switch {
		case st == nil:
			gone("step", step+" (it finished in run "+old.Short()+")")
		case (len(st.Matrix) > 0) != (res.Copies != nil):
			problems = append(problems, fmt.Sprintf("step %s changed between a matrix step and a plain one since it finished", step))
		default:
			r.Steps[step] = res
		}
	}
	if len(r.Queue) == 0 && len(r.Matrix) == 0 {
		problems = append(problems, "nothing to rerun: no step failed, was cut short or was waiting to start")
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("run %s can't be rerun with the workflow as it is now:\n  %s", old.Short(), strings.Join(problems, "\n  "))
	}

	for name, prev := range old.Seats {
		if s := carrySeat(old, w, name, prev); s != nil {
			r.Seats[name] = s
		}
	}
	return r, nil
}

// carrySeat is what a rerun keeps of a seat: its worktree, where it still is and the seat still
// works in one, and its conversation, where it resumes in the same folder with the same runtime.
// A seat the workflow dropped keeps only its worktree, so the rerun still cleans it up.
func carrySeat(old *state.Run, w *spec.Workflow, name string, prev *state.Seat) *state.Seat {
	was, now := old.Spec.SeatOf(name), w.SeatOf(name)
	s := &state.Seat{Window: name}
	if prev.Worktree {
		if _, err := os.Stat(prev.Dir); err == nil && (now == nil || now.Cwd == "worktree") {
			s.Dir, s.Worktree, s.SetupDone = prev.Dir, true, prev.SetupDone
		}
	}
	if now == nil || was == nil {
		if !s.Worktree {
			return nil
		}
		return s
	}
	sameDir := s.Worktree || (!prev.Worktree && was.Cwd == now.Cwd)
	if sameDir && was.Runtime == now.Runtime && now.Session != "fresh" && prev.SessionID != "" {
		s.SessionID, s.Rerun = prev.SessionID, true
	}
	return s
}

// RerunNote is pasted before the first step a resumed seat gets in a rerun: its conversation goes
// on, and the agent should know why it starts again and that the workflow may have changed.
func RerunNote(old string, f *state.Failure) string {
	why := "it ended red"
	if f != nil {
		why = fmt.Sprintf("it failed (%s): %s", f.Kind, f.Reason)
	}
	return fmt.Sprintf("[laight] This is a rerun of run %s, which you were part of; %s. The cause may have been fixed and the workflow changed since. Carry on with the step below.\n\n", state.Short(old), why)
}
