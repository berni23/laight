package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/berni23/laight/internal/state"
)

// report prints a finished run's report (CLAUDE.md §6.3): its status, and for a red run which
// step failed, in which hop and seat, of what kind, why, and the end of its pane or log. Every
// run's report lists the requests its seats were denied, green runs too.
func report(w io.Writer, home string, r *state.Run) {
	if f := r.Failure; f != nil && r.Status != state.Succeeded {
		fmt.Fprintf(w, "run %s %s\n", r.Short(), r.Status)
		where := []string{}
		if f.Hop > 0 {
			where = append(where, fmt.Sprintf("hop %d", f.Hop))
		}
		if f.Seat != "" {
			where = append(where, "seat "+f.Seat)
		}
		step := f.Step
		if f.Copy > 0 {
			step = fmt.Sprintf("%s#%d", f.Step, f.Copy)
		}
		head := "✕ " + f.Kind
		if step != "" {
			head = "✕ " + step
			if len(where) > 0 {
				head += " (" + strings.Join(where, ", ") + ")"
			}
			head += ": " + f.Kind
		}
		fmt.Fprintf(w, "  %s\n    %s\n", head, f.Reason)
		for _, l := range strings.Split(f.Detail, "\n") {
			if strings.TrimSpace(l) != "" {
				fmt.Fprintf(w, "    │ %s\n", l)
			}
		}
	} else if r.Error != "" {
		fmt.Fprintf(w, "run %s %s: %s\n", r.Short(), r.Status, r.Error)
	} else {
		fmt.Fprintf(w, "run %s %s\n", r.Short(), r.Status)
	}

	evs, _, _ := state.EventsFrom(home, r.ID, 0)
	var denied []string
	for _, ev := range evs {
		if ev.Type != "permission.denied" {
			continue
		}
		what, _, _ := strings.Cut(fmt.Sprint(ev.Data["input"]), "\n")
		by := "nobody to ask"
		if ev.Data["by"] == "auto mode" {
			by = "auto mode"
		}
		denied = append(denied, fmt.Sprintf("%s (%s) %v: %s (%s)", ev.Step, ev.Seat, ev.Data["tool"], what, by))
	}
	if len(denied) > 0 {
		fmt.Fprintf(w, "  ⊘ %d request(s) denied:\n", len(denied))
		for _, d := range denied {
			fmt.Fprintf(w, "    %s\n", d)
		}
	}
	kept := 0
	for _, s := range r.Seats {
		if _, err := os.Stat(s.Dir); s.Worktree && err == nil {
			kept++
		}
	}
	if r.Status != state.Succeeded && kept > 0 {
		fmt.Fprintf(w, "  %d worktree(s) kept (on_finish.failure: keep); laight abort %s deletes them\n", kept, r.Short())
	}
}
