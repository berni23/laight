package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/berni23/laight/internal/engine"
	"github.com/berni23/laight/internal/spec"
	"github.com/berni23/laight/internal/state"
	"github.com/berni23/laight/internal/tmux"
)

// cmdRerun is `laight run --from-failed <run>` (#66): a new run of the red run's workflow, as the
// file is now, that keeps what the red run finished (outputs, worktrees, conversations) and starts
// again from what failed. The fix goes in first: a deny rule, a credential, an objective.
func cmdRerun(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return fail(stderr, "usage: laight run --from-failed <run>")
	}
	p, err := spec.FindProject(".")
	if err != nil {
		return fail(stderr, "%v", err)
	}
	home, laight, err := homeAndBinary()
	if err != nil {
		return fail(stderr, "%v", err)
	}
	srv := tmux.Server{Socket: state.Socket(home)}
	old, err := state.Find(home, p.Root, args[0], func(*state.Run) bool { return false })
	if err != nil {
		return fail(stderr, "%v", err)
	}
	if old.Repo != p.Root {
		return fail(stderr, "run %s is of the project at %s: rerun it from there", old.Short(), old.Repo)
	}
	if old.Live() {
		if engineDied(srv, old) {
			return fail(stderr, "run %s: its engine died; end it with `laight abort %s`, then rerun it", old.Short(), old.Short())
		}
		return fail(stderr, "run %s is still %s", old.Short(), old.Status)
	}
	file, err := p.WorkflowFile(old.Workflow)
	if err != nil {
		return fail(stderr, "%v", err)
	}
	w, probs := spec.Load(p.Dir, file)
	for _, pr := range probs {
		fmt.Fprintln(stderr, pr)
	}
	if w == nil || spec.HasErrors(probs) {
		return fail(stderr, "fix the workflow first (`laight validate %s`)", old.Workflow)
	}
	// The red run's inputs, as given; an input the workflow gained since takes its default.
	var given []string
	for _, k := range slices.Sorted(maps.Keys(old.Inputs)) {
		if _, ok := w.Inputs[k]; ok {
			given = append(given, k+"="+old.Inputs[k])
		}
	}
	inputs, err := parseInputs(w, given)
	if err != nil {
		var missing []string
		for _, k := range slices.Sorted(maps.Keys(w.Inputs)) {
			if _, had := old.Inputs[k]; !had && w.Inputs[k].Required && w.Inputs[k].Default == nil {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 { // a rerun takes the red run's inputs, and no others
			return fail(stderr, "input %s is required and new since run %s: give it a default in the workflow to rerun it", strings.Join(missing, ", "), old.Short())
		}
		return fail(stderr, "%v", err)
	}
	// Refused before the preflight, which may ask for a login: a rerun that can't happen must not.
	id := state.NewID(time.Now())
	if _, err := engine.Rerun(old, w, inputs, id, time.Now()); err != nil {
		return fail(stderr, "%v", err)
	}
	if code := beforeRun(w, p.Root, stdout, stderr); code != 0 {
		return code
	}

	// Built and created under the red run's lock, which records the hand-over: two reruns of one
	// run can't both take its worktrees, nor can an abort delete them as they go. If recording it
	// fails, the new run goes: its worktrees are still the red run's.
	var r *state.Run
	created := false
	if _, err := state.Update(home, old.ID, func(o *state.Run) error {
		var err error
		if r, err = engine.Rerun(o, w, inputs, id, time.Now()); err != nil {
			return err
		}
		r.SpecDir = p.Dir
		if err := state.Create(home, r); err != nil {
			return err
		}
		created, o.RerunAs = true, id
		return nil
	}); err != nil {
		if created {
			os.RemoveAll(state.Dir(home, id))
		}
		return fail(stderr, "%v", err)
	}
	state.Append(home, state.Event{Run: old.ID, Type: "run.rerun", Data: map[string]any{"as": id}})
	fmt.Fprintf(stdout, "rerun of %s: %s\n", old.Short(), rerunPlan(r))
	return startRun(home, laight, r, stdout, stderr)
}

// rerunPlan says what a rerun keeps and what it starts again.
func rerunPlan(r *state.Run) string {
	if r.Hops == 0 && len(r.Matrix) == 0 { // as engine.Rerun decides it
		return "it failed before its first step, so it starts over"
	}
	var again []string
	again = append(again, r.Queue...)
	for _, step := range slices.Sorted(maps.Keys(r.Matrix)) {
		m := r.Matrix[step]
		copies := make([]string, len(m.Pending))
		for i, k := range m.Pending {
			copies[i] = fmt.Sprint(k)
		}
		again = append(again, fmt.Sprintf("%s (copies %s of %d)", step, strings.Join(copies, ", "), len(m.Copies)))
	}
	kept := slices.Sorted(maps.Keys(r.Steps))
	msg := "starts again at " + strings.Join(again, ", ")
	if len(kept) > 0 {
		msg += "; keeps " + strings.Join(kept, ", ")
	}
	var wt []string
	for _, name := range slices.Sorted(maps.Keys(r.Seats)) {
		if r.Seats[name].Worktree {
			wt = append(wt, name)
		}
	}
	if len(wt) > 0 {
		msg += "; worktrees of " + strings.Join(wt, ", ")
	}
	return msg
}
